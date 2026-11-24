package genbuapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestApprovalConfigReviewMatchesFilesAndRedactsBothSides(t *testing.T) {
	client, calls := newConfigApprovalFixture(t, "approved")
	detail, err := client.ApprovalDetail(context.Background(), ApprovalDetailRequest{ID: 42, Service: "pc", IncludeConfigReview: true})
	if err != nil || detail.Review == nil || !detail.Review.CanApprove {
		t.Fatalf("review unavailable: %+v, %v", detail, err)
	}
	if detail.Review.ConfigMap != "runtime-config" || detail.Review.Namespace != "team" || len(detail.Review.Revision) != 64 {
		t.Fatalf("review lost its ConfigMap identity: %+v", detail.Review)
	}
	want := []ApprovalConfigFileChange{
		{Name: "added.txt", AfterExists: true},
		{Name: "application.yaml", Before: "feature: false\npassword: <redacted>\n", After: "feature: true\npassword: <redacted>\n", BeforeExists: true, AfterExists: true},
		{Name: "removed.txt", Before: "removed", BeforeExists: true},
		{Name: "token.txt", Before: "<redacted>", After: "<redacted>", BeforeExists: true, AfterExists: true},
	}
	if !reflect.DeepEqual(detail.Review.Files, want) || detail.Details["审批备注"] != "更新开关" || len(detail.Details) != 1 {
		t.Fatalf("unexpected changes: %+v / %+v", detail.Review.Files, detail.Details)
	}
	if calls.puts.Load() != 0 {
		t.Fatal("reading the approval mutated it")
	}
}

func TestApproveUATConfigChangeWritesOnceAndReconcilesReceipt(t *testing.T) {
	tests := []struct {
		mode       string
		wantWrites int32
		wantStatus string
		wantError  error
	}{
		{mode: "approved", wantWrites: 1, wantStatus: "已通过"},
		{mode: "lost receipt", wantWrites: 1, wantStatus: "已通过"},
		{mode: "accepted but pending", wantWrites: 1, wantStatus: "待处理", wantError: ErrApprovalOutcomeUnknown},
		{mode: "unknown", wantWrites: 1, wantStatus: "待处理", wantError: ErrApprovalOutcomeUnknown},
		{mode: "write denied", wantWrites: 1, wantError: ErrApprovalRejected},
		{mode: "changed baseline", wantError: ErrApprovalReviewStale},
		{mode: "changed proposal", wantError: ErrApprovalReviewStale},
		{mode: "permission removed", wantError: ErrApprovalRejected},
	}
	for _, test := range tests {
		t.Run(test.mode, func(t *testing.T) {
			client, calls := newConfigApprovalFixture(t, test.mode)
			ctx := context.Background()
			detail, err := client.ApprovalDetail(ctx, ApprovalDetailRequest{ID: 42, Service: "pc", IncludeConfigReview: true})
			if err != nil || detail.Review == nil || !detail.Review.CanApprove {
				t.Fatalf("initial review unavailable: %+v, %v", detail, err)
			}
			calls.rechecking.Store(true)
			result, err := client.ApproveUATConfigChange(ctx, ApprovalConfigApproveRequest{ID: 42, Service: "pc", Revision: detail.Review.Revision})
			if !errors.Is(err, test.wantError) || result.Status != test.wantStatus || calls.puts.Load() != test.wantWrites {
				t.Fatalf("approval writes=%d, result=%+v, err=%v", calls.puts.Load(), result, err)
			}
			if test.wantStatus != "" && (result.Review == nil || result.Review.CanApprove || len(result.Review.Files) != len(detail.Review.Files)) {
				t.Fatalf("readback lost reviewed changes or still permits another submit: %+v", result)
			}
		})
	}
}

func TestApprovalConfigReviewRefusesMissingOrMismatchedTarget(t *testing.T) {
	for _, mode := range []string{"prod", "wrong service", "wrong type", "wrong app", "wrong baseline", "missing baseline", "duplicate files"} {
		t.Run(mode, func(t *testing.T) {
			client, calls := newConfigApprovalFixture(t, mode)
			detail, err := client.ApprovalDetail(context.Background(), ApprovalDetailRequest{ID: 42, Service: "pc", IncludeConfigReview: true})
			if detail.Review != nil || (err == nil && detail.ReviewError == "") || calls.puts.Load() != 0 {
				t.Fatalf("unexpected usable review: %+v, %v", detail, err)
			}
		})
	}
}

type configApprovalFixtureCalls struct {
	puts       atomic.Int32
	rechecking atomic.Bool
	approved   atomic.Bool
}

func newConfigApprovalFixture(t *testing.T, mode string) (*Client, *configApprovalFixtureCalls) {
	t.Helper()
	calls := &configApprovalFixtureCalls{}
	file := func(name, content string) map[string]any { return map[string]any{"name": name, "content": content} }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const token = "fixture-review-token"
		const appID = "10010003"
		if r.Header.Get("X-Token") != token || r.Header.Get("System-Code") != systemCode {
			t.Error("review request lost Genbu headers")
		}
		switch r.Method + " " + r.URL.Path {
		case http.MethodGet + " " + approvalPath:
			query := r.URL.Query()
			if query.Get("env") != "UAT" || query.Get("approve_type") != "7" {
				t.Errorf("approval detail is not scoped to UAT ConfigMap changes: %v", query)
			}
			files := []any{file("added.txt", ""), file("unchanged.txt", "same"), file("token.txt", "new-token"), file("application.yaml", "feature: true\npassword: new-password\n")}
			content := map[string]any{"name": "runtime-config", "env": "UAT", "namespace": "team", "app_id": appID, "encoded_data": files}
			row := map[string]any{
				"id": 42, "env": "UAT", "app_name": "mp-pay-center", "approve_type": 7, "status": 0, "reason": "更新开关", "content": content,
			}
			switch mode {
			case "prod":
				row["env"] = "PRD"
			case "wrong service":
				row["app_name"] = "another-service"
			case "wrong type":
				row["approve_type"] = 8
			case "wrong app":
				content["app_id"] = "another-app"
			case "changed proposal":
				if calls.rechecking.Load() {
					files[3] = file("application.yaml", "feature: changed")
				}
			case "duplicate files":
				content["encoded_data"] = append(files, files[0])
			}
			if calls.approved.Load() {
				row["status"], row["result"], row["approve_user"] = 1, 3, "approver"
			}
			writeTestJSON(w, map[string]any{"code": 20000, "total": 1, "response": []any{row}})
		case http.MethodGet + " " + approvalConfigDetailPath:
			want := url.Values{"name": {"runtime-config"}, "env": {"UAT"}, "namespace": {"team"}, "app_id": {appID}}
			if !reflect.DeepEqual(r.URL.Query(), want) || r.Header.Get("App-Id") != appID {
				t.Errorf("baseline request lost target identity: %v", r.URL.Query())
			}
			if mode == "missing baseline" {
				http.NotFound(w, r)
				return
			}
			files := []any{file("removed.txt", "removed"), file("application.yaml", "feature: false\npassword: old-password\n"), file("token.txt", "old-token"), file("unchanged.txt", "same")}
			if mode == "changed baseline" && calls.rechecking.Load() {
				files[1] = file("application.yaml", "feature: false\npassword: changed-password\n")
			}
			current := map[string]any{"name": "runtime-config", "env": "UAT", "namespace": "team", "app_id": appID, "encoded_data": files}
			if mode == "wrong baseline" {
				current["namespace"] = "another-team"
			}
			writeTestJSON(w, map[string]any{"code": 20000, "response": current})
		case http.MethodGet + " " + approvalPermissionsPath:
			env := "UAT"
			if mode == "permission removed" && calls.rechecking.Load() {
				env = "PRD"
			}
			writeTestJSON(w, map[string]any{"code": 20000, "response": []any{map[string]any{"operation_key": "edit_config_map", "env": env}}})
		case http.MethodPut + " " + approvalPath:
			calls.puts.Add(1)
			var body map[string]any
			want := map[string]any{"id": float64(42), "status": float64(1), "reason": "更新开关"}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || !reflect.DeepEqual(body, want) || r.Header.Get("App-Id") != appID {
				t.Errorf("wrong approval mutation: %v, %v", body, err)
			}
			if mode == "approved" || mode == "lost receipt" {
				calls.approved.Store(true)
			}
			switch mode {
			case "lost receipt", "unknown":
				w.WriteHeader(http.StatusGatewayTimeout)
			case "write denied":
				w.WriteHeader(http.StatusUnauthorized)
			default:
				writeTestJSON(w, map[string]any{"code": 20000})
			}
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(Config{
		BeastURL: server.URL, GenbuURL: server.URL, Token: "fixture-review-token",
		Timeout: 2 * time.Second, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return client, calls
}

func TestApprovalConfigFileRedactsMultilineCredentials(t *testing.T) {
	for _, value := range []string{
		"password=private-part\\\n  continued-part\n",
		"env:\n  - name: DB_PASSWORD\n    value: nested-secret\n",
		"webhook=https://example.test/path?region=test&access%5ftoken=query-secret\n",
	} {
		got := redactApprovalConfigFile("application.properties", value, nil)
		for _, secret := range []string{"private-part", "continued-part", "nested-secret", "query-secret"} {
			if strings.Contains(got, secret) {
				t.Errorf("redacted config retains %q: %q", secret, got)
			}
		}
	}
}
