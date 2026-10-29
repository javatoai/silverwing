package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func TestTypedTaskOperationsAndActionAdapterSharePersistedLifecycle(t *testing.T) {
	app, _, _, _ := taskContextScopeFixture(t)
	emptyTools := []string{}
	var creator TaskCreator = app
	progress := []workspace.TaskCreationProgress{}
	created, err := creator.CreateTask(context.Background(), TaskCreateRequest{CreateTaskRequest: workspace.CreateTaskRequest{FolderName: "typed", GroupID: domain.DefaultGroupID}, WorkspaceToolIDs: &emptyTools}, func(event workspace.TaskCreationProgress) { progress = append(progress, event) })
	if err != nil {
		t.Fatal(err)
	}
	if created.Data.TaskDirectoryName != "typed" || len(progress) == 0 {
		t.Fatalf("typed create: %+v progress=%v", created, progress)
	}
	for index, event := range progress {
		if event.Sequence != uint64(index+1) {
			t.Fatalf("unordered progress: %+v", progress)
		}
	}
	var lifecycle TaskLifecycle = app
	archived, err := lifecycle.ArchiveTask(context.Background(), TaskArchiveRequest{Target: NewTaskTarget("typed", "")})
	if err != nil || archived.Data.LifecycleStatus != domain.TaskArchived {
		t.Fatalf("typed archive: %+v %v", archived, err)
	}
	restoredAction, err := app.Dispatch(context.Background(), Action{Kind: "task.restore", TaskID: "typed"})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := ResultData[domain.TaskManifest](restoredAction)
	if err != nil || restored.LifecycleStatus == domain.TaskArchived {
		t.Fatalf("legacy restore did not consume typed use case: %+v %v", restored, err)
	}
	deleted, err := lifecycle.DeleteTask(context.Background(), TaskDeleteRequest{Target: NewTaskTarget("typed", "")})
	if err != nil || deleted.Message != "任务已删除" {
		t.Fatalf("typed delete: %+v %v", deleted, err)
	}
	if _, _, err = app.task(Action{TaskID: "typed"}); err == nil {
		t.Fatal("deleted task remained in catalog")
	}
	legacy, err := app.Dispatch(context.Background(), Action{Kind: "task.create", Payload: map[string]any{"title": "legacy", "workspaceToolIds": []string{}}})
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := ResultData[domain.TaskManifest](legacy)
	if err != nil || manifest.TaskDirectoryName != "legacy" {
		t.Fatalf("legacy alias create: %+v %v", manifest, err)
	}
}

func TestTypedTaskTargetOwnsPrivateScopeAndRejectsReplacedTask(t *testing.T) {
	app, config, manifest, path := taskContextScopeFixture(t)
	action := taskContextActionForTest(t, app, config, manifest, path, "task.archive")
	target := TaskTargetFromAction(action)
	action.Payload[desktopTaskContextPayloadKey].(map[string]any)["taskId"] = "mutated-after-capture"
	if _, err := app.ArchiveTask(context.Background(), TaskArchiveRequest{Target: target}); err != nil {
		t.Fatalf("request guard was aliased: %v", err)
	}
	if _, err := app.RestoreTask(context.Background(), NewTaskTarget(manifest.TaskDirectoryName, path)); err != nil {
		t.Fatal(err)
	}
	current, currentPath, err := app.task(Action{TaskID: manifest.TaskDirectoryName})
	if err != nil {
		t.Fatal(err)
	}
	action = taskContextActionForTest(t, app, config, current, currentPath, "task.archive")
	stale := TaskTargetFromAction(action)
	current.CreatedAt = "2026-10-20T10:00:00Z"
	if err := app.Manifests.Save(currentPath, current); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ArchiveTask(context.Background(), TaskArchiveRequest{Target: stale}); !errors.Is(err, errDesktopTaskContextChanged) {
		t.Fatalf("replacement task accepted: %v", err)
	}
	deleteTarget := TaskTargetFromAction(Action{Kind: "task.delete", TaskID: current.TaskDirectoryName, Path: currentPath, Payload: map[string]any{"taskDeleteCreatedAt": manifest.CreatedAt}})
	if _, err := app.DeleteTask(context.Background(), TaskDeleteRequest{Target: deleteTarget}); !errors.Is(err, errDesktopTaskContextChanged) {
		t.Fatalf("delete incarnation guard lost: %v", err)
	}
	if _, err := os.Stat(currentPath); err != nil {
		t.Fatalf("stale request touched task: %v", err)
	}
}

func TestTypedTaskCreationCancellationPrecedesFilesystemChanges(t *testing.T) {
	app, config, _, _ := taskContextScopeFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := app.CreateTask(ctx, TaskCreateRequest{CreateTaskRequest: workspace.CreateTaskRequest{FolderName: "cancelled"}}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if _, err := os.Stat(filepath.Join(*config.TaskRoot, "cancelled")); !os.IsNotExist(err) {
		t.Fatalf("cancelled request wrote task: %v", err)
	}
}
