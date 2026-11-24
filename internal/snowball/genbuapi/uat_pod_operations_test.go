package genbuapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const uatRedeployTestGroup = "mp-pay-center-sidecar-a"

func TestRedeployUATPodsRequest(t *testing.T) {
	for _, tc := range []struct {
		service string
		dryRun  bool
		status  string
		writes  int32
	}{
		{service: "PC", dryRun: true, status: "preview"},
		{service: "mp-pay-center", status: "submitted", writes: 1},
	} {
		t.Run(tc.status, func(t *testing.T) {
			var groupReads, podReads, admissions, writes atomic.Int32
			client := newUATRedeployTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("App-Id") != "10010003" || r.Header.Get("System-Code") != "ANDCN" ||
					r.Header.Get("X-Token") != "uat-pod-test-token" {
					t.Errorf("missing Genbu headers for %s %s", r.Method, r.URL.Path)
				}
				switch r.URL.Path {
				case "/genbu/basic/api/podGroup":
					groupReads.Add(1)
					expectUATRedeployRequest(t, r, http.MethodGet, url.Values{"env": {"UAT"}})
					writeUATRedeployTestGroups(w, []map[string]any{uatRedeployTestGroupRow()})
				case "/genbu/basic/api/pod":
					podReads.Add(1)
					expectUATRedeployRequest(t, r, http.MethodGet, url.Values{
						"env": {"UAT"}, "group_id": {"7"}, "namespace": {"mp"}, "match_labels": {"app=pc,version=a"},
					})
					writeUATRedeployTestPods(w, []map[string]any{{"pod_name": "pay-center-pod", "restart_count": 0}})
				case "/genbu/release/operate":
					writes.Add(1)
					if admissions.Load() != 1 {
						t.Error("mutation reached the server before the admission callback")
					}
					expectUATRedeployRequest(t, r, http.MethodPatch, url.Values{})
					if r.Header.Get("Content-Type") != "application/json" {
						t.Error("missing JSON content type")
					}
					want := map[string]any{
						"group_id": float64(7), "env": "UAT", "deleteDeployment": true, "business_id": "", "approve_type": "",
					}
					var payload map[string]any
					if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || !reflect.DeepEqual(payload, want) {
						t.Errorf("redeploy payload = %v, error = %v, want %v", payload, err, want)
					}
					writeTestJSON(w, map[string]any{"code": 20000})
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			})
			ctx := context.Background()
			request := UATPodOperationRequest{Action: "redeploy", Service: tc.service}
			preview, err := client.PreviewUATPodOperation(ctx, request)
			if err != nil || preview.Status != "preview" || len(preview.PreviewID) != 64 || writes.Load() != 0 {
				t.Fatalf("redeploy preview = %+v, writes = %d, error = %v", preview, writes.Load(), err)
			}
			got, reads := preview, int32(1)
			if !tc.dryRun {
				request.Service = "PC"
				got, err = client.SubmitUATPodOperation(ctx, request, preview.PreviewID, func(_ context.Context, target UATPodOperationResult) error {
					admissions.Add(1)
					if target != preview {
						t.Errorf("admitted target = %+v, want %+v", target, preview)
					}
					return nil
				})
				reads++
			}
			want := UATPodOperationResult{
				Service: "mp-pay-center", AppID: "10010003", Environment: "UAT",
				GroupID: 7, Group: uatRedeployTestGroup, Namespace: "mp", PodCount: 1,
				Action: "redeploy", Status: tc.status, PreviewID: preview.PreviewID,
			}
			if got != want || err != nil {
				t.Fatalf("redeploy result = %+v, error = %v, want %+v", got, err, want)
			}
			if groupReads.Load() != reads || podReads.Load() != reads || writes.Load() != tc.writes {
				t.Errorf("got %d group reads, %d Pod reads and %d writes", groupReads.Load(), podReads.Load(), writes.Load())
			}
		})
	}
}

func TestRedeployUATPodsRejectsUnavailableGroup(t *testing.T) {
	group := uatRedeployTestGroupRow()
	second := uatRedeployTestGroupRow()
	second["group_id"] = 8
	second["pod_group_name"] = "alt-group"
	invalid := uatRedeployTestGroupRow()
	invalid["k8s_namespace"] = ""
	wrongEnvironment := uatRedeployTestGroupRow()
	wrongEnvironment["env"] = "PRD"
	wrongApplication := uatRedeployTestGroupRow()
	wrongApplication["app_id"] = 11010016
	for _, tc := range []struct {
		name   string
		groups []map[string]any
		pods   []map[string]any
		want   string
	}{
		{"no groups", []map[string]any{}, nil, "UAT 未找到 Pod Group"},
		{"ambiguous groups", []map[string]any{group, second}, nil, "mp/alt-group、mp/" + uatRedeployTestGroup},
		{"invalid namespace", []map[string]any{invalid}, nil, "缺少有效的 ID、名称或命名空间"},
		{"wrong environment", []map[string]any{wrongEnvironment}, nil, "指定服务或 UAT 环境不符"},
		{"wrong application", []map[string]any{wrongApplication}, nil, "指定服务或 UAT 环境不符"},
		{"no Pods", []map[string]any{group}, []map[string]any{}, "没有 Pod"},
		{"placeholder Pod", []map[string]any{group}, []map[string]any{{"pod_name": "pod-1", "restart_count": 0, "pod_id": nil}}, "Pod 缺少 ID"},
		{"Pod with wrong environment", []map[string]any{group}, []map[string]any{{"pod_name": "pod-1", "restart_count": 0, "pod_id": 91, "env": "PROD"}}, "指定服务或 UAT 环境不符"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newUATRedeployTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == podGroupPath && r.Method == http.MethodGet:
					writeUATRedeployTestGroups(w, tc.groups)
				case r.URL.Path == podPath && r.Method == http.MethodGet && tc.pods != nil:
					writeUATRedeployTestPods(w, tc.pods)
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			})
			result, err := client.PreviewUATPodOperation(context.Background(), UATPodOperationRequest{Action: "redeploy", Service: "pc"})
			if result != (UATPodOperationResult{}) || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("redeploy result = %+v, error = %v, want error containing %q", result, err, tc.want)
			}
		})
	}
}

func TestRedeployUATPodsNeverRetriesSubmission(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		body       string
		want       string
		wantStatus string
		wantErr    error
	}{
		{"timeout", 0, "", "结果不明", "unknown", ErrUATPodSubmitUnknown},
		{"malformed response", 200, `{"code":`, "结果不明", "unknown", ErrUATPodSubmitUnknown},
		{"missing receipt", 200, `{}`, "结果不明", "unknown", ErrUATPodSubmitUnknown},
		{"multiple receipts", 200, `{"code":20000} {"code":40001}`, "结果不明", "unknown", ErrUATPodSubmitUnknown},
		{"redirect", 307, "", "结果不明", "unknown", ErrUATPodSubmitUnknown},
		{"server error", 500, "private-value", "结果不明", "unknown", ErrUATPodSubmitUnknown},
		{"HTTP authentication failure", 401, "private-value", "HTTP 401", "rejected", ErrUATPodSubmitRejected},
		{"business rejection", 200, `{"code":"40001","response":"private-value"}`, "业务码 40001", "rejected", ErrUATPodSubmitRejected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var writes atomic.Int32
			accepted := make(chan struct{})
			client := newUATRedeployTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case podGroupPath:
					writeUATRedeployTestGroups(w, []map[string]any{uatRedeployTestGroupRow()})
				case podPath:
					writeUATRedeployTestPods(w, []map[string]any{{"pod_name": "pod-1", "restart_count": 0, "pod_id": 91}})
				case uatRedeployPath:
					first := writes.Add(1) == 1
					if tc.status == 0 {
						_, _ = io.Copy(io.Discard, r.Body)
						if first {
							close(accepted)
						}
						<-r.Context().Done()
						return
					}
					if tc.status == http.StatusTemporaryRedirect {
						w.Header().Set("Location", "/never-follow")
					}
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, tc.body)
				default:
					t.Errorf("mutation triggered an unexpected request %s %s", r.Method, r.URL.Path)
					http.NotFound(w, r)
				}
			})
			if tc.status == 0 {
				client.httpClient.Timeout = time.Second
			}
			ctx := context.Background()
			request := UATPodOperationRequest{Action: "redeploy", Service: "pc"}
			preview, err := client.PreviewUATPodOperation(ctx, request)
			if err != nil {
				t.Fatalf("preview failed: %v", err)
			}
			result, err := client.SubmitUATPodOperation(ctx, request, preview.PreviewID, nil)
			if tc.status == 0 {
				select {
				case <-accepted:
				default:
					t.Fatal("server did not receive the request before the timeout")
				}
			}
			if !errors.Is(err, tc.wantErr) || !strings.Contains(err.Error(), tc.want) || result.Status != tc.wantStatus || result.GroupID != 7 {
				t.Fatalf("redeploy result = %+v, error = %v, want error containing %q", result, err, tc.want)
			}
			if writes.Load() != 1 || IsAuthenticationError(err) || strings.Contains(err.Error(), "private-value") {
				t.Errorf("writes = %d, error = %v", writes.Load(), err)
			}
		})
	}
}

func TestRedeployUATPodsBindsPodIdentity(t *testing.T) {
	var snapshot atomic.Int32
	client := newUATRedeployTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case podGroupPath:
			writeUATRedeployTestGroups(w, []map[string]any{uatRedeployTestGroupRow()})
		case podPath:
			first := map[string]any{"pod_name": "pod-1", "restart_count": 0, "pod_id": 91}
			second := map[string]any{"pod_name": "pod-2", "restart_count": 0, "pod_id": 92}
			pods := []map[string]any{first, second}
			switch snapshot.Load() {
			case 1:
				first["ready"] = true
				first["restart_count"] = 1
				pods = []map[string]any{second, first}
			case 2:
				first["pod_id"] = 93
			}
			writeUATRedeployTestPods(w, pods)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	request := UATPodOperationRequest{Action: "redeploy", Service: "PC"}
	preview, err := client.PreviewUATPodOperation(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Store(1)
	same, err := client.PreviewUATPodOperation(ctx, request)
	if err != nil || same.PreviewID != preview.PreviewID {
		t.Fatalf("Pod order or health changed the preflight: %+v, %v", same, err)
	}
	snapshot.Store(2)
	_, err = client.SubmitUATPodOperation(ctx, request, preview.PreviewID, func(context.Context, UATPodOperationResult) error {
		t.Error("stale operation reached the admission callback")
		return nil
	})
	if !errors.Is(err, ErrUATPodPreviewStale) {
		t.Fatalf("expected a stale preflight after Pod replacement, got %v", err)
	}
}

func TestUATPodOperationHonorsAdmission(t *testing.T) {
	var requests atomic.Int32
	client := newUATRedeployTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case podGroupPath:
			writeUATRedeployTestGroups(w, []map[string]any{uatRedeployTestGroupRow()})
		case podPath:
			writeUATRedeployTestPods(w, []map[string]any{{"pod_name": "pod-1", "restart_count": 0, "pod_id": 91}})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	request := UATPodOperationRequest{Action: "redeploy", Service: "PC"}
	_, err := client.SubmitUATPodOperation(ctx, request, "", nil)
	if !errors.Is(err, ErrUATPodPreviewStale) || requests.Load() != 0 {
		t.Fatalf("missing preflight validation sent %d requests, error %v", requests.Load(), err)
	}
	preview, err := client.PreviewUATPodOperation(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	blocked := &authenticationError{message: "test admission rejection"}
	_, err = client.SubmitUATPodOperation(ctx, request, preview.PreviewID, func(context.Context, UATPodOperationResult) error {
		return blocked
	})
	if err != blocked || requests.Load() != 4 {
		t.Fatalf("admission rejection sent %d requests, error %v", requests.Load(), err)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	_, err = client.SubmitUATPodOperation(ctx, request, preview.PreviewID, func(context.Context, UATPodOperationResult) error {
		cancel()
		return nil
	})
	var notSent RequestNotSentError
	if !errors.As(err, &notSent) || !errors.Is(err, context.Canceled) || requests.Load() != 6 {
		t.Fatalf("cancellation before dispatch sent %d requests, error %v", requests.Load(), err)
	}
}

func TestUATPodOperationRefreshesOnlyAuthenticatedReads(t *testing.T) {
	var groupReads, logins, writes atomic.Int32
	freshToken := makeJWT(time.Now().Add(time.Hour).Unix())
	client := newUATRedeployTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case loginPath:
			logins.Add(1)
			writeTestJSON(w, map[string]any{"code": "000000", "data": map[string]any{"token": "beast-token"}})
		case dragonUserPath:
			writeTestJSON(w, map[string]any{"code": 20000, "token": freshToken})
		case podGroupPath:
			if groupReads.Add(1) > 1 && r.Header.Get("X-Token") != freshToken {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			writeUATRedeployTestGroups(w, []map[string]any{uatRedeployTestGroupRow()})
		case podPath:
			writeUATRedeployTestPods(w, []map[string]any{{"pod_name": "pod-1", "restart_count": 0, "pod_id": 91}})
		case uatRedeployPath:
			writes.Add(1)
			if r.Header.Get("X-Token") != freshToken {
				t.Error("mutation did not use the refreshed session")
			}
			w.WriteHeader(http.StatusUnauthorized)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	request := UATPodOperationRequest{Action: "redeploy", Service: "PC"}
	preview, err := client.PreviewUATPodOperation(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	admitted := 0
	_, err = client.SubmitUATPodOperation(ctx, request, preview.PreviewID, func(context.Context, UATPodOperationResult) error {
		admitted++
		return nil
	})
	if !errors.Is(err, ErrUATPodSubmitRejected) || logins.Load() != 1 || writes.Load() != 1 || admitted != 1 {
		t.Fatalf("logins=%d, writes=%d, admitted=%d, error=%v", logins.Load(), writes.Load(), admitted, err)
	}
}

func newUATRedeployTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient(Config{
		GenbuURL: server.URL, BeastURL: server.URL, Timeout: 3 * time.Second, HTTPClient: server.Client(),
		Token: "uat-pod-test-token", TokenExpiresAt: time.Now().Add(time.Hour).Unix(),
		Username: "uat-pod-test-user", Password: "uat-pod-test-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func expectUATRedeployRequest(t *testing.T, r *http.Request, method string, query url.Values) {
	t.Helper()
	if r.Method != method || !reflect.DeepEqual(r.URL.Query(), query) {
		t.Errorf("request = %s %s %v, want %s %v", r.Method, r.URL.Path, r.URL.Query(), method, query)
	}
}

func uatRedeployTestGroupRow() map[string]any {
	return map[string]any{
		"group_id": 7, "pod_group_name": uatRedeployTestGroup, "k8s_namespace": "mp", "match_labels": "app=pc,version=a",
	}
}

func writeUATRedeployTestGroups(w http.ResponseWriter, groups []map[string]any) {
	writeTestJSON(w, map[string]any{"code": 20000, "response": map[string]any{"groups": groups}})
}

func writeUATRedeployTestPods(w http.ResponseWriter, pods []map[string]any) {
	writeTestJSON(w, map[string]any{"code": 20000, "response": pods})
}
