package application

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

type genbuConfigSourceFake struct {
	genbuEditRouteFake
	files  func(env, service string) (production.GenbuConfigFiles, error)
	source func(env, service, file string) (production.GenbuConfigFile, error)
}

func (f *genbuConfigSourceFake) ListConfigFiles(_ context.Context, _ domain.AppConfig, env, service string) (production.GenbuConfigFiles, error) {
	return f.files(env, service)
}

func (f *genbuConfigSourceFake) ReadConfigFile(_ context.Context, _ domain.AppConfig, env, service, file string) (production.GenbuConfigFile, error) {
	return f.source(env, service, file)
}

func genbuTestConfigSnapshot(env string) production.GenbuConfigSnapshot {
	return production.GenbuConfigSnapshot{
		Service: "canonical", Environment: env, Namespace: "team", Name: "app-cm",
		UpdateTime: "2026-11-19 09:00:00", SnapshotID: strings.Repeat("c", 64),
	}
}

func genbuTestConfigSourceAction(base Action, file string) Action {
	base.Kind, base.Payload = "task.services.config-source", maps.Clone(base.Payload)
	base.Payload["file"] = file
	return base
}

func genbuTestInlineConfigActions(base Action, source TaskServiceConfigSourceResult, draft string) (Action, Action) {
	preview, submit := genbuConfigTestActions(base, source.File, "content", draft)
	preview.Payload["snapshotId"], preview.Payload["sourceToken"] = source.SnapshotID, source.SourceToken
	return preview, submit
}

func TestTaskServiceInlineConfigReadsAndSubmitsExactDraft(t *testing.T) {
	const file = "服务.yaml"
	const original = "\ufeff# 私有配置示例\r\nold:  value  \r\n"
	for _, draft := range []string{"\ufeff# 完整修改\r\nnew:  value  \r\n\r\n", ""} {
		name := "exact UTF-8"
		if draft == "" {
			name = "empty document"
		}
		t.Run(name, func(t *testing.T) {
			app, base := taskServicesFixture(t)
			// Initialize both persistent lock files before the snapshot so it
			// detects other writes through preview and submission.
			path := base.Payload["taskServicesContext"].(TaskServicesContext).TaskPath
			if err := workspace.NewOperationLocks(app.Paths.Locks).WithTask(path, func() error { return nil }); err != nil {
				t.Fatal(err)
			}
			unlock, err := storage.AcquireLock(filepath.Join(app.Paths.Locks, "genbu-config-submissions.lock"))
			if err != nil {
				t.Fatal(err)
			}
			unlock()
			before := taskFilesBytes(t, app.Paths.Home)
			ctx := context.Background()
			client := &genbuConfigSourceFake{
				genbuEditRouteFake: genbuEditRouteFake{identity: "account-cli"},
				files: func(env, service string) (production.GenbuConfigFiles, error) {
					if env != "uat" || service != "alias" {
						t.Fatal("file list lost task scope")
					}
					return production.GenbuConfigFiles{GenbuConfigSnapshot: genbuTestConfigSnapshot(env), Files: []string{file}}, nil
				},
				source: func(env, service, requested string) (production.GenbuConfigFile, error) {
					if env != "uat" || service != "alias" || requested != file {
						t.Fatal("source read lost task scope or exact file name")
					}
					return production.GenbuConfigFile{GenbuConfigSnapshot: genbuTestConfigSnapshot(env), File: file, Content: original}, nil
				},
			}
			files := base
			files.Kind = "task.services.config-files"
			list, err := app.taskServiceGenbuAction(ctx, files, client)
			if err != nil || !reflect.DeepEqual(list.Data.(production.GenbuConfigFiles).Files, []string{file}) {
				t.Fatal("file list missing selected file", err)
			}
			read, err := app.taskServiceGenbuAction(ctx, genbuTestConfigSourceAction(base, file), client)
			if err != nil {
				t.Fatal(err)
			}
			source := read.Data.(TaskServiceConfigSourceResult)
			if source.Content != original || !validGenbuConfigDigest(source.SourceToken) || source.SourceToken == source.SnapshotID {
				t.Fatal("source read lost its exact body or local grant")
			}
			encoded, err := json.Marshal(read)
			if err != nil || strings.Contains(string(encoded), "私有配置示例") || read.Text != "" || read.Message != "" {
				t.Fatal("source body leaked into the generic result")
			}
			preview, submit := genbuTestInlineConfigActions(base, source, draft)
			var previewed production.GenbuConfigEditRequest
			var remotePreview production.GenbuConfigEditResult
			client.preview = func(req production.GenbuConfigEditRequest) (production.GenbuConfigEditResult, error) {
				if !req.StdinReplacement || req.Replacement == nil || string(req.Replacement) != draft || req.File != file || req.Environment != "uat" || req.Service != "alias" {
					t.Fatal("preview changed the draft or target before sending through stdin")
				}
				previewed, remotePreview = req, genbuConfigTestPreview(req)
				remotePreview.SnapshotID = source.SnapshotID
				return remotePreview, nil
			}
			var submissions int
			client.submit = func(req production.GenbuConfigEditRequest, expect string) (production.GenbuConfigEditResult, error) {
				submissions++
				if !reflect.DeepEqual(req, previewed) || expect != remotePreview.PreviewID {
					t.Fatal("submit did not reuse the exact preview draft")
				}
				result := remotePreview
				result.Status, result.ApprovalID = "submitted", 710
				return result, nil
			}
			result, err := app.taskServiceGenbuAction(ctx, preview, client)
			if err != nil || submissions != 0 {
				t.Fatal("preview failed or submitted changes", err)
			}
			token := result.Data.(TaskServiceConfigPreviewResult).PreviewToken
			if token == "" || token == source.SourceToken {
				t.Fatal("inline preview omitted its independent submit token")
			}
			submit.Payload["previewToken"] = token
			if _, err := app.taskServiceGenbuAction(ctx, submit, client); err != nil {
				t.Fatal(err)
			}
			if _, err := app.taskServiceGenbuAction(ctx, submit, client); !errors.Is(err, errGenbuConfigPreviewGone) || submissions != 1 {
				t.Fatal("inline preview submitted more than once", err)
			}
			if !reflect.DeepEqual(before, taskFilesBytes(t, app.Paths.Home)) {
				t.Fatal("config body was persisted into the task app directory")
			}
		})
	}
}

func TestTaskServiceInlineConfigRejectsStaleSourceBeforeToken(t *testing.T) {
	for _, change := range []string{"snapshot", "no-change snapshot", "no-change same snapshot", "source request digest", "source request file", "source request environment", "missing grant", "expired grant", "same hash different account", "account changes during preview"} {
		t.Run(change, func(t *testing.T) {
			app, base := taskServicesFixture(t)
			ctx := context.Background()
			client := &genbuConfigSourceFake{
				genbuEditRouteFake: genbuEditRouteFake{identity: "account-cli"},
				source: func(env, _, file string) (production.GenbuConfigFile, error) {
					return production.GenbuConfigFile{GenbuConfigSnapshot: genbuTestConfigSnapshot(env), File: file}, nil
				},
			}
			read := genbuTestConfigSourceAction(base, "application.yaml")
			result, err := app.taskServiceGenbuAction(ctx, read, client)
			if err != nil {
				t.Fatal(err)
			}
			source := result.Data.(TaskServiceConfigSourceResult)
			preview, _ := genbuTestInlineConfigActions(base, source, "new: value")
			wantPreviewCalls := 0
			switch change {
			case "source request digest":
				preview.Payload["snapshotId"] = strings.Repeat("d", 64)
			case "source request file":
				preview.Payload["file"] = "another.yaml"
			case "source request environment":
				preview.Payload["environment"] = "prod"
			case "missing grant":
				delete(preview.Payload, "sourceToken")
			case "expired grant":
				app.genbuConfigSources.mu.Lock()
				entry := app.genbuConfigSources.entries[source.SourceToken]
				entry.expires = time.Now().Add(-time.Second)
				app.genbuConfigSources.entries[source.SourceToken] = entry
				app.genbuConfigSources.mu.Unlock()
			case "same hash different account":
				client.identity = "another-account-cli"
				// Reading the same snapshot under this account cannot rebind the prior token.
				if _, err := app.taskServiceGenbuAction(ctx, read, client); err != nil {
					t.Fatal(err)
				}
			default:
				wantPreviewCalls = 1
			}
			var previewCalls int
			client.preview = func(req production.GenbuConfigEditRequest) (production.GenbuConfigEditResult, error) {
				previewCalls++
				result := genbuConfigTestPreview(req)
				result.SnapshotID = source.SnapshotID
				if change == "snapshot" || change == "no-change snapshot" {
					result.SnapshotID = strings.Repeat("d", 64)
				}
				if strings.HasPrefix(change, "no-change") {
					result.Status, result.PreviewID = "no-change", ""
				}
				if change == "account changes during preview" {
					client.identity = "another-account-cli"
				}
				return result, nil
			}
			result, err = app.taskServiceGenbuAction(ctx, preview, client)
			wantError := production.ErrGenbuConfigPreviewStale
			if change == "source request environment" {
				wantError = errProdServiceRuntimeReadOnly
			}
			if change == "no-change same snapshot" {
				if err != nil || result.Data.(TaskServiceConfigPreviewResult).Status != "no-change" || result.Data.(TaskServiceConfigPreviewResult).PreviewToken != "" {
					t.Fatal("no-change preview should succeed without a token", err)
				}
			} else if !errors.Is(err, wantError) || result.Data != nil {
				t.Fatal("invalid source was not rejected with the expected error", err)
			}
			if previewCalls != wantPreviewCalls || len(app.genbuConfigPreviews.entries) != 0 {
				t.Fatal("invalid source reached Genbu or created a submit token")
			}
		})
	}
}

func TestTaskServiceConfigSourceWithholdsRawBodyOnScopeChanges(t *testing.T) {
	for _, change := range []string{"already archived", "archive during read", "disable Genbu", "change account identity", "cancel during read", "retarget payload"} {
		t.Run(change, func(t *testing.T) {
			app, base := taskServicesFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			read := genbuTestConfigSourceAction(base, "application.yaml")
			archive := func() {
				path := base.Payload["taskServicesContext"].(TaskServicesContext).TaskPath
				manifest, err := app.Manifests.Load(path)
				if err != nil {
					t.Fatal(err)
				}
				manifest.LifecycleStatus = domain.TaskArchived
				if err := app.Manifests.Save(path, manifest); err != nil {
					t.Fatal(err)
				}
			}
			if change == "already archived" {
				archive()
				current := base.Payload["taskServicesContext"].(TaskServicesContext)
				current.Archived = true
				read.Payload["taskServicesContext"] = current
			}
			if change == "retarget payload" {
				read.Payload["namespace"] = "elsewhere"
			}
			const raw = "private fixture for a blocked result"
			client := &genbuConfigSourceFake{genbuEditRouteFake: genbuEditRouteFake{identity: "account-cli"}}
			client.source = func(env, _, file string) (production.GenbuConfigFile, error) {
				switch change {
				case "already archived", "retarget payload":
					t.Fatal("invalid scope reached raw Genbu read")
				case "archive during read":
					archive()
				case "disable Genbu":
					cfg, err := app.Config.LoadForPreview(ctx)
					if err != nil {
						t.Fatal(err)
					}
					disabled := false
					cfg.OptionalCLIs.Genbu = &disabled
					if err := app.Config.Save(cfg); err != nil {
						t.Fatal(err)
					}
				case "change account identity":
					client.identity = "changed"
				case "cancel during read":
					cancel()
				}
				return production.GenbuConfigFile{GenbuConfigSnapshot: genbuTestConfigSnapshot(env), File: file, Content: raw}, nil
			}
			result, err := app.taskServiceGenbuAction(ctx, read, client)
			if err == nil || strings.Contains(err.Error(), raw) || result.Data != nil || len(app.genbuConfigSources.entries) != 0 {
				t.Fatal("raw body or grant escaped a failed source read")
			}
		})
	}
}
