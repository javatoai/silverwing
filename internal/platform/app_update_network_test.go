package platform

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestAppUpdateCheckAndDownloadClientsReloadSharedProxySettings(t *testing.T) {
	var apiRequests, downloadRequests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Host != "update-fixture.invalid" {
			t.Errorf("proxy received unexpected host: %s", r.URL.Host)
		}
		if r.URL.Path == "/package" {
			downloadRequests.Add(1)
			io.WriteString(w, "package")
			return
		}
		apiRequests.Add(1)
		json.NewEncoder(w).Encode(appUpdateRecord("v0.0.10", "silverwing", "0.0.10"))
	}))
	defer proxy.Close()
	s := NewService(storage.NewPaths(t.TempDir()), nil, nil)
	config := domain.DefaultAppConfig()
	config.CommandProxyURL = &proxy.URL
	config.CommandProxyTargets = []domain.CommandProxyTarget{domain.ProxyTargetAppUpdates}
	if err := s.config.Save(config); err != nil {
		t.Fatal(err)
	}
	s.updateChecker = releaseTestChecker(proxy)
	s.updateChecker.apiBase = "http://update-fixture.invalid"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result, err := s.checkApplicationUpdate(ctx)
	if err != nil || result.Release == nil || apiRequests.Load() != 1 {
		t.Fatalf("proxied update check: %+v %v", result, err)
	}
	client, err := s.appUpdateHTTPClient(time.Second)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Get("http://update-fixture.invalid/package")
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	client.CloseIdleConnections()
	if downloadRequests.Load() != 1 {
		t.Fatal("download client bypassed settings proxy")
	}
	config.CommandProxyTargets = []domain.CommandProxyTarget{domain.ProxyTargetGit}
	if err := s.config.Save(config); err != nil {
		t.Fatal(err)
	}
	direct := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(appUpdateRecord("v0.0.10", "silverwing", "0.0.10"))
	}))
	defer direct.Close()
	s.updateChecker.apiBase = direct.URL
	if _, err := s.checkApplicationUpdate(ctx); err != nil {
		t.Fatal(err)
	}
	if apiRequests.Load() != 1 {
		t.Fatal("disabled update proxy still used")
	}
	config.CommandProxyTargets = []domain.CommandProxyTarget{domain.ProxyTargetAppUpdates}
	if err := s.config.Save(config); err != nil {
		t.Fatal(err)
	}
	s.updateChecker.apiBase = "http://update-fixture.invalid"
	if _, err := s.checkApplicationUpdate(ctx); err != nil || apiRequests.Load() != 2 {
		t.Fatal("reenabling proxy required restart", err)
	}
	restarted := NewService(s.paths, nil, nil)
	restarted.updateChecker = releaseTestChecker(proxy)
	restarted.updateChecker.apiBase = "http://update-fixture.invalid"
	if _, err := restarted.checkApplicationUpdate(ctx); err != nil || apiRequests.Load() != 3 {
		t.Fatal("proxy selection did not survive service recreation", err)
	}
}
