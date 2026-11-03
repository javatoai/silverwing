package platform

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func appUpdateRecord(tag, executable, version string) map[string]any {
	base := "https://github.com/owner/repo/releases/"
	assets := []map[string]any{}
	for _, suffix := range []string{"windows-x64-setup.exe", "windows-x64.msi", "windows-x64-portable.zip", "macos-arm64.dmg"} {
		name := executable + "-" + version + "-" + suffix
		assets = append(assets, map[string]any{"name": name, "state": "uploaded", "size": 100, "browser_download_url": base + "download/" + tag + "/" + name})
	}
	return map[string]any{"tag_name": tag, "html_url": base + "tag/" + tag, "published_at": "2026-11-03T02:00:00Z", "assets": assets}
}

func releaseTestChecker(server *httptest.Server) *githubReleaseChecker {
	return &githubReleaseChecker{client: server.Client(), apiBase: server.URL, repository: "owner/repo", tagPrefix: "v", executable: "silverwing", goos: "windows", goarch: "amd64", currentVersion: "0.0.9"}
}

func TestGithubReleaseLatestAndPlatformPackages(t *testing.T) {
	for _, test := range []struct{ goos, goarch, suffix string }{
		{"windows", "amd64", "windows-x64-setup.exe"}, {"darwin", "arm64", "macos-arm64.dmg"}, {"linux", "arm64", ""},
	} {
		t.Run(test.goos, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/repos/owner/repo/releases/latest" || r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("User-Agent") == "" || r.Header.Get("Authorization") != "" {
					t.Errorf("unexpected public release request: %s %v", r.URL, r.Header)
				}
				json.NewEncoder(w).Encode(appUpdateRecord("v0.0.10", "silverwing", "0.0.10"))
			}))
			defer server.Close()
			checker := releaseTestChecker(server)
			checker.goos, checker.goarch = test.goos, test.goarch
			result, err := checker.check(context.Background())
			if err != nil || !result.Available("0.0.9", "owner/repo", "v") || result.CheckedAt == "" || calls != 1 {
				t.Fatalf("latest release: %+v, %v, calls=%d", result, err, calls)
			}
			if test.suffix == "" {
				if result.Release.Package != nil {
					t.Fatal("unsupported platform offered a foreign package")
				}
			} else if result.Release.Package == nil || !strings.HasSuffix(result.Release.Package.Name, test.suffix) {
				t.Fatalf("wrong package: %+v", result.Release.Package)
			}
		})
	}
}

func TestGithubReleaseEditionStableFilteringAndNumericOrdering(t *testing.T) {
	draft := appUpdateRecord("qa-v9.0.0", "silverwing-qa", "9.0.0")
	draft["draft"] = true
	beta := appUpdateRecord("qa-v8.0.0", "silverwing-qa", "8.0.0")
	beta["prerelease"] = true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/latest") {
			t.Error("edition must not use the repository's developer latest")
		}
		json.NewEncoder(w).Encode([]any{appUpdateRecord("v10.0.0", "silverwing", "10.0.0"), draft, beta,
			appUpdateRecord("qa-v0.0.9", "silverwing-qa", "0.0.9"), appUpdateRecord("qa-v0.0.10", "silverwing-qa", "0.0.10")})
	}))
	defer server.Close()
	checker := releaseTestChecker(server)
	checker.tagPrefix, checker.executable = "qa-v", "silverwing-qa"
	result, err := checker.check(context.Background())
	if err != nil || result.Release == nil || result.Release.Version != "0.0.10" || result.Release.Package == nil || !strings.HasPrefix(result.Release.Package.Name, "silverwing-qa-") {
		t.Fatalf("wrong edition result: %+v, %v", result, err)
	}
}

func TestGithubReleaseFailuresAreNotUpToDate(t *testing.T) {
	for _, code := range []int{403, 429, 404, 500} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) }))
		result, err := releaseTestChecker(server).check(context.Background())
		server.Close()
		if err == nil || result.CheckedAt != "" || result.Release != nil {
			t.Fatalf("HTTP %d became a successful check: %+v, %v", code, result, err)
		}
	}
	for _, body := range []string{"null", "<html>gateway</html>", strings.Repeat("x", (4<<20)+1)} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		_, err := releaseTestChecker(server).check(context.Background())
		server.Close()
		if err == nil {
			t.Fatal("malformed or oversized response accepted")
		}
	}
}

func TestGithubReleaseInvalidPublishedLinkDoesNotBecomeSuccessfulEmptyResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record := appUpdateRecord("v0.0.10", "silverwing", "0.0.10")
		record["html_url"] = "https://example.com/untrusted"
		json.NewEncoder(w).Encode(record)
	}))
	defer server.Close()
	result, err := releaseTestChecker(server).check(context.Background())
	if err == nil || result.CheckedAt != "" || result.Release != nil {
		t.Fatalf("invalid published metadata cleared the old notification: %+v %v", result, err)
	}
}

func TestGithubReleaseCancellationAndExistingDispatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(appUpdateRecord("v0.0.9", "silverwing", "0.0.9"))
	}))
	defer server.Close()
	checker := releaseTestChecker(server)
	service := NewService(storage.NewPaths(t.TempDir()), nil, nil)
	service.updateChecker = checker
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Execute(ctx, "update.check", nil); err == nil {
		t.Fatal("cancelled check succeeded")
	}
	ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	data, err := service.Execute(ctx, "update.check", nil)
	if err != nil {
		t.Fatal(err)
	}
	result, ok := data.(domain.UpdateCheckResult)
	if !ok || result.Release == nil || result.Available("0.0.9", "owner/repo", "v") {
		t.Fatalf("existing Dispatch did not return a current-version result: %+v", data)
	}
}
