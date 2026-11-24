package application

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"
)

type genbuEditRouteFake struct {
	taskServiceGenbuClient
	identity string
	preview  func(production.GenbuConfigEditRequest) (production.GenbuConfigEditResult, error)
	submit   func(production.GenbuConfigEditRequest, string) (production.GenbuConfigEditResult, error)
}

func (f *genbuEditRouteFake) AccountIdentity(context.Context, domain.AppConfig) (string, error) {
	return f.identity, nil
}

func (f *genbuEditRouteFake) PreviewConfigEdit(_ context.Context, _ domain.AppConfig, req production.GenbuConfigEditRequest) (production.GenbuConfigEditResult, error) {
	return f.preview(req)
}

func (f *genbuEditRouteFake) SubmitConfigEdit(_ context.Context, _ domain.AppConfig, req production.GenbuConfigEditRequest, expect string) (production.GenbuConfigEditResult, error) {
	return f.submit(req, expect)
}

func genbuConfigTestPreview(req production.GenbuConfigEditRequest) production.GenbuConfigEditResult {
	return production.GenbuConfigEditResult{
		Service: "canonical", Environment: req.Environment, Namespace: "team", Name: "app-cm", File: req.File,
		UpdateTime: "2026-11-19 09:00:00", Status: "preview", PreviewID: strings.Repeat("a", 64), ChangeID: strings.Repeat("b", 64),
	}
}

func genbuConfigTestActions(base Action, file, input string, content any) (Action, Action) {
	preview, submit := base, base
	preview.Kind, submit.Kind = "task.services.config-preview", "task.services.config-submit"
	preview.Payload, submit.Payload = maps.Clone(base.Payload), maps.Clone(base.Payload)
	preview.Payload["file"], preview.Payload[input] = file, content
	submit.Payload["confirmed"] = true
	return preview, submit
}

func TestTaskServiceConfigEditUsesExactPreviewOnce(t *testing.T) {
	const secret = "  测试用例 value\r\nend  "
	for _, input := range []struct {
		name, file, field string
		value             any
		replacement       []byte
		edits             []production.GenbuPropertyEdit
	}{
		{name: "exact replacement", file: "app.yml", field: "content", value: secret, replacement: []byte(secret)},
		{name: "empty replacement", file: "app.yml", field: "content", value: "", replacement: []byte{}},
		{name: "properties", file: "app.properties", field: "operations", value: []map[string]any{{"operation": "set", "key": "example", "value": secret}}, edits: []production.GenbuPropertyEdit{{Operation: "set", Key: "example", Value: secret}}},
	} {
		t.Run(input.name, func(t *testing.T) {
			app, base := taskServicesFixture(t)
			preview, submit := genbuConfigTestActions(base, input.file, input.field, input.value)
			before := taskFilesBytes(t, app.Paths.Home)
			var remotePreview production.GenbuConfigEditResult
			var previewed production.GenbuConfigEditRequest
			var submitCalls int
			client := &genbuEditRouteFake{identity: "account-cli", preview: func(req production.GenbuConfigEditRequest) (production.GenbuConfigEditResult, error) {
				if req.Environment != "uat" || req.Service != "alias" || req.File != input.file || req.StdinReplacement || !reflect.DeepEqual(req.Replacement, input.replacement) || !reflect.DeepEqual(req.Edits, input.edits) {
					t.Fatalf("preview changed edit or destination: %+v", req)
				}
				previewed, remotePreview = req, genbuConfigTestPreview(req)
				return remotePreview, nil
			}, submit: func(req production.GenbuConfigEditRequest, expect string) (production.GenbuConfigEditResult, error) {
				submitCalls++
				if !reflect.DeepEqual(req, previewed) || expect != remotePreview.PreviewID {
					t.Fatalf("submit changed previewed edit/digest: %+v %s", req, expect)
				}
				remoteResult := remotePreview
				remoteResult.Status, remoteResult.ApprovalID = "submitted", 710
				return remoteResult, nil
			}}
			ctx := context.Background()
			result, err := app.taskServiceGenbuAction(ctx, preview, client)
			if err != nil || !reflect.DeepEqual(before, taskFilesBytes(t, app.Paths.Home)) || submitCalls != 0 {
				t.Fatalf("preview failed or persisted local data: %v", err)
			}
			previewResult := result.Data.(TaskServiceConfigPreviewResult)
			if previewResult.PreviewToken == "" || previewResult.PreviewToken == remotePreview.PreviewID || previewResult.ExpiresAt == "" {
				t.Fatal("missing independent local preview token")
			}
			submit.Payload["previewToken"] = previewResult.PreviewToken
			wrong := submit
			wrong.Payload = maps.Clone(submit.Payload)
			wrong.Payload["environment"] = "prod"
			if _, err = app.taskServiceGenbuAction(ctx, wrong, client); !errors.Is(err, errProdServiceRuntimeReadOnly) || submitCalls != 0 {
				t.Fatalf("preview crossed environment: %v", err)
			}
			wrong.Payload["environment"], wrong.Payload["content"] = "uat", "changed"
			if _, err = app.taskServiceGenbuAction(ctx, wrong, client); err == nil || submitCalls != 0 {
				t.Fatal("submit accepted a replacement body alongside its token")
			}
			result, err = app.taskServiceGenbuAction(ctx, submit, client)
			if err != nil || result.Data.(production.GenbuConfigEditResult).ApprovalID != 710 || submitCalls != 1 {
				t.Fatalf("unexpected submission result or count: %v %v", result, err)
			}
			if _, err = app.taskServiceGenbuAction(ctx, submit, client); !errors.Is(err, errGenbuConfigPreviewGone) || submitCalls != 1 {
				t.Fatal("used preview token became actionable again")
			}
		})
	}
}

func TestTaskServiceConfigEditRejectsExpiredDriftedArchivedOrDiscardedPreview(t *testing.T) {
	for _, change := range []string{"expired", "account or executable", "archived", "discarded"} {
		t.Run(change, func(t *testing.T) {
			app, base := taskServicesFixture(t)
			preview, submit := genbuConfigTestActions(base, "app.yml", "content", "fixture")
			client := &genbuEditRouteFake{identity: "account-cli", preview: func(req production.GenbuConfigEditRequest) (production.GenbuConfigEditResult, error) {
				return genbuConfigTestPreview(req), nil
			}, submit: func(production.GenbuConfigEditRequest, string) (production.GenbuConfigEditResult, error) {
				t.Fatal("invalidated preview reached Genbu submit")
				return production.GenbuConfigEditResult{}, nil
			}}
			ctx := context.Background()
			result, err := app.taskServiceGenbuAction(ctx, preview, client)
			if err != nil {
				t.Fatal(err)
			}
			token := result.Data.(TaskServiceConfigPreviewResult).PreviewToken
			submit.Payload["previewToken"] = token
			switch change {
			case "expired":
				app.genbuConfigPreviews.mu.Lock()
				record := app.genbuConfigPreviews.entries[token]
				record.expires = time.Now().Add(-time.Second)
				app.genbuConfigPreviews.entries[token] = record
				app.genbuConfigPreviews.mu.Unlock()
			case "account or executable":
				client.identity = "different"
			case "archived":
				path := base.Payload["taskServicesContext"].(TaskServicesContext).TaskPath
				manifest, err := app.Manifests.Load(path)
				if err != nil {
					t.Fatal(err)
				}
				manifest.LifecycleStatus = domain.TaskArchived
				if err := app.Manifests.Save(path, manifest); err != nil {
					t.Fatal(err)
				}
			case "discarded":
				discard := submit
				discard.Kind, discard.Payload = "task.services.config-preview-discard", maps.Clone(submit.Payload)
				delete(discard.Payload, "confirmed")
				if _, err := app.taskServiceGenbuAction(ctx, discard, client); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := app.taskServiceGenbuAction(ctx, submit, client); err == nil {
				t.Fatal("invalidated preview succeeded")
			}
			if len(app.genbuConfigPreviews.entries) != 0 {
				t.Fatal("attempt left edit in preview store")
			}
		})
	}
}

func TestTaskServiceConfigEditNoChangeAndUnknownSubmit(t *testing.T) {
	app, base := taskServicesFixture(t)
	preview, submit := genbuConfigTestActions(base, "app.yml", "content", "fixture")
	ctx := context.Background()
	var noChange = true
	var submits int
	var changedSnapshot bool
	submitErr := production.ErrGenbuConfigSubmitUnknown
	changeID := func(req production.GenbuConfigEditRequest) string {
		if string(req.Replacement) == "fixture" {
			return strings.Repeat("b", 64)
		}
		return strings.Repeat("d", 64)
	}
	client := &genbuEditRouteFake{identity: "account-cli", preview: func(req production.GenbuConfigEditRequest) (production.GenbuConfigEditResult, error) {
		result := genbuConfigTestPreview(req)
		if noChange {
			result.Status, result.PreviewID = "no-change", ""
		}
		if changedSnapshot {
			result.PreviewID = strings.Repeat("c", 64)
		}
		result.ChangeID = changeID(req)
		return result, nil
	}, submit: func(req production.GenbuConfigEditRequest, _ string) (production.GenbuConfigEditResult, error) {
		submits++
		path := app.genbuConfigSubmissionPath(changeID(req))
		var recorded *GenbuConfigPendingError
		if err := checkGenbuConfigSubmission(path); !errors.As(err, &recorded) {
			t.Fatalf("submission was not recorded before contacting Genbu: %v", err)
		}
		return production.GenbuConfigEditResult{}, submitErr
	}}
	result, err := app.taskServiceGenbuAction(ctx, preview, client)
	if err != nil || result.Data.(TaskServiceConfigPreviewResult).Status != "no-change" || result.Data.(TaskServiceConfigPreviewResult).PreviewToken != "" {
		t.Fatalf("no-change preview created a submit token: %v %v", result, err)
	}
	noChange = false
	result, err = app.taskServiceGenbuAction(ctx, preview, client)
	if err != nil {
		t.Fatal(err)
	}
	submit.Payload["previewToken"] = result.Data.(TaskServiceConfigPreviewResult).PreviewToken
	reopened, err := newApp(app.Paths.Home)
	if err != nil {
		t.Fatal(err)
	}
	otherResult, err := reopened.taskServiceGenbuAction(ctx, preview, client)
	if err != nil {
		t.Fatal(err)
	}
	var originalAttempt *GenbuConfigPendingError
	if _, err := app.taskServiceGenbuAction(ctx, submit, client); !errors.As(err, &originalAttempt) || originalAttempt.AttemptedAt.IsZero() {
		t.Fatalf("unknown submission status was lost: %v", err)
	}
	if _, err := app.taskServiceGenbuAction(ctx, submit, client); !errors.Is(err, errGenbuConfigPreviewGone) || submits != 1 {
		t.Fatalf("unknown submit could be repeated: %v", err)
	}
	otherSubmit := submit
	otherSubmit.Payload = maps.Clone(submit.Payload)
	otherSubmit.Payload["previewToken"] = otherResult.Data.(TaskServiceConfigPreviewResult).PreviewToken
	if _, err = reopened.taskServiceGenbuAction(ctx, otherSubmit, client); !errors.Is(err, production.ErrGenbuConfigSubmitUnknown) || submits != 1 {
		t.Fatalf("already-issued preview bypassed the submission record: %v", err)
	}
	// A fresh App has neither the old preview nor any in-memory pending state.
	reopened, err = newApp(app.Paths.Home)
	if err != nil {
		t.Fatal(err)
	}
	changedSnapshot = true
	var pending *GenbuConfigPendingError
	if _, err = reopened.taskServiceGenbuAction(ctx, preview, client); !errors.As(err, &pending) ||
		!pending.AttemptedAt.Equal(originalAttempt.AttemptedAt) || submits != 1 {
		t.Fatalf("restarting allowed a fresh preview of the unresolved submission: %v", err)
	}
	preview.Payload["content"] = "distinct edit"
	result, err = reopened.taskServiceGenbuAction(ctx, preview, client)
	if err != nil {
		t.Fatal("unresolved edit blocked another edit", err)
	}
	submitErr = context.DeadlineExceeded // Timing out before the POST is a definite failure.
	submit.Payload["previewToken"] = result.Data.(TaskServiceConfigPreviewResult).PreviewToken
	if _, err = reopened.taskServiceGenbuAction(ctx, submit, client); !errors.Is(err, submitErr) || submits != 2 {
		t.Fatal("definite submission failure was not returned", err)
	}
	if _, err = reopened.taskServiceGenbuAction(ctx, preview, client); err != nil {
		t.Fatal("definite failure left the edit blocked", err)
	}
}
