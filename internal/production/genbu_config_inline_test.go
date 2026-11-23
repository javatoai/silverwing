package production

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"silverwing-go/internal/commandruntime"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/snowball/genbuapi"
)

func TestNativeGenbuConfigSnapshotAndApproval(t *testing.T) {
	const file = "application.yaml"
	const source = "\ufeffpassword: private-source\r\nname: 乘车\r\n"
	const draft = "password: private-draft\r\n"
	var body atomic.Value
	body.Store(source)
	var posts, responseStatus atomic.Int32
	var retired atomic.Bool
	responseStatus.Store(http.StatusBadGateway)
	client := newNativeGenbu(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case genbuapi.AppEnvNamespacePath:
			writeGenbuResponse(w, map[string]any{"response": []string{"unrelated", "mp"}})
		case genbuapi.ConfigMapListPath:
			if r.Method == http.MethodPost {
				posts.Add(1)
				var row struct {
					Files []struct {
						Name    string `json:"name"`
						Content string `json:"content"`
					} `json:"encoded_data"`
				}
				if err := json.NewDecoder(r.Body).Decode(&row); err != nil || len(row.Files) != 2 ||
					row.Files[0].Name != file || row.Files[0].Content != draft || row.Files[1].Content != "keep" {
					t.Error("ConfigMap approval did not preserve the complete target row")
				}
				w.WriteHeader(int(responseStatus.Load()))
				if responseStatus.Load() == http.StatusOK {
					retired.Store(true)
				}
				writeGenbuResponse(w, map[string]any{"code": 20000, "approve_id": 52, "message": "private-response"})
				return
			}
			if r.URL.Query().Get("env") != "PRD" || r.URL.Query().Get("namespace") != "mp" {
				t.Errorf("unexpected ConfigMap request: %s", r.URL)
			}
			writeGenbuResponse(w, map[string]any{"response": []map[string]any{
				{"name": "mp-pay-center-2.0.0", "env": "PRD", "namespace": "mp", "update_time": "2026-11-19 15:00:00",
					"encoded_data": []map[string]any{{"name": file, "content": body.Load()}, {"name": "other", "content": "keep"}}},
			}})
		default:
			t.Errorf("unexpected request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	ctx := context.Background()
	cfg := domain.DefaultAppConfig()
	files, err := client.ListConfigFiles(ctx, cfg, "prod", "PC")
	if err != nil || len(files.SnapshotID) != 64 || !reflect.DeepEqual(files.Files, []string{file, "other"}) {
		t.Fatalf("ListConfigFiles() = %+v, %v", files, err)
	}
	raw, err := client.ReadConfigFile(ctx, cfg, "prod", "pc", file)
	if err != nil || raw.Content != source || raw.GenbuConfigSnapshot != files.GenbuConfigSnapshot {
		t.Fatal("ReadConfigFile() did not return the exact source and metadata", err)
	}
	encoded, err := json.Marshal(raw)
	if err != nil || strings.Contains(string(encoded), "private-source") {
		t.Fatal("raw source was serialized by the app-facing DTO", err)
	}
	request := GenbuConfigEditRequest{Environment: "prod", Service: "pc", File: file, Replacement: []byte(draft), StdinReplacement: true}
	preview, err := client.PreviewConfigEdit(ctx, cfg, request)
	if err != nil || preview.Status != "preview" || preview.SnapshotID != raw.SnapshotID || posts.Load() != 0 ||
		preview.BeforeBytes == nil || *preview.BeforeBytes != len(source) || preview.AfterBytes == nil || *preview.AfterBytes != len(draft) {
		t.Fatalf("PreviewConfigEdit() = %+v, %v, posts=%d", preview, err, posts.Load())
	}
	body.Store(source + "changed")
	if _, err := client.SubmitConfigEdit(ctx, cfg, request, preview.PreviewID); !errors.Is(err, ErrGenbuConfigPreviewStale) || posts.Load() != 0 {
		t.Fatalf("stale SubmitConfigEdit() error=%v, posts=%d", err, posts.Load())
	}
	body.Store(source)
	if _, err := client.SubmitConfigEdit(ctx, cfg, request, preview.PreviewID); !errors.Is(err, ErrGenbuConfigSubmitUnknown) ||
		strings.Contains(err.Error(), "private-") || posts.Load() != 1 {
		t.Fatalf("uncertain SubmitConfigEdit() error=%v, posts=%d", err, posts.Load())
	}
	responseStatus.Store(http.StatusOK)
	client.Require = func(context.Context) error {
		if retired.Load() {
			return errors.New("shared account changed after dispatch")
		}
		return nil
	}
	result, err := client.SubmitConfigEdit(ctx, cfg, request, preview.PreviewID)
	if err != nil || result.Status != "submitted" || result.ApprovalID != 52 || posts.Load() != 2 || !retired.Load() {
		t.Fatalf("successful SubmitConfigEdit() = %+v, %v, posts=%d", result, err, posts.Load())
	}
}

func TestNativeGenbuConfigSubmitKeepsAdmissionAcrossClientAcquisition(t *testing.T) {
	var requests atomic.Int32
	client := newNativeGenbu(t, func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case genbuapi.AppEnvNamespacePath:
			writeGenbuResponse(w, map[string]any{"response": []string{"mp"}})
		case genbuapi.ConfigMapListPath:
			if r.Method != http.MethodGet {
				t.Error("approval was dispatched through a replacement client")
				writeGenbuResponse(w, map[string]any{"code": 20000, "approve_id": 1})
				return
			}
			writeGenbuResponse(w, map[string]any{"response": []map[string]any{
				{"name": "mp-pay-center-2.0.0", "env": "PRD", "namespace": "mp", "update_time": "2026-11-19 15:00:00",
					"encoded_data": []map[string]any{{"name": "application.yaml", "content": "key: old"}}},
			}})
		default:
			t.Errorf("unexpected request: %s", r.URL)
			w.WriteHeader(http.StatusNotFound)
		}
	})
	ctx := context.Background()
	cfg := domain.DefaultAppConfig()
	request := GenbuConfigEditRequest{Environment: "prod", Service: "pc", File: "application.yaml", Replacement: []byte("key: new")}
	preview, err := client.PreviewConfigEdit(ctx, cfg, request)
	if err != nil || preview.PreviewID == "" {
		t.Fatalf("PreviewConfigEdit() = %+v, %v", preview, err)
	}
	before := requests.Load()
	var retired atomic.Bool
	scopeChanged := errors.New("shared account changed during client acquisition")
	client.Require = func(context.Context) error {
		if retired.Load() {
			return scopeChanged
		}
		return nil
	}
	acquire := client.Client
	client.Client = func(ctx context.Context, cfg domain.AppConfig) (*genbuapi.Client, error) {
		retired.Store(true)
		return acquire(ctx, cfg)
	}
	_, err = client.SubmitConfigEdit(ctx, cfg, request, preview.PreviewID)
	var blocked *commandruntime.AdmissionError
	if !errors.Is(err, scopeChanged) || !errors.As(err, &blocked) || requests.Load() != before {
		t.Fatalf("SubmitConfigEdit() error = %v, requests = %d, want %d", err, requests.Load(), before)
	}
}
