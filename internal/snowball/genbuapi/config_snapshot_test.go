package genbuapi

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestQueryConfigSnapshotListAndRawShareSelectedSnapshot(t *testing.T) {
	const target = "application.yaml"
	const content = "\ufeff# 密码=test-password\r\nname: 乘车\r\n"
	client := newConfigEditTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		checkConfigEditTestHeaders(t, r)
		newer := configEditTestRow(target, content)
		older := configEditTestRow(target, "older")
		older["name"] = "mp-pay-center-1.9.9"
		older["version"] = "1.9.9"
		older["update_time"] = "2026-10-27 12:00:00"
		writeConfigEditTestRows(w, newer, older)
	})

	list, err := client.QueryConfigSnapshot(context.Background(), ConfigSnapshotRequest{
		Environment: "prod", Service: "pc",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantFiles := []string{target, "other.json"}
	if list.Service != "mp-pay-center" || list.Environment != "PRD" || list.Namespace != "mp" ||
		list.Name != "mp-pay-center-2.0.0" || list.UpdateTime != "2026-10-28 15:00:01" ||
		list.SnapshotID == "" || list.Files == nil || !reflect.DeepEqual(*list.Files, wantFiles) ||
		list.File != "" || list.Content != nil {
		t.Fatalf("unexpected list result: %+v", list)
	}
	if decoded, err := hex.DecodeString(list.SnapshotID); err != nil || len(decoded) != 32 {
		t.Fatalf("snapshot ID is not a SHA-256 hex value: %q", list.SnapshotID)
	}
	listJSON, err := json.Marshal(list)
	if err != nil {
		t.Fatal(err)
	}
	var listFields map[string]json.RawMessage
	if err := json.Unmarshal(listJSON, &listFields); err != nil {
		t.Fatal(err)
	}
	if _, ok := listFields["file"]; ok {
		t.Errorf("list result includes file field: %s", listJSON)
	}
	if _, ok := listFields["content"]; ok {
		t.Errorf("list result includes raw content: %s", listJSON)
	}
	if strings.Contains(string(listJSON), "test-password") {
		t.Errorf("list result exposed a file body: %s", listJSON)
	}

	raw, err := client.QueryConfigSnapshot(context.Background(), ConfigSnapshotRequest{
		Environment: "prod", Service: "pc", File: target,
	})
	if err != nil {
		t.Fatal(err)
	}
	if raw.SnapshotID != list.SnapshotID || raw.File != target || raw.Content == nil || *raw.Content != content || raw.Files != nil {
		t.Fatalf("unexpected raw result: %+v", raw)
	}
	rawJSON, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var rawFields map[string]json.RawMessage
	if err := json.Unmarshal(rawJSON, &rawFields); err != nil {
		t.Fatal(err)
	}
	if _, ok := rawFields["files"]; ok {
		t.Errorf("raw result includes file list: %s", rawJSON)
	}
	var serializedContent string
	if err := json.Unmarshal(rawFields["content"], &serializedContent); err != nil {
		t.Fatalf("decode raw JSON content: %v", err)
	}
	if serializedContent != content {
		t.Errorf("raw JSON content = %q, want exact UTF-8 body %q", serializedContent, content)
	}

	replacement := content
	preview, err := client.PreviewConfigEdit(context.Background(), ConfigEditRequest{
		Environment: "prod", Service: "pc", File: target, Replacement: &replacement,
	})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Status != "no-change" || preview.SnapshotID == "" || preview.SnapshotID != list.SnapshotID {
		t.Fatalf("preview snapshot does not match query: list=%+v preview=%+v", list, preview)
	}
}

func TestQueryConfigSnapshotListKeepsEmptyFileArray(t *testing.T) {
	client := newConfigEditTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		row := configEditTestRow("unused", "")
		row["encoded_data"] = []any{}
		writeConfigEditTestRows(w, row)
	})
	result, err := client.QueryConfigSnapshot(context.Background(), ConfigSnapshotRequest{
		Environment: "prod", Service: "pc",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Files == nil || len(*result.Files) != 0 {
		t.Fatalf("empty file list is nil or nonempty: %+v", result.Files)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if got, exists := fields["files"]; !exists || string(got) != "[]" {
		t.Errorf("files JSON field = %s (present %v), want []", got, exists)
	}
}

func TestQueryConfigSnapshotReturnsEmptyRawContentAsPresentField(t *testing.T) {
	client := newConfigEditTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		checkConfigEditTestHeaders(t, r)
		row := configEditTestRow("empty.txt", "")
		writeConfigEditTestRows(w, row)
	})
	result, err := client.QueryConfigSnapshot(context.Background(), ConfigSnapshotRequest{
		Environment: "prod", Service: "pc", File: "empty.txt",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content == nil || *result.Content != "" || result.File != "empty.txt" {
		t.Fatalf("empty file content is missing: %+v", result)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if got, exists := fields["content"]; !exists || string(got) != `""` {
		t.Errorf("content JSON field = %s (present %v), want empty string", got, exists)
	}
}

func TestQueryConfigSnapshotIDChangesWithCompleteConfigMapRow(t *testing.T) {
	var changed atomic.Bool
	client := newConfigEditTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		row := configEditTestRow("application.yaml", "name: old\n")
		if changed.Load() {
			row["encoded_data"].([]any)[1].(map[string]any)["content"] = "sibling changed"
			row["labels"].(map[string]any)["owner"] = "updated owner"
		}
		writeConfigEditTestRows(w, row)
	})
	request := ConfigSnapshotRequest{Environment: "prod", Service: "pc"}
	initial, err := client.QueryConfigSnapshot(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	changed.Store(true)
	updated, err := client.QueryConfigSnapshot(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if initial.SnapshotID == updated.SnapshotID {
		t.Errorf("snapshot ID did not change after a sibling file and metadata changed: %q", initial.SnapshotID)
	}
}

func TestQueryConfigSnapshotRejectsDuplicateExactFileNames(t *testing.T) {
	client := newConfigEditTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		row := configEditTestRow("application.yaml", "name: old\n")
		files := row["encoded_data"].([]any)
		row["encoded_data"] = append(files, map[string]any{"name": "application.yaml", "content": "duplicate"})
		writeConfigEditTestRows(w, row)
	})
	for _, request := range []ConfigSnapshotRequest{
		{Environment: "prod", Service: "pc"},
		{Environment: "prod", Service: "pc", File: "application.yaml"},
	} {
		if _, err := client.QueryConfigSnapshot(context.Background(), request); err == nil ||
			!strings.Contains(err.Error(), "多个同名文件") {
			t.Errorf("duplicate file request error = %v", err)
		}
	}
}

func TestQueryConfigSnapshotRejectsRowOutsideResolvedIdentity(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mutate    func(map[string]any)
		wantError string
	}{
		{"wrong namespace", func(row map[string]any) { row["namespace"] = "another" }, "查询条件不符"},
		{"wrong environment", func(row map[string]any) { row["env"] = "UAT" }, "查询条件不符"},
		{"wrong application", func(row map[string]any) { row["app_id"] = "20020002" }, "查询条件不符"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := newConfigEditTestClient(t, func(w http.ResponseWriter, r *http.Request) {
				row := configEditTestRow("application.yaml", "name: old\n")
				tc.mutate(row)
				writeConfigEditTestRows(w, row)
			})
			_, err := client.QueryConfigSnapshot(context.Background(), ConfigSnapshotRequest{
				Environment: "prod", Service: "pc",
			})
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("query error = %v, want %q", err, tc.wantError)
			}
		})
	}
}
