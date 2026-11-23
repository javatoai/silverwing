package genbuapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

const configEditTestProperties = "application.properties"

func TestConfigEditPreviewAndSubmitPreserveSelectedRow(t *testing.T) {
	for _, tc := range []struct {
		name        string
		replacement string
		tie         bool
	}{
		{"UTF-8 and CRLF preserved", "\ufeff# 示例\r\nsetting: 值\r\nlast: true", false},
		{"empty replacement with equal update_time", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const file = "application.yaml"
			const original = "setting: old\r\n"
			var reads, posts atomic.Int32
			client := newConfigEditTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				checkConfigEditTestHeaders(t, r)
				if r.Method == http.MethodGet {
					reads.Add(1)
					selected := configEditTestRow(file, original)
					other := configEditTestRow(file, "other")
					other["name"] = "mp-pay-center-2.0.1"
					other["version"] = "2.0.1"
					if tc.tie {
						writeConfigEditTestRows(w, selected, other)
					} else {
						other["update_time"] = "2026-10-27 12:00:00"
						writeConfigEditTestRows(w, other, selected)
					}
					return
				}
				posts.Add(1)
				if r.Method != http.MethodPost || r.URL.RawQuery != "" || r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("unexpected submit request: %s %s %s", r.Method, r.URL.Path, r.URL.RawQuery)
				}
				decodeConfigEditTestPost(t, r, configEditTestRow(file, tc.replacement))
				writeTestJSON(w, map[string]any{"code": 20000, "response": map[string]any{"approve_id": "42"}})
			})
			req := configEditTestRequest(file)
			req.Replacement = &tc.replacement

			preview, err := client.PreviewConfigEdit(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			if preview.Status != "preview" || preview.PreviewID == "" ||
				preview.Service != "mp-pay-center" || preview.Environment != "PRD" ||
				preview.Namespace != "mp" || preview.Name != "mp-pay-center-2.0.0" || preview.File != file ||
				preview.UpdateTime != "2026-10-28 15:00:01" {
				t.Fatalf("unexpected preview: %+v", preview)
			}
			if preview.BeforeBytes == nil || *preview.BeforeBytes != len(original) ||
				preview.AfterBytes == nil || *preview.AfterBytes != len(tc.replacement) {
				t.Errorf("incorrect byte counts: %+v", preview)
			}
			if posts.Load() != 0 || reads.Load() != 1 {
				t.Fatalf("preview issued %d reads and %d posts", reads.Load(), posts.Load())
			}

			req.Service = "mp-pay-center"
			submission, err := client.SubmitConfigEdit(context.Background(), req, preview.PreviewID)
			if err != nil {
				t.Fatal(err)
			}
			if submission.Status != "submitted" || submission.ApprovalID != 42 || reads.Load() != 2 || posts.Load() != 1 {
				t.Fatalf("submission = %+v, reads = %d, posts = %d", submission, reads.Load(), posts.Load())
			}
		})
	}
}

func TestConfigEditPropertiesPreviewAndSubmit(t *testing.T) {
	const original = "# file header\r\nexample.enabled=false\r\nkeep: abc\r\ndelete=old\r\n"
	const replacement = "# file header\r\nexample.enabled=true\r\nkeep: abc\r\nnew=value=with=equals\r\n"
	var posts atomic.Int32
	var updated atomic.Bool
	client := newConfigEditTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			row := configEditTestRow(configEditTestProperties, original)
			if updated.Load() {
				row["update_time"] = "2026-10-29 09:00:00"
			}
			writeConfigEditTestRows(w, row)
			return
		}
		posts.Add(1)
		decodeConfigEditTestPost(t, r, configEditTestRow(configEditTestProperties, replacement))
		writeTestJSON(w, map[string]any{"code": 20000, "response": map[string]any{"id": 999}})
	})
	req := configEditTestRequest(configEditTestProperties)
	req.Edits = []PropertyEdit{
		{Operation: "set", Key: "example.enabled", Value: "true"},
		{Operation: "unset", Key: "delete"},
		{Operation: "add", Key: "new", Value: "value=with=equals"},
	}
	preview, err := client.PreviewConfigEdit(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	wantChanges := []ConfigEditChange{
		{Operation: "set", Key: "example.enabled"},
		{Operation: "unset", Key: "delete"},
		{Operation: "add", Key: "new"},
	}
	if preview.Status != "preview" || preview.PreviewID == "" || len(preview.ChangeID) != 64 || !reflect.DeepEqual(preview.Changes, wantChanges) ||
		preview.BeforeBytes != nil || preview.AfterBytes != nil || posts.Load() != 0 {
		t.Fatalf("unexpected property preview: %+v, posts = %d", preview, posts.Load())
	}
	data, err := json.Marshal(preview)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), req.Edits[2].Value) || strings.Contains(string(data), preview.ChangeID) {
		t.Error("preview JSON contains private edit data")
	}
	submission, err := client.SubmitConfigEdit(context.Background(), req, preview.PreviewID)
	if err != nil {
		t.Fatal(err)
	}
	if submission.Status != "submitted" || submission.ChangeID != preview.ChangeID || submission.ApprovalID != 0 || posts.Load() != 1 {
		t.Fatalf("unexpected submission: %+v, posts = %d", submission, posts.Load())
	}
	updated.Store(true)
	wholeFile := replacement
	req.Edits, req.Replacement = nil, &wholeFile
	newPreview, err := client.PreviewConfigEdit(context.Background(), req)
	if err != nil || newPreview.PreviewID == preview.PreviewID || newPreview.ChangeID != preview.ChangeID {
		t.Fatal("equivalent change failed to match after equivalent whole-file preview and metadata change", err)
	}
}

func TestConfigEditResolvesNamespace(t *testing.T) {
	for _, tc := range []struct {
		name        string
		candidates  []string
		want        string
		noConfigMap bool
	}{
		{name: "uses resolved service prefix", candidates: []string{"pc", "mp", "zeta"}, want: "mp"},
		{name: "uses only candidate", candidates: []string{"payments"}, want: "payments"},
		{name: "no ConfigMap in matched prefix", candidates: []string{"pc", "mp", "zeta"}, want: "mp", noConfigMap: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var lookups, configReads atomic.Int32
			client := newConfigEditHTTPTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				checkConfigEditReadRequest(t, r)
				switch r.URL.Path {
				case appEnvNamespacePath:
					lookups.Add(1)
					writeTestJSON(w, map[string]any{"code": 20000, "response": tc.candidates})
				case configMapListPath:
					configReads.Add(1)
					if got := r.URL.Query().Get("namespace"); got != tc.want {
						t.Errorf("GET namespace = %q, want %q", got, tc.want)
					}
					if tc.noConfigMap {
						writeConfigEditTestRows(w, []map[string]any{}...)
						return
					}
					row := configEditTestRow(configEditTestProperties, "key=old")
					row["namespace"] = tc.want
					writeConfigEditTestRows(w, row)
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
					http.NotFound(w, r)
				}
			})
			req := configEditTestRequest(configEditTestProperties)
			replacement := "key=new"
			req.Replacement = &replacement
			preview, err := client.PreviewConfigEdit(context.Background(), req)
			if tc.noConfigMap {
				if err == nil || !strings.Contains(err.Error(), "未找到可编辑的 ConfigMap") {
					t.Fatalf("preview error = %v, want no ConfigMap", err)
				}
			} else if err != nil || preview.Namespace != tc.want || preview.Service != "mp-pay-center" || preview.PreviewID == "" {
				t.Fatalf("preview = %+v, error = %v", preview, err)
			}
			if lookups.Load() != 1 || configReads.Load() != 1 {
				t.Errorf("namespace lookups = %d, ConfigMap reads = %d", lookups.Load(), configReads.Load())
			}
		})
	}
}

func TestConfigEditRejectsUnresolvedNamespace(t *testing.T) {
	for _, tc := range []struct {
		name       string
		candidates []string
		status     int
		wantError  string
	}{
		{
			name: "ambiguous candidates", candidates: []string{"zeta", "alpha"},
			wantError: "namespace（alpha, zeta）",
		},
		{name: "no candidate", candidates: []string{}, wantError: "暂无 PRD namespace"},
		{name: "lookup failure", status: http.StatusBadGateway, wantError: "HTTP 502"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var lookups atomic.Int32
			client := newConfigEditHTTPTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				checkConfigEditReadRequest(t, r)
				if r.URL.Path != appEnvNamespacePath {
					t.Errorf("unexpected endpoint after failed namespace lookup: %s", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				lookups.Add(1)
				if tc.status != 0 {
					w.WriteHeader(tc.status)
					return
				}
				writeTestJSON(w, map[string]any{"code": 20000, "response": tc.candidates})
			})
			req := configEditTestRequest(configEditTestProperties)
			replacement := "key=new"
			req.Replacement = &replacement
			if _, err := client.PreviewConfigEdit(context.Background(), req); err == nil ||
				!strings.Contains(err.Error(), tc.wantError) || strings.Contains(err.Error(), "--namespace") {
				t.Fatalf("preview error = %v, want %q", err, tc.wantError)
			}
			if _, err := client.SubmitConfigEdit(context.Background(), req, strings.Repeat("0", 64)); err == nil ||
				!strings.Contains(err.Error(), tc.wantError) || strings.Contains(err.Error(), "--namespace") {
				t.Fatalf("submit error = %v, want %q", err, tc.wantError)
			}
			if lookups.Load() != 2 {
				t.Errorf("namespace lookups = %d, want 2", lookups.Load())
			}
		})
	}
}

func TestConfigEditRejectsStalePreviewWhenNamespaceChanges(t *testing.T) {
	var changed atomic.Bool
	var lookups, configReads atomic.Int32
	client := newConfigEditHTTPTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		checkConfigEditReadRequest(t, r)
		namespace := "mp"
		if changed.Load() {
			namespace = "payments"
		}
		switch r.URL.Path {
		case appEnvNamespacePath:
			lookups.Add(1)
			writeTestJSON(w, map[string]any{"code": 20000, "response": []string{namespace}})
		case configMapListPath:
			configReads.Add(1)
			if got := r.URL.Query().Get("namespace"); got != namespace {
				t.Errorf("GET namespace = %q, want %q", got, namespace)
			}
			row := configEditTestRow(configEditTestProperties, "key=old")
			row["namespace"] = namespace
			writeConfigEditTestRows(w, row)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	req := configEditTestRequest(configEditTestProperties)
	replacement := "key=new"
	req.Replacement = &replacement
	preview, err := client.PreviewConfigEdit(context.Background(), req)
	if err != nil || preview.Namespace != "mp" {
		t.Fatalf("initial preview = %+v, error = %v", preview, err)
	}
	changed.Store(true)
	_, err = client.SubmitConfigEdit(context.Background(), req, preview.PreviewID)
	if err == nil || !strings.Contains(err.Error(), "请重新预览") {
		t.Fatalf("stale submit error = %v", err)
	}
	if lookups.Load() != 2 || configReads.Load() != 2 {
		t.Errorf("namespace lookups = %d, ConfigMap reads = %d", lookups.Load(), configReads.Load())
	}
}

func TestConfigEditRejectsStalePreview(t *testing.T) {
	for _, tc := range []struct {
		name        string
		changeRow   bool
		changeInput func(*ConfigEditRequest)
	}{
		{name: "sibling file changed", changeRow: true},
		{name: "replacement changed", changeInput: func(req *ConfigEditRequest) {
			newValue := "key: third value\r\n"
			req.Replacement = &newValue
		}},
		{name: "operation value changed", changeInput: func(req *ConfigEditRequest) {
			req.Edits = []PropertyEdit{{Operation: "set", Key: "key", Value: "updated"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var changed atomic.Bool
			client := newConfigEditTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("unexpected write for stale preview: %s", r.Method)
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				row := configEditTestRow(configEditTestProperties, "key=old\n")
				if changed.Load() {
					row["encoded_data"].([]any)[1].(map[string]any)["content"] = "other setting"
				}
				writeConfigEditTestRows(w, row)
			})
			req := configEditTestRequest(configEditTestProperties)
			if tc.name == "operation value changed" {
				req.Edits = []PropertyEdit{{Operation: "set", Key: "key", Value: "new"}}
			} else {
				newValue := "key=new\n"
				req.Replacement = &newValue
			}
			preview, err := client.PreviewConfigEdit(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			changed.Store(tc.changeRow)
			if tc.changeInput != nil {
				tc.changeInput(&req)
			}
			if _, err := client.SubmitConfigEdit(context.Background(), req, preview.PreviewID); err == nil ||
				!strings.Contains(err.Error(), "请重新预览") {
				t.Fatalf("stale submit error = %v", err)
			}
		})
	}
}

func TestConfigEditRejectsStalePreviewWhenLatestConfigMapChanges(t *testing.T) {
	var newVersion atomic.Bool
	var posts atomic.Int32
	client := newConfigEditTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			posts.Add(1)
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		initial := configEditTestRow(configEditTestProperties, "key=old")
		if !newVersion.Load() {
			writeConfigEditTestRows(w, initial)
			return
		}
		newer := configEditTestRow(configEditTestProperties, "key=old")
		newer["name"] = "mp-pay-center-2.0.1"
		newer["version"] = "2.0.1"
		newer["update_time"] = "2026-11-12 12:00:00"
		writeConfigEditTestRows(w, initial, newer)
	})
	req := configEditTestRequest(configEditTestProperties)
	replacement := "key=new"
	req.Replacement = &replacement
	preview, err := client.PreviewConfigEdit(context.Background(), req)
	if err != nil || preview.Name != "mp-pay-center-2.0.0" {
		t.Fatalf("initial preview = %+v, error = %v", preview, err)
	}

	newVersion.Store(true)
	_, err = client.SubmitConfigEdit(context.Background(), req, preview.PreviewID)
	if err == nil || !strings.Contains(err.Error(), "请重新预览") {
		t.Fatalf("stale submit error = %v", err)
	}
	if posts.Load() != 0 {
		t.Errorf("stale submit issued %d posts", posts.Load())
	}
	next, err := client.PreviewConfigEdit(context.Background(), req)
	if err != nil || next.Name != "mp-pay-center-2.0.1" || next.PreviewID == preview.PreviewID {
		t.Fatalf("new preview = %+v, error = %v", next, err)
	}
}

func TestConfigEditPreviewNoChange(t *testing.T) {
	const original = "key=value\n"
	client := newConfigEditTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("unexpected write for identical content: %s", r.Method)
		}
		writeConfigEditTestRows(w, configEditTestRow(configEditTestProperties, original))
	})
	req := configEditTestRequest(configEditTestProperties)
	same := original
	req.Replacement = &same
	preview, err := client.PreviewConfigEdit(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if preview.Status != "no-change" || preview.PreviewID != "" {
		t.Fatalf("unexpected no-change result: %+v", preview)
	}
}

func TestConfigEditRejectsUncertainTarget(t *testing.T) {
	for _, tc := range []struct {
		name       string
		changeRows func(map[string]any) []map[string]any
		wantError  string
	}{
		{"no ConfigMap", func(map[string]any) []map[string]any {
			return []map[string]any{}
		}, "未找到可编辑的 ConfigMap"},
		{"missing ConfigMap name", func(row map[string]any) []map[string]any {
			delete(row, "name")
			return []map[string]any{row}
		}, "缺少有效名称"},
		{"wrong namespace", func(row map[string]any) []map[string]any {
			row["namespace"] = "another"
			return []map[string]any{row}
		}, "查询条件不符"},
		{"wrong environment", func(row map[string]any) []map[string]any {
			row["env"] = "UAT"
			return []map[string]any{row}
		}, "查询条件不符"},
		{"wrong app", func(row map[string]any) []map[string]any {
			row["app_id"] = "20020002"
			return []map[string]any{row}
		}, "查询条件不符"},
		{"partial file name", func(row map[string]any) []map[string]any {
			row["encoded_data"].([]any)[0].(map[string]any)["name"] = "old.application.properties"
			older := configEditTestRow(configEditTestProperties, "older")
			older["name"] = "mp-pay-center-2.0.1"
			older["update_time"] = "2026-10-27 12:00:00"
			return []map[string]any{older, row}
		}, "未找到指定的配置文件"},
		{"duplicate file name", func(row map[string]any) []map[string]any {
			files := row["encoded_data"].([]any)
			row["encoded_data"] = append(files, files[0])
			return []map[string]any{row}
		}, "多个同名文件"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newConfigEditTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					t.Errorf("unexpected write for uncertain target: %s", r.Method)
				}
				writeConfigEditTestRows(w, tc.changeRows(configEditTestRow(configEditTestProperties, "key=old"))...)
			})
			req := configEditTestRequest(configEditTestProperties)
			replacement := "key=new"
			req.Replacement = &replacement
			if _, err := client.PreviewConfigEdit(context.Background(), req); err == nil ||
				!strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("preview error = %v, want %q", err, tc.wantError)
			}
		})
	}
}

func TestConfigEditSubmitRejectsInvalidTargetIdentity(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
		value any
	}{
		{name: "missing environment", field: "env"},
		{name: "missing namespace", field: "namespace"},
		{name: "non-string environment", field: "env", value: []string{"PRD"}},
		{name: "non-string namespace", field: "namespace", value: []string{"mp"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var invalidate atomic.Bool
			var posts atomic.Int32
			client := newConfigEditTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					posts.Add(1)
					w.WriteHeader(http.StatusMethodNotAllowed)
					return
				}
				row := configEditTestRow(configEditTestProperties, "key=old")
				if invalidate.Load() {
					if tc.value == nil {
						delete(row, tc.field)
					} else {
						row[tc.field] = tc.value
					}
				}
				writeConfigEditTestRows(w, row)
			})
			req := configEditTestRequest(configEditTestProperties)
			replacement := "key=new"
			req.Replacement = &replacement
			preview, err := client.PreviewConfigEdit(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			invalidate.Store(true)
			_, err = client.SubmitConfigEdit(context.Background(), req, preview.PreviewID)
			if err == nil || !strings.Contains(err.Error(), "缺少有效的环境或命名空间") {
				t.Fatalf("submit error = %v", err)
			}
			if posts.Load() != 0 {
				t.Errorf("invalid target issued %d posts", posts.Load())
			}
		})
	}
}

func TestConfigEditSubmitDoesNotRetry(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"timeout after post", 0, "", "提交结果不明"},
		{"redirect", http.StatusTemporaryRedirect, "server-private-value", "提交结果不明"},
		{"HTTP unauthorized", http.StatusUnauthorized, "server-private-value", "HTTP 401"},
		{"HTTP unavailable", http.StatusServiceUnavailable, "server-private-value", "提交结果不明"},
		{"non-success JSON", http.StatusOK, `{"code":401,"msg":"server-private-value"}`, "业务码 401"},
		{"no success code", http.StatusOK, `{"response":{"approve_id":42}}`, "提交结果不明"},
		{"malformed response", http.StatusOK, `{"code":20000} server-private-value`, "提交结果不明"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var posts atomic.Int32
			client := newConfigEditTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != configMapListPath {
					t.Errorf("unexpected retry or redirect to %q", r.URL.Path)
					http.NotFound(w, r)
					return
				}
				if r.Method == http.MethodGet {
					writeConfigEditTestRows(w, configEditTestRow(configEditTestProperties, "key=old"))
					return
				}
				posts.Add(1)
				if tc.status == 0 {
					_, _ = io.Copy(io.Discard, r.Body)
					select {
					case <-r.Context().Done():
					case <-time.After(2 * time.Second):
					}
					return
				}
				if tc.status == http.StatusTemporaryRedirect {
					w.Header().Set("Location", "/retry")
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			})
			req := configEditTestRequest(configEditTestProperties)
			replacement := "key=client-private-value"
			req.Replacement = &replacement
			preview, err := client.PreviewConfigEdit(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if tc.status == 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, 200*time.Millisecond)
				defer cancel()
			}
			_, err = client.SubmitConfigEdit(ctx, req, preview.PreviewID)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("submit error = %v, want %q", err, tc.want)
			}
			if posts.Load() != 1 || IsAuthenticationError(err) || strings.Contains(err.Error(), "private-value") {
				t.Errorf("unexpected failure handling: posts = %d, error = %v", posts.Load(), err)
			}
		})
	}
}

type preflightRejection struct{}

func (*preflightRejection) Error() string   { return "network scope changed" }
func (*preflightRejection) RequestNotSent() {}

type configEditTestTransport func(*http.Request) (*http.Response, error)

func (transport configEditTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestConfigEditSubmitDistinguishesTransportPreflight(t *testing.T) {
	client := newConfigEditTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("approval write reached backend after preflight rejection")
		}
		writeConfigEditTestRows(w, configEditTestRow(configEditTestProperties, "key=old"))
	})
	rejected := &preflightRejection{}
	client.httpClient.Transport = configEditTestTransport(func(request *http.Request) (*http.Response, error) {
		if request.Method == http.MethodPost && request.URL.Path == configMapListPath {
			return nil, rejected
		}
		return http.DefaultTransport.RoundTrip(request)
	})
	ctx := context.Background()
	req := configEditTestRequest(configEditTestProperties)
	replacement := "key=new"
	req.Replacement = &replacement
	preview, err := client.PreviewConfigEdit(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = client.SubmitConfigEdit(ctx, req, preview.PreviewID); !errors.Is(err, rejected) {
		t.Fatalf("SubmitConfigEdit() error = %v, want preflight rejection", err)
	}
}

func newConfigEditTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	return newConfigEditHTTPTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == appEnvNamespacePath {
			checkConfigEditReadRequest(t, r)
			writeTestJSON(w, map[string]any{"code": 20000, "response": []string{"mp"}})
			return
		}
		handler(w, r)
	})
}

func newConfigEditHTTPTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client, err := NewClient(Config{
		BeastURL: server.URL, GenbuURL: server.URL, Timeout: 3 * time.Second, HTTPClient: server.Client(),
		Token: "test-token", TokenExpiresAt: time.Now().Add(time.Hour).Unix(),
		Username: "test-user", Password: "test-password",
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func configEditTestRequest(file string) ConfigEditRequest {
	return ConfigEditRequest{
		Environment: "prod", Service: "pc", File: file,
	}
}

func configEditTestRow(file, content string) map[string]any {
	return map[string]any{
		"name": "mp-pay-center-2.0.0", "namespace": "mp", "env": "PRD", "app_id": json.Number("10010003"),
		"version": "2.0.0", "update_time": "2026-10-28 15:00:01",
		"labels": map[string]any{"owner": "pay-center"}, "record_id": json.Number("9007199254740993"),
		"encoded_data": []any{
			map[string]any{"name": file, "content": content, "custom_meta": "keep"},
			map[string]any{"name": "other.json", "content": "{\r\n  \"key\": \"preserved\"\r\n}\r\n"},
		},
	}
}

func writeConfigEditTestRows(w http.ResponseWriter, rows ...map[string]any) {
	writeTestJSON(w, map[string]any{"code": 20000, "response": rows})
}

func checkConfigEditTestHeaders(t *testing.T, r *http.Request) {
	t.Helper()
	if r.URL.Path != configMapListPath || r.Header.Get("System-Code") != systemCode ||
		r.Header.Get("App-Id") != "10010003" || r.Header.Get("X-Token") != "test-token" {
		t.Errorf("request path or headers did not match expected application: %s %s", r.Method, r.URL.Path)
	}
	want := url.Values{
		"is_all": {"true"}, "env": {"PRD"}, "namespace": {"mp"},
		"keyword": {""}, "current": {"1"}, "size": {"10"},
	}
	if r.Method == http.MethodGet && !reflect.DeepEqual(r.URL.Query(), want) {
		t.Errorf("query = %v, want %v", r.URL.Query(), want)
	}
}

func checkConfigEditReadRequest(t *testing.T, r *http.Request) {
	t.Helper()
	if r.Method != http.MethodGet || r.URL.Query().Get("env") != "PRD" ||
		r.Header.Get("App-Id") != "10010003" || r.Header.Get("System-Code") != systemCode ||
		r.Header.Get("X-Token") != "test-token" {
		t.Errorf("unexpected ConfigMap edit request: %s %s", r.Method, r.URL.String())
	}
}

func decodeConfigEditTestPost(t *testing.T, r *http.Request, want map[string]any) {
	t.Helper()
	decoder := json.NewDecoder(r.Body)
	decoder.UseNumber()
	var body map[string]any
	if err := decoder.Decode(&body); err != nil {
		t.Errorf("read POST body: %v", err)
		return
	}
	if !reflect.DeepEqual(body, want) {
		t.Errorf("POST body differs from exact selected row with one file changed: %v", fmt.Sprint(body))
	}
}
