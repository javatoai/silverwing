package networkproxy

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
)

func proxyConfig(endpoint string) domain.AppConfig {
	return domain.AppConfig{CommandProxyURL: &endpoint, CommandProxyTargets: []domain.CommandProxyTarget{domain.ProxyTargetAppUpdates}}
}

func TestUpdateProxyOffIgnoresEnvironmentAndOtherTargets(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://inherited.invalid:99")
	t.Setenv("HTTP_PROXY", "http://inherited.invalid:99")
	t.Setenv("ALL_PROXY", "socks5://inherited.invalid:99")
	for _, config := range []domain.AppConfig{domain.DefaultAppConfig(), {CommandProxyTargets: []domain.CommandProxyTarget{domain.ProxyTargetGit}}} {
		client, err := NewHTTPClient(config, domain.ProxyTargetAppUpdates, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if client.Transport.(redactingTransport).Proxy != nil {
			t.Fatal("disabled update proxy inherited a route")
		}
		client.CloseIdleConnections()
	}
}

func TestUpdateProxySchemesAuthenticationAndExceptions(t *testing.T) {
	user, password, noProxy := "proxy user", "p@ss:/word", ".example.org,10.0.0.0/8"
	for _, scheme := range []string{"http", "https", "socks5"} {
		config := proxyConfig(scheme + "://proxy.example:7897")
		config.CommandProxyUsername, config.CommandProxyPassword, config.CommandProxyNoProxy = &user, &password, &noProxy
		client, err := NewHTTPClient(config, domain.ProxyTargetAppUpdates, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		transport := client.Transport.(redactingTransport)
		for _, address := range []string{"https://api.github.com/releases", "https://github.com/download", "https://release-assets.githubusercontent.com/file"} {
			request, _ := http.NewRequest(http.MethodGet, address, nil)
			proxy, err := transport.Proxy(request)
			if err != nil || proxy == nil || proxy.Scheme != scheme || proxy.Host != "proxy.example:7897" || proxy.User.Username() != user {
				t.Fatalf("route %s: %v %v", address, proxy, err)
			}
			if actual, _ := proxy.User.Password(); actual != password {
				t.Fatal("proxy password changed")
			}
		}
		for _, address := range []string{"https://team.example.org/file", "https://10.2.3.4/file"} {
			request, _ := http.NewRequest(http.MethodGet, address, nil)
			if proxy, err := transport.Proxy(request); err != nil || proxy != nil {
				t.Fatal("exception routed through proxy", address, err)
			}
		}
		client.CloseIdleConnections()
	}
	if _, err := NewHTTPClient(proxyConfig(""), domain.ProxyTargetAppUpdates, time.Second); err == nil {
		t.Fatal("enabled missing proxy silently connected directly")
	}
}

func TestHTTPSUpdateRequestUsesAuthenticatedConnectTunnel(t *testing.T) {
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" {
			t.Error("proxy credentials reached origin")
		}
		io.WriteString(w, "verified package transport")
	}))
	defer origin.Close()
	user, password := "fixture-user", "fixture-secret"
	connects := make(chan string, 1)
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Header.Get("Proxy-Authorization") != "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+password)) {
			t.Error("missing CONNECT proxy authentication")
			http.Error(w, "auth", 407)
			return
		}
		remote, err := net.DialTimeout("tcp", origin.Listener.Addr().String(), time.Second)
		if err != nil {
			t.Error(err)
			return
		}
		conn, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			remote.Close()
			t.Error(err)
			return
		}
		connects <- r.Host
		buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		buffer.Flush()
		go func() { defer remote.Close(); defer conn.Close(); io.Copy(conn, remote) }()
		io.Copy(remote, buffer)
		remote.Close()
		conn.Close()
	}))
	defer proxy.Close()
	config := proxyConfig(proxy.URL)
	config.CommandProxyUsername, config.CommandProxyPassword = &user, &password
	client, err := NewHTTPClient(config, domain.ProxyTargetAppUpdates, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	transport := client.Transport.(redactingTransport)
	transport.TLSClientConfig = origin.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	transport.TLSClientConfig.ServerName = origin.Certificate().DNSNames[0]
	response, err := client.Get("https://github.update.test/package")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil || string(data) != "verified package transport" {
		t.Fatal("tunneled response", err)
	}
	if host := <-connects; host != "github.update.test:443" {
		t.Fatal("unexpected CONNECT target", host)
	}
}

func TestUpdateProxyErrorsRedactSecretsAndPreserveCancellation(t *testing.T) {
	user, password := "fixture-user", "fixture-secret"
	config := proxyConfig("http://proxy.example:7897")
	config.CommandProxyUsername, config.CommandProxyPassword = &user, &password
	client, err := NewHTTPClient(config, domain.ProxyTargetAppUpdates, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	transport := client.Transport.(redactingTransport)
	transport.DialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, fmt.Errorf("proxy.example:7897 %s %s: %w", user, password, context.Canceled)
	}
	_, err = client.Get("https://github.update.test/package")
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation was lost", err)
	}
	for _, secret := range []string{user, password, "proxy.example:7897"} {
		if strings.Contains(err.Error(), secret) {
			t.Fatal("proxy detail leaked")
		}
	}
}
