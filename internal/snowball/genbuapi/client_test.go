package genbuapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestQueryServicePodsHonorsContextCancellation(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == podGroupPath {
			close(started)
			<-r.Context().Done()
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{BeastURL: server.URL, GenbuURL: server.URL, Token: "test-token", Timeout: time.Minute, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	_, err = client.QueryServicePods(ctx, ServicePodsRequest{Service: "fp-message-push", Environment: "uat"})
	if err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("QueryServicePods error = %v, want context cancellation", err)
	}
}

func TestConcurrentAuthenticationRefreshUsesOneLogin(t *testing.T) {
	var loginCalls atomic.Int32
	freshToken := makeJWT(time.Now().Add(time.Hour).Unix())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case loginPath:
			loginCalls.Add(1)
			writeTestJSON(w, map[string]any{"code": "000000", "data": map[string]any{"token": "beast-token"}})
		case dragonUserPath:
			writeTestJSON(w, map[string]any{"code": 20000, "token": freshToken})
		case podGroupPath:
			if r.Header.Get("X-Token") != freshToken {
				w.WriteHeader(http.StatusUnauthorized)
				writeTestJSON(w, map[string]any{"message": "expired"})
				return
			}
			writeTestJSON(w, map[string]any{"code": 20000, "response": map[string]any{"groups": []any{}}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BeastURL: server.URL, GenbuURL: server.URL, Username: "user", Password: "password",
		Token: "stale-token", TokenExpiresAt: time.Now().Add(time.Hour).Unix(),
		Timeout: time.Second, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := client.QueryServicePods(context.Background(), ServicePodsRequest{Service: "fp-message-push", Environment: "uat"})
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatalf("concurrent QueryServicePods error = %v", err)
		}
	}
	if got := loginCalls.Load(); got != 1 {
		t.Fatalf("login requests = %d, want one synchronized refresh", got)
	}
	if client.SessionSnapshot().Token != freshToken {
		t.Fatal("refreshed token was not retained in the in-memory config")
	}
}

func TestReadRefreshesRejectedBusinessToken(t *testing.T) {
	var loginCalls atomic.Int32
	freshToken := makeJWT(time.Now().Add(time.Hour).Unix())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case loginPath:
			loginCalls.Add(1)
			writeTestJSON(w, map[string]any{"code": "000000", "data": map[string]any{"token": "beast-token"}})
		case dragonUserPath:
			writeTestJSON(w, map[string]any{"code": 20000, "token": freshToken})
		case podGroupPath:
			if r.Header.Get("X-Token") != freshToken {
				writeTestJSON(w, map[string]any{"code": 401, "message": "expired"})
				return
			}
			writeTestJSON(w, map[string]any{"code": 20000, "response": map[string]any{"groups": []any{}}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client, err := NewClient(Config{
		BeastURL: server.URL, GenbuURL: server.URL, Username: "user", Password: "password",
		Token: "stale-token", TokenExpiresAt: time.Now().Add(time.Hour).Unix(),
		Timeout: time.Second, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.QueryServicePods(context.Background(), ServicePodsRequest{Service: "fp-message-push", Environment: "uat"})
	if err != nil || loginCalls.Load() != 1 {
		t.Fatalf("QueryServicePods() error = %v, login requests = %d", err, loginCalls.Load())
	}
}

func TestQueryServiceConfigRedactsBeforeFiltering(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != configMapListPath {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		writeTestJSON(w, map[string]any{"code": 20000, "response": []any{
			map[string]any{"update_time": "2026-08-12 22:00:00", "encoded_data": []any{
				map[string]any{"content": "spring.datasource.password=hidden-value\nvisible.setting=ready"},
			}},
		}})
	}))
	defer server.Close()

	client, err := NewClient(Config{
		BeastURL: server.URL, GenbuURL: server.URL, Username: "user", Password: "hidden-value",
		Token: "fresh-token", TokenExpiresAt: time.Now().Add(time.Hour).Unix(), Timeout: time.Second, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.QueryServiceConfig(context.Background(), ServiceConfigRequest{
		Service: "fp-message-push", Environment: "uat", Namespace: "fp", Filter: "hidden-value",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(result.Content, "hidden-value") || !strings.Contains(result.Content, "password=<redacted>") {
		t.Fatalf("unsafe or unexpectedly unmasked config content: %q", result.Content)
	}
	if len(result.Matches) != 0 {
		t.Fatalf("filter matched a secret that was redacted first: %v", result.Matches)
	}
}

func TestResolveServiceAndEnvironmentCatalog(t *testing.T) {
	service := ResolveService("ATS")
	if !service.BuiltIn || service.Name != "fp-android-transit-service" {
		t.Fatalf("resolved service = %#v", service)
	}
	if len(ListServices()) == 0 || strings.Join(ListEnvironments(), ",") != "uat,prod" {
		t.Fatal("logical Genbu catalogs are empty or unexpected")
	}
}

func makeJWT(exp int64) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"exp":%d}`, exp)))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{}`))
	return header + "." + payload + ".signature"
}

func writeTestJSON(w http.ResponseWriter, value any) {
	_ = json.NewEncoder(w).Encode(value)
}
