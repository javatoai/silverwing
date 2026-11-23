package production

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"silverwing-go/internal/commandruntime"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/snowball/genbuapi"
)

type genbuFixtureTransport struct{ base http.RoundTripper }

func (transport genbuFixtureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if err := commandruntime.CheckAdmission(request.Context()); err != nil {
		return nil, err
	}
	return transport.base.RoundTrip(request)
}

func newNativeGenbu(t *testing.T, handle http.HandlerFunc) NativeGenbu {
	t.Helper()
	server := httptest.NewServer(handle)
	t.Cleanup(server.Close)
	httpClient := server.Client()
	httpClient.Transport = genbuFixtureTransport{base: httpClient.Transport}
	client, err := genbuapi.NewClient(genbuapi.Config{
		BeastURL: server.URL, GenbuURL: server.URL, Timeout: 5 * time.Second,
		Username: "fixture-user", Password: "fixture-secret", Token: "fixture-token",
		TokenExpiresAt: time.Now().Add(time.Hour).Unix(), HTTPClient: httpClient,
	})
	if err != nil {
		t.Fatal(err)
	}
	return NativeGenbu{
		Client: func(context.Context, domain.AppConfig) (*genbuapi.Client, error) { return client, nil },
		Identity: func(context.Context, domain.AppConfig) (string, error) {
			return "fixture-account", nil
		},
	}
}

func writeGenbuResponse(w http.ResponseWriter, value any) {
	_ = json.NewEncoder(w).Encode(value)
}

func TestNativeGenbuCheckScopePrecedesClientAndIdentity(t *testing.T) {
	ctx := context.Background()
	cfg := domain.DefaultAppConfig()
	retired := errors.New("retired capability")
	client := NativeGenbu{
		Require: func(context.Context) error { return retired },
		Client: func(context.Context, domain.AppConfig) (*genbuapi.Client, error) {
			t.Error("retired scope reached client acquisition")
			return nil, nil
		},
		Identity: func(context.Context, domain.AppConfig) (string, error) {
			t.Error("retired scope reached identity")
			return "", nil
		},
	}
	status, err := client.Status(ctx, cfg, "pc", "2.0.0")
	if !errors.Is(err, retired) || status.Build != domain.GenbuUnknown || status.CheckedAt == nil {
		t.Errorf("Status() = %+v, %v", status, err)
	}
	if _, err := client.AccountIdentity(ctx, cfg); !errors.Is(err, retired) {
		t.Errorf("AccountIdentity() error = %v", err)
	}
}

func TestNativeGenbuPodsAndVersions(t *testing.T) {
	client := newNativeGenbu(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("env") != "PRD" || r.Header.Get("App-Id") != "10010003" {
			t.Errorf("unexpected pod query: %s, app %s", r.URL, r.Header.Get("App-Id"))
		}
		switch r.URL.Path {
		case genbuapi.PodGroupPath:
			writeGenbuResponse(w, map[string]any{"response": map[string]any{"groups": []map[string]any{
				{"group_id": 1, "name": "stable", "namespace": "mp", "match_labels": "test"},
			}}})
		case genbuapi.PodPath:
			writeGenbuResponse(w, map[string]any{"response": []map[string]any{
				{"pod_name": "active", "app_version": "2.0.2", "phase": "Running", "ready": true, "restart_count": 2},
				{"pod_name": "active", "app_version": "2.0.2", "phase": "Running", "ready": true, "restart_count": 2},
				{"pod_name": "pending", "app_version": "2.0.1", "phase": "Pending", "ready": false, "restart_count": 0},
			}})
		default:
			t.Errorf("unexpected request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	ctx := context.Background()
	cfg := domain.DefaultAppConfig()
	pods, err := client.Pods(ctx, cfg, "prod", "PC")
	if err != nil || pods.Environment != "prod" || pods.Service != "mp-pay-center" || len(pods.Pods) != 2 {
		t.Fatalf("Pods() = %+v, %v", pods, err)
	}
	if !pods.Pods[0].Ready || pods.Pods[0].Restarts != 2 || pods.Pods[1].Ready || pods.Pods[1].Phase != "Pending" {
		t.Errorf("unexpected pod data: %+v", pods.Pods)
	}
	versions, err := client.Versions(ctx, cfg, "PC")
	if err != nil || !reflect.DeepEqual(versions, []string{"2.0.1", "2.0.2"}) {
		t.Errorf("Versions() = %v, %v", versions, err)
	}
}

func TestNativeGenbuPipelineStatusAndBuildLog(t *testing.T) {
	const completedAt = "2026-11-19 16:03:00"
	client := newNativeGenbu(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case genbuapi.VersionPath:
			writeGenbuResponse(w, map[string]any{"response": []map[string]any{
				{"version_id": 42, "version_name": "2.0.0", "app_name": "mp-pay-center"},
				{"version_id": 43, "version_name": "2.0.0-rc", "app_name": "mp-pay-center"},
			}})
		case genbuapi.BaseInfoPath:
			if r.URL.Query().Get("version_id") != "42" {
				t.Errorf("queried a non-exact Tag: %s", r.URL)
			}
			writeGenbuResponse(w, map[string]any{"response": []map[string]any{
				{"step": 1, "status": 1, "created_time": completedAt},
				{"step": 7, "status": 2, "created_time": completedAt},
				{"step": 9, "status": 0, "created_time": completedAt},
			}})
		case genbuapi.BuildLogPath:
			if r.URL.Query().Get("version_id") != "42" || r.URL.Query().Get("tail_lines") != "100" {
				t.Errorf("unexpected build log query: %s", r.URL)
			}
			writeGenbuResponse(w, map[string]any{"response": "build ran with fixture-secret and fixture-token"})
		default:
			t.Errorf("unexpected request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	ctx := context.Background()
	cfg := domain.DefaultAppConfig()
	status, err := client.Status(ctx, cfg, "pc", "2.0.0")
	if err != nil || status.CheckedAt == nil || status.Build != domain.GenbuSuccess ||
		status.UAT != domain.GenbuFailed || status.Production != domain.GenbuInitial ||
		status.BuiltCompletedAt == nil || *status.BuiltCompletedAt != completedAt ||
		status.ReleasedCompletedAt != nil || status.ProductionReleasedCompletedAt != nil {
		t.Errorf("Status() = %+v, %v", status, err)
	}
	log, err := client.BuildLog(ctx, cfg, "pc", "2.0.0", 0)
	if err != nil || !strings.Contains(log, "build ran") || !strings.HasSuffix(log, "\n") ||
		strings.Contains(log, "fixture-secret") || strings.Contains(log, "fixture-token") {
		t.Error("BuildLog() failed to return a safe readable log", err)
	}
}

func TestNativeGenbuStatusDistinguishesMissingTagOrService(t *testing.T) {
	for _, tc := range []struct {
		name, service, path string
		body                any
		httpStatus          int
		missing             bool
	}{
		{"missing exact tag", "pc", genbuapi.VersionPath, []map[string]any{{"version_id": 42, "version_name": "2.0.0-other"}}, http.StatusOK, true},
		{"missing service", "unlisted-fixture", genbuapi.ApplicationsPath, []any{}, http.StatusOK, true},
		{"server query failure", "pc", genbuapi.VersionPath, "未找到例程 Tag", http.StatusBadGateway, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newNativeGenbu(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != tc.path {
					t.Errorf("unexpected request: %s", r.URL)
				}
				w.WriteHeader(tc.httpStatus)
				writeGenbuResponse(w, map[string]any{"response": tc.body})
			})
			status, err := client.Status(context.Background(), domain.DefaultAppConfig(), tc.service, "2.0.0")
			if status.NotFound != tc.missing || (err == nil) != tc.missing || status.CheckedAt == nil ||
				status.Build != domain.GenbuUnknown || status.UAT != domain.GenbuUnknown || status.Production != domain.GenbuUnknown {
				t.Fatalf("Status() = %+v, %v", status, err)
			}
		})
	}
}

func TestProductionMissingGenbuTagRetainsLastKnownBuild(t *testing.T) {
	const warning = "Genbu 未找到服务或 Tag"
	ctx := context.Background()
	f := newFixture(t)
	b := f.create()
	checkedAt := "2026-11-19T08:00:00Z"
	builtAt, releasedAt, productionAt := "2026-11-19 15:30:00", "2026-11-19 15:40:00", "2026-11-19 15:50:00"
	confirmed := domain.GenbuTagProbeStatus{
		Build: domain.GenbuSuccess, UAT: domain.GenbuSuccess, Production: domain.GenbuSuccess,
		CheckedAt: &checkedAt, BuiltCompletedAt: &builtAt, ReleasedCompletedAt: &releasedAt, ProductionReleasedCompletedAt: &productionAt,
	}
	b.Tags = []Tag{{Name: b.Version, Status: confirmed}}
	if err := f.s.Store.Save(&b); err != nil {
		t.Fatal(err)
	}

	var found atomic.Bool
	const newBuildTime = "2026-11-19 16:00:00"
	f.s.Genbu = newNativeGenbu(t, func(w http.ResponseWriter, r *http.Request) {
		var response any
		switch r.URL.Path {
		case genbuapi.ApplicationsPath:
			response = []map[string]any{{"app_id": "1", "app_name": b.Target.GenbuService}}
		case genbuapi.PodGroupPath:
			response = map[string]any{"groups": []map[string]any{
				{"group_id": 1, "name": "stable", "namespace": "fixture", "match_labels": "test"},
			}}
		case genbuapi.PodPath:
			response = []map[string]any{{"pod_name": "active", "app_version": "2.3.43", "phase": "Running", "ready": true, "restart_count": 0}}
		case genbuapi.VersionPath:
			versions := []map[string]any{}
			if found.Load() {
				versions = append(versions, map[string]any{"version_id": 42, "version_name": b.Version})
			}
			response = versions
		case genbuapi.BaseInfoPath:
			response = []map[string]any{{"step": 1, "status": 1, "created_time": newBuildTime}}
		default:
			t.Errorf("unexpected request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
		writeGenbuResponse(w, map[string]any{"response": response})
	})

	if _, err := f.s.RefreshBuild(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	saved, err := f.s.Store.Load(b.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := saved.Tags[0].Status
	reason := got.FailureReason
	got.FailureReason = nil
	if reason == nil || *reason != warning || !reflect.DeepEqual(got, confirmed) {
		t.Errorf("missing result changed confirmed status or lost the warning: %+v", saved.Tags[0].Status)
	}
	_, preview, err := f.s.InspectBatch(ctx, b.ID)
	if err != nil || preview.ProductionTag != "2.3.43" || !strings.Contains(preview.Warning, warning) {
		t.Errorf("missing result preview warning = %q, err = %v", preview.Warning, err)
	}

	found.Store(true)
	refreshed, err := f.s.RefreshBuild(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	got = refreshed.Tags[0].Status
	if got.FailureReason != nil || got.NotFound || got.Build != domain.GenbuSuccess ||
		got.BuiltCompletedAt == nil || *got.BuiltCompletedAt != newBuildTime {
		t.Errorf("successful refresh did not replace stale status and warning: %+v", got)
	}
}

func TestNativeGenbuReadRejectsReplyAfterScopeChanges(t *testing.T) {
	var retired atomic.Bool
	scopeChanged := errors.New("shared account changed after dispatch")
	client := newNativeGenbu(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != genbuapi.PodGroupPath {
			t.Errorf("unexpected request: %s", r.URL)
		}
		retired.Store(true)
		writeGenbuResponse(w, map[string]any{"response": map[string]any{"groups": []any{}}})
	})
	client.Require = func(context.Context) error {
		if retired.Load() {
			return scopeChanged
		}
		return nil
	}
	pods, err := client.Pods(context.Background(), domain.DefaultAppConfig(), "uat", "pc")
	if !errors.Is(err, scopeChanged) || pods.Service != "" {
		t.Fatalf("Pods() returned an observation from the retired account: %+v, %v", pods, err)
	}
}
