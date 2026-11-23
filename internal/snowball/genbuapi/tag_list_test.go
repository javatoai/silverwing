package genbuapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestListTagsSendsFiltersAndUsesServerPage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != versionPath || request.Method != http.MethodPost ||
			request.Header.Get("App-Id") != "10010003" || request.Header.Get("X-Token") != "test-token" ||
			request.Header.Get("System-Code") != systemCode {
			t.Errorf("unexpected tag query %s %s with App-Id %q", request.Method, request.URL.Path, request.Header.Get("App-Id"))
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		var body struct {
			Current    int `json:"current"`
			Size       int `json:"size"`
			Conditions struct {
				Keyword     string   `json:"key_word"`
				CreatedDate []string `json:"created_date"`
			} `json:"conditions"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode version query: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		wantDateRange := []string{"2026-11-04 00:00:00", "2026-11-05 00:00:00"}
		if body.Current != 2 || body.Size != 2 || body.Conditions.Keyword != "beta" || !reflect.DeepEqual(body.Conditions.CreatedDate, wantDateRange) {
			t.Errorf("unexpected tag query payload: %+v", body)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		writeTestJSON(writer, map[string]any{"code": 20000, "total": 27, "response": []any{
			map[string]any{"version_id": 12, "version_name": "2.0.4.beta-2", "created_user": "gitlab", "created_time": "2026-11-04 16:40:00", "uat": 1, "prd": 0, "dev": 1},
			map[string]any{"version_id": 11, "version_name": "2.0.4.beta-1", "created_user": "builder", "created_time": "2026-11-04 16:30:00", "uat": 2, "prd": 1},
		}})
	}))
	defer server.Close()
	client := newTagListTestClient(t, server)
	want := TagListResult{Service: "mp-pay-center", Page: 2, PageSize: 2, Total: 27, Items: []TagListItem{
		{Tag: "2.0.4.beta-2", CreatedAt: "2026-11-04 16:40:00"},
		{Tag: "2.0.4.beta-1", CreatedAt: "2026-11-04 16:30:00"},
	}}
	for _, service := range []string{"pc", "PC", "mp-pay-center"} {
		got, err := client.ListTags(context.Background(), TagListRequest{
			Service: service, Keyword: "beta", StartDate: "2026-11-04", EndDate: "2026-11-05", Page: 2, PageSize: 2,
		})
		if err != nil {
			t.Fatalf("ListTags(%q): %v", service, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("ListTags(%q) = %#v, want %#v", service, got, want)
		}
	}
}

func TestListTagsResolvesUnknownServiceAndReturnsEmptyItems(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case applicationsPath:
			writeTestJSON(writer, map[string]any{"code": 20000, "response": []any{
				map[string]string{"app_id": "90010001", "app_name": "fp-unlisted"},
			}})
		case versionPath:
			var body struct {
				Current    int            `json:"current"`
				Size       int            `json:"size"`
				Conditions map[string]any `json:"conditions"`
			}
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode version query: %v", err)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			if request.Header.Get("App-Id") != "90010001" || body.Current != 1 || body.Size != 10 || len(body.Conditions) != 0 {
				t.Errorf("unexpected unfiltered tag query: App-Id=%q body=%+v", request.Header.Get("App-Id"), body)
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			writeTestJSON(writer, map[string]any{"code": 20000, "total": 0, "response": []any{}})
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	client := newTagListTestClient(t, server)
	got, err := client.ListTags(context.Background(), TagListRequest{Service: "fp-unlisted"})
	if err != nil {
		t.Fatal(err)
	}
	want := TagListResult{Service: "fp-unlisted", Page: 1, PageSize: 10, Items: []TagListItem{}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ListTags() = %#v, want %#v", got, want)
	}
}

func newTagListTestClient(t *testing.T, server *httptest.Server) *Client {
	t.Helper()
	client, err := NewClient(Config{BeastURL: server.URL, GenbuURL: server.URL, Token: "test-token", Timeout: time.Second, HTTPClient: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestNormalizeTagListRequest(t *testing.T) {
	got, err := normalizeTagListRequest(TagListRequest{Service: " pc ", Keyword: " beta "})
	if err != nil {
		t.Fatal(err)
	}
	want := TagListRequest{Service: "pc", Keyword: "beta", Page: 1, PageSize: 10}
	if got != want {
		t.Fatalf("normalized request = %#v, want %#v", got, want)
	}

	tests := []struct {
		name    string
		request TagListRequest
		error   string
	}{
		{"missing service", TagListRequest{}, "服务名"},
		{"negative page", TagListRequest{Service: "pc", Page: -1}, "页码"},
		{"page too large", TagListRequest{Service: "pc", PageSize: 101}, "每页条数"},
		{"incomplete range", TagListRequest{Service: "pc", StartDate: "2026-11-19"}, "同时指定"},
		{"invalid date", TagListRequest{Service: "pc", StartDate: "2026-02-30", EndDate: "2026-03-01"}, "YYYY-MM-DD"},
		{"reversed range", TagListRequest{Service: "pc", StartDate: "2026-11-19", EndDate: "2026-11-18"}, "晚于"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := normalizeTagListRequest(test.request)
			if err == nil || !strings.Contains(err.Error(), test.error) {
				t.Fatalf("normalizeTagListRequest() error = %v, want %q", err, test.error)
			}
		})
	}
}
