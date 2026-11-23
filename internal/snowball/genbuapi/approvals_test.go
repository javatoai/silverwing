package genbuapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestListApprovalsUsesServerFiltersAndPagination(t *testing.T) {
	wantQuery := url.Values{
		"current": {"2"}, "size": {"5"}, "env": {"PRD"}, "keyword": {"mp-pay-center"},
		"time_type": {"2"}, "date_begin": {"2026-11-13"}, "date_end": {"2026-11-19"},
		"status": {"0"}, "result_type": {"2"}, "approve_type": {"7"},
	}
	client := newApprovalTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query(); !reflect.DeepEqual(got, wantQuery) {
			t.Errorf("query = %v, want %v", got, wantQuery)
		}
		writeTestJSON(w, map[string]any{"code": 20000, "total": 11, "response": []any{
			map[string]any{
				"id": 42, "app_name": "mp-pay-center", "env": "PRD", "approve_type": 7, "status": 0,
				"created_user": "tester", "created_time": "2026-11-14 12:13:14", "result": 0,
				"content": map[string]any{"password": "private-proposal"},
			},
		}})
	})
	for _, service := range []string{"PC", "mp-pay-center"} {
		got, err := client.ListApprovals(context.Background(), ApprovalListRequest{
			Environment: "prod", Service: service, Status: "pending", Scope: "reviewed", DateKind: "approved",
			StartDate: "2026-11-13", EndDate: "2026-11-19", Type: "ConfigMap 修改", Page: 2, PageSize: 5,
		})
		if err != nil {
			t.Fatal(err)
		}
		want := ApprovalListResult{Page: 2, PageSize: 5, Total: 11, Items: []ApprovalItem{{
			ID: 42, Service: "mp-pay-center", Environment: "PRD",
			Status: "待处理", Result: "未处理", Creator: "tester", CreatedAt: "2026-11-14 12:13:14",
		}}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("ListApprovals() = %+v, want %+v", got, want)
		}
	}
}

func TestApprovalListQueryDefaultDatesUseShanghaiDay(t *testing.T) {
	now := time.Date(2026, time.November, 18, 19, 0, 0, 0, time.UTC)
	query, err := approvalListQuery(ApprovalListRequest{Environment: "UAT"}, now)
	if err != nil {
		t.Fatal(err)
	}
	want := url.Values{
		"current": {"1"}, "size": {"20"}, "env": {"UAT"}, "time_type": {"1"},
		"date_begin": {"2026-11-13"}, "date_end": {"2026-11-19"},
	}
	if !reflect.DeepEqual(query, want) {
		t.Fatalf("query = %v, want %v", query, want)
	}
}

func TestListApprovalsEmptyPageKeepsServerTotal(t *testing.T) {
	client := newApprovalTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"code": 20000, "total": 12, "response": nil})
	})
	got, err := client.ListApprovals(context.Background(), ApprovalListRequest{Environment: "uat", Page: 3})
	if err != nil || got.Total != 12 || got.Page != 3 || got.Items == nil || len(got.Items) != 0 {
		t.Fatalf("empty page = %+v, error = %v", got, err)
	}
}

func TestListApprovalsRejectsUnrequestedEnvironmentFromServer(t *testing.T) {
	client := newApprovalTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"code": 20000, "total": 1, "response": []any{
			map[string]any{"id": 2, "env": "DEV", "app_name": "excluded-app"},
		}})
	})
	got, err := client.ListApprovals(context.Background(), ApprovalListRequest{Environment: "uat"})
	if err == nil || strings.Contains(err.Error(), "DEV") || len(got.Items) != 0 {
		t.Fatalf("out-of-scope environment = %+v, error = %v", got, err)
	}
}

func TestApprovalDetailMatchesOnlyExactRecordAndRedacts(t *testing.T) {
	client := newApprovalTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if q := r.URL.Query(); q.Has("keyword") || !q.Has("date_begin") || q.Get("date_begin") != "" {
			t.Errorf("unexpected detail query: %v", q)
		}
		writeTestJSON(w, map[string]any{"code": 20000, "total": 2, "response": []any{
			map[string]any{"id": 142, "env": "PRD", "app_name": "approximate", "reason": "42"},
			map[string]any{"id": 42, "env": "UAT", "app_name": "mp-pay-center", "approve_type": 7, "status": 1, "result": 1,
				"reason": "active-token", "message": "lookup-password"},
		}})
	})
	got, err := client.ApprovalDetail(context.Background(), ApprovalDetailRequest{ID: 42})
	if err != nil || got.ID != 42 || got.Service != "mp-pay-center" || got.Result != "处理成功" {
		t.Fatalf("detail = %+v, error = %v", got, err)
	}
	encoded, err := json.Marshal(got)
	if err != nil || strings.Contains(string(encoded), "active-token") || strings.Contains(string(encoded), "lookup-password") {
		t.Fatal("approval detail contains unredacted credentials or failed to encode")
	}
	if strings.Contains(string(encoded), `"type":`) {
		t.Fatal("approval detail includes the list-only type field")
	}
}

func TestApprovalDetailRejectsUnsupportedEnvironmentAndMissingID(t *testing.T) {
	client := newApprovalTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		writeTestJSON(w, map[string]any{"code": 20000, "total": 1, "response": []any{
			map[string]any{"id": 42, "env": "DEV", "app_name": "excluded-app"},
		}})
	})
	for _, test := range []struct {
		id   int64
		want string
	}{{42, "不在支持的环境范围内"}, {43, "未找到审批编号 43"}} {
		got, err := client.ApprovalDetail(context.Background(), ApprovalDetailRequest{ID: test.id})
		if err == nil || !strings.Contains(err.Error(), test.want) || got.ID != 0 || strings.Contains(err.Error(), "DEV") {
			t.Fatalf("ApprovalDetail(%d) = %+v, %v; want %q", test.id, got, err, test.want)
		}
	}
}

func TestApprovalDetailFindsOlderRecordByPageBounds(t *testing.T) {
	const total = 80
	client := newApprovalTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		page, _ := strconv.Atoi(q.Get("current"))
		size, _ := strconv.Atoi(q.Get("size"))
		if page != 1 && page != 3 && page != 4 || size != 20 {
			t.Errorf("unexpected detail page: page=%d size=%d", page, size)
		}
		rows := make([]map[string]any, 0, size)
		first := total - (page-1)*size
		for i := range size {
			rows = append(rows, map[string]any{"id": first - i, "env": "PRD"})
		}
		writeTestJSON(w, map[string]any{"code": 20000, "total": total, "response": rows})
	})
	got, err := client.ApprovalDetail(context.Background(), ApprovalDetailRequest{ID: 5})
	if err != nil || got.ID != 5 || got.Environment != "PRD" {
		t.Fatalf("detail = %+v, error = %v", got, err)
	}
}

func newApprovalTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != approvalPath || r.Header.Get("X-Token") != "active-token" || r.Header.Get("App-Id") != "" {
			t.Errorf("unexpected approval request %s %s or unexpected headers", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	client, err := NewClient(Config{
		BeastURL: server.URL, GenbuURL: server.URL, Timeout: time.Second,
		Token: "active-token", Password: "lookup-password", HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}
