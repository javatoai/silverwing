package genbuapi

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

func TestReleaseUATPods(t *testing.T) {
	const tag = "2.15.85.beta-8"
	group := map[string]any{
		"group_id": 7, "pod_group_name": "mp-pay-center-sidecar-a", "k8s_namespace": "mp",
		"env": "UAT", "app_id": 10010003,
	}
	probe := map[string]any{"http_get": map[string]any{"path": "/health", "port": 8080}}
	for _, tc := range []struct {
		name          string
		submit        bool
		similarOnly   bool
		buildStatus   int
		uatEnabled    int
		skipUATStep   bool
		groups        []map[string]any
		settingsEnv   string
		staleSettings bool
		wantError     string
		wantSettings  int32
		wantWrites    int32
	}{
		{name: "preview", buildStatus: 1, uatEnabled: 1, groups: []map[string]any{group}, settingsEnv: "UAT", wantSettings: 1},
		{name: "submit", submit: true, buildStatus: 1, uatEnabled: 1, groups: []map[string]any{group}, settingsEnv: "UAT", wantSettings: 2, wantWrites: 1},
		{name: "settings changed", submit: true, buildStatus: 1, uatEnabled: 1, groups: []map[string]any{group}, settingsEnv: "UAT", staleSettings: true, wantSettings: 2},
		{name: "similar Tag only", similarOnly: true, wantError: "未找到服务"},
		{name: "build failed", buildStatus: 2, uatEnabled: 1, wantError: "尚未构建成功"},
		{name: "build failed before UAT step is present", buildStatus: 2, skipUATStep: true, wantError: "尚未构建成功"},
		{name: "missing UAT step", buildStatus: 1, skipUATStep: true, wantError: "无法确认 Tag"},
		{name: "UAT disabled without step name", buildStatus: 1, wantError: "无法确认 Tag"},
		{name: "hidden UAT switch", buildStatus: 1, uatEnabled: 2, wantError: "无法确认 Tag"},
		{name: "no group", buildStatus: 1, uatEnabled: 1, wantError: "未找到 Pod Group"},
		{name: "ambiguous groups", buildStatus: 1, uatEnabled: 1, groups: []map[string]any{group, {
			"group_id": 8, "pod_group_name": "mp-pay-center-sidecar-b", "k8s_namespace": "mp",
		}}, wantError: "多个 Pod Group"},
		{name: "unexpected group environment", buildStatus: 1, uatEnabled: 1, groups: []map[string]any{{
			"group_id": 7, "pod_group_name": "mp-pay-center-sidecar-a", "k8s_namespace": "mp", "env": "PRD",
		}}, wantError: "指定服务或 UAT 环境不符"},
		{name: "unexpected settings environment", buildStatus: 1, uatEnabled: 1, groups: []map[string]any{group}, settingsEnv: "PRD", wantSettings: 1, wantError: "与选定的 UAT Group 不符"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var settingsReads, writes atomic.Int32
			settings := map[string]any{
				"group_id": 7, "env": tc.settingsEnv, "k8s_namespace": "mp", "pod_num": 2, "config_version": "2.0.0",
				"auto_rollback": false, "mount_filebeat": true, "filebeat_config": "test-config", "launch_timeout": 81,
				"live_check_form": probe, "ready_check_form": probe, "startup_check_form": probe,
				"resources":  map[string]any{"cpu": "1", "memory": "256Mi"},
				"group_name": "old-name", "new_group_name": "new-name", "business_id": "old-business", "approve_type": "old-approval",
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("App-Id") != "10010003" || r.Header.Get("System-Code") != systemCode || r.Header.Get("X-Token") != "test-token" {
					t.Errorf("missing authentication or service scope for %s %s", r.Method, r.URL.Path)
				}
				switch r.URL.Path {
				case versionPath:
					var query struct {
						Conditions map[string]string `json:"conditions"`
					}
					if err := json.NewDecoder(r.Body).Decode(&query); err != nil || r.Method != http.MethodPost || query.Conditions["key_word"] != tag {
						t.Errorf("unexpected version query: %+v, error=%v", query, err)
					}
					rows := []map[string]any{{"version_id": 99, "version_name": tag + "0"}}
					if !tc.similarOnly {
						rows = append(rows, map[string]any{"version_id": 42, "version_name": tag})
					}
					writeTestJSON(w, map[string]any{"code": 20000, "response": rows})
				case baseInfoPath:
					if r.Method != http.MethodGet || r.URL.RawQuery != "version_id=42" {
						t.Errorf("unexpected build query %s %s", r.Method, r.URL)
					}
					steps := []map[string]any{{"step": buildStepNumber, "status": tc.buildStatus}}
					if !tc.skipUATStep {
						steps = append(steps, map[string]any{"step": uatStepNumber, "status": 0, "enabled": tc.uatEnabled})
					}
					writeTestJSON(w, map[string]any{"code": 20000, "response": steps})
				case uatReleaseGroupsPath:
					if r.Method != http.MethodGet || r.URL.RawQuery != "env=UAT" {
						t.Errorf("unexpected release Group query %s %s", r.Method, r.URL)
					}
					writeTestJSON(w, map[string]any{"code": 20000, "response": tc.groups})
				case uatDeployInfoPath:
					read := settingsReads.Add(1)
					if r.Method != http.MethodGet || r.URL.RawQuery != "group_id=7" {
						t.Errorf("unexpected release settings query %s %s", r.Method, r.URL)
					}
					response := settings
					if read > 1 && tc.staleSettings {
						response = maps.Clone(settings)
						response["resources"] = map[string]any{"cpu": "2", "memory": "256Mi"}
					}
					writeTestJSON(w, map[string]any{"code": 20000, "response": response})
				case uatReleasePath:
					writes.Add(1)
					if r.Method != http.MethodPost || r.URL.RawQuery != "" {
						t.Errorf("unexpected release request %s %s", r.Method, r.URL)
					}
					assertUATReleasePayload(t, r, settings)
					writeTestJSON(w, map[string]any{"code": 20000})
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL)
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			client := newTagListTestClient(t, server)
			ctx := context.Background()
			request := UATPodOperationRequest{Action: "release", Service: "  PC  ", Tag: " " + tag + " "}
			result, err := client.PreviewUATPodOperation(ctx, request)
			previewID := result.PreviewID
			if tc.submit && err == nil {
				if writes.Load() != 0 || result.Status != "preview" {
					t.Fatalf("preview = %+v, writes = %d", result, writes.Load())
				}
				result, err = client.SubmitUATPodOperation(ctx, request, previewID, nil)
			}
			if settingsReads.Load() != tc.wantSettings || writes.Load() != tc.wantWrites {
				t.Errorf("got %d settings reads and %d writes", settingsReads.Load(), writes.Load())
			}
			if tc.staleSettings {
				if !errors.Is(err, ErrUATPodPreviewStale) {
					t.Errorf("release = %+v, error = %v, want a stale preflight", result, err)
				}
				return
			}
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) || result != (UATPodOperationResult{}) {
					t.Fatalf("release = %+v, error = %v, want %q", result, err, tc.wantError)
				}
				return
			}
			status := "submitted"
			if !tc.submit {
				status = "preview"
			}
			want := UATPodOperationResult{
				Service: "mp-pay-center", AppID: "10010003", Environment: "UAT",
				GroupID: 7, Group: uatRedeployTestGroup, Namespace: "mp", PodCount: 2,
				Tag: tag, Action: "release", Status: status, PreviewID: previewID,
			}
			if err != nil || result != want || len(previewID) != 64 {
				t.Errorf("release = %+v, error = %v, want %+v", result, err, want)
			}
		})
	}
}

func assertUATReleasePayload(t *testing.T, r *http.Request, settings map[string]any) {
	t.Helper()
	var got map[string]any
	if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
		t.Errorf("decode release body: %v", err)
		return
	}
	for key, value := range settings {
		switch key {
		case "group_name", "new_group_name", "business_id":
			if _, present := got[key]; present {
				t.Errorf("unexpected release field %s", key)
			}
		case "group_id", "approve_type":
			// Checked with the other target fields.
		default:
			want, _ := json.Marshal(value)
			actual, _ := json.Marshal(got[key])
			if string(actual) != string(want) {
				t.Errorf("release field %s = %s, want %s", key, actual, want)
			}
		}
	}
	if !reflect.DeepEqual(got["group_id"], []any{float64(7)}) || got["version_id"] != float64(42) || got["target_system"] != systemCode || got["env"] != "UAT" {
		t.Errorf("unexpected release target: group=%v version=%v system=%v", got["group_id"], got["version_id"], got["target_system"])
	}
	if value, present := got["approve_type"]; !present || value != nil {
		t.Errorf("approve_type = %v, want explicit null", value)
	}
	if got["termination_grace_period_seconds"] != float64(30) || got["launch_timeout"] != float64(81) {
		t.Errorf("settings did not override defaults: termination_grace_period_seconds=%v launch_timeout=%v", got["termination_grace_period_seconds"], got["launch_timeout"])
	}
}
