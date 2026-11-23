package application

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/delivery"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"
)

type genbuBuildLogFake func(service, tag string, tailLines int) (string, error)

func (f genbuBuildLogFake) BuildLog(_ context.Context, _ domain.AppConfig, service, tag string, tailLines int) (string, error) {
	return f(service, tag, tailLines)
}

func TestTagHistoryBuildLogReadsExactLocalOperation(t *testing.T) {
	app, base := taskServicesFixture(t)
	files := base.Payload["taskServicesContext"].(TaskServicesContext).TaskFilesContext
	tag := "1.27.8.beta-1"
	op := domain.TagOperation{
		OperationID: delivery.NewTagJobID(), GroupServiceID: "service", ModuleID: "one", ServiceName: "service", FolderName: base.TaskID,
		RepositoryID: "repo", SourceBranch: "feature/fixture", Remote: "origin", TagMode: domain.TagBuildCurrentBranch,
		State: domain.TagOperationSuccess, CreatedAt: "2026-11-19T08:00:00Z", UpdatedAt: "2026-11-19T08:00:00Z", Tag: &tag, ConflictFiles: []string{},
		GenbuStatus: domain.GenbuTagProbeStatus{Build: domain.GenbuUnknown, UAT: domain.GenbuUnknown, Production: domain.GenbuUnknown},
	}
	saveTagProbeFixture(t, files.TaskPath, op)
	if err := os.WriteFile(filepath.Join(files.TaskPath, "tag-operations", "unrelated.json"), []byte("not JSON"), 0600); err != nil {
		t.Fatal(err)
	}
	action := Action{Kind: "tag.history.build-log", TaskID: base.TaskID, Payload: map[string]any{
		"tagHistoryHome": files.Home, "tagHistoryTaskRoot": files.TaskRoot, "tagHistoryTaskPath": files.TaskPath,
		"tagHistoryTaskCreatedAt": files.TaskCreatedAt, "operationId": op.OperationID, "tag": "caller-is-not-source", "tailLines": 15,
	}}
	calls := 0
	client := genbuBuildLogFake(func(service, actualTag string, lines int) (string, error) {
		calls++
		if service != "alias" || actualTag != tag || lines != 15 {
			t.Fatalf("wrong log query: %s %s %d", service, actualTag, lines)
		}
		return "fixture logs", nil
	})
	ctx := context.Background()
	result, err := app.tagHistoryBuildLogAction(ctx, action, client)
	if err != nil || result.Data != (GenbuBuildLogResult{Service: "alias", Tag: tag, Text: "fixture logs"}) || calls != 1 {
		t.Fatalf("exact Tag log read failed: %v %v", result, err)
	}
	action.Payload["operationId"] = delivery.NewTagJobID()
	if _, err = app.tagHistoryBuildLogAction(ctx, action, client); err == nil || calls != 1 {
		t.Fatal("unowned operation reached Genbu")
	}
	action.Payload["operationId"], action.Payload["tagHistoryTaskCreatedAt"] = op.OperationID, "recreated"
	if _, err = app.tagHistoryBuildLogAction(ctx, action, client); err == nil || calls != 1 {
		t.Fatal("stale task reached Genbu")
	}
}

func TestProductionBuildLogUsesBatchTagAndRejectsDrift(t *testing.T) {
	app, _ := taskServicesFixture(t)
	store := production.New(app.Paths, nil, nil, nil).Store
	batch := production.Batch{
		ID: "0123456789abcdef0123456789abcdef", Target: production.Target{GenbuService: "recorded-service"},
		Version: "1.2.3", ReleaseBranch: "release/1.2.3", ProductionTag: "baseline-tag",
		Tags: []production.Tag{{Name: "selected-tag", Commit: "fixture-commit", Object: "fixture-object"}, {Name: "last-tag"}},
	}
	if err := store.Save(&batch); err != nil {
		t.Fatal(err)
	}
	action := Action{Kind: "production.build-log", ServiceID: "caller-service", Payload: map[string]any{"batchId": batch.ID, "tag": "selected-tag"}}
	client := genbuBuildLogFake(func(service, tag string, lines int) (string, error) {
		if service != "recorded-service" || tag != "selected-tag" || lines != 100 {
			t.Fatalf("wrong log query: %s %s %d", service, tag, lines)
		}
		return "fixture logs", nil
	})
	ctx := context.Background()
	if result, err := app.productionBuildLogAction(ctx, action, client); err != nil || result.Data.(GenbuBuildLogResult).Tag != "selected-tag" {
		t.Fatalf("selected Tag log read failed: %v %v", result, err)
	}
	action.Payload["tag"] = "baseline-tag"
	if _, err := app.productionBuildLogAction(ctx, action, client); err == nil {
		t.Fatal("unrecorded production baseline was treated as a batch Tag")
	}
	action.Payload["tag"] = "selected-tag"
	client = func(service, tag string, lines int) (string, error) {
		batch.Tags = batch.Tags[1:]
		if err := store.Save(&batch); err != nil {
			t.Fatal(err)
		}
		return "stale logs", nil
	}
	if result, err := app.productionBuildLogAction(ctx, action, client); err == nil || result.Data != nil {
		t.Fatal("changed batch published a late response", result, err)
	}
}
