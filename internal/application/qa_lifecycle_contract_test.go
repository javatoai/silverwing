package application

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

type qaTaskLifecycleSnapshot struct {
	manifest      []byte
	documents     map[string][]byte
	routerPresent bool
	router        []byte
}

func captureQATaskLifecycleSnapshot(t *testing.T, taskPath string) qaTaskLifecycleSnapshot {
	t.Helper()
	snapshot := qaTaskLifecycleSnapshot{
		documents: make(map[string][]byte),
	}
	var err error
	snapshot.manifest, err = os.ReadFile(filepath.Join(taskPath, storage.TaskManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{
		filepath.Join(workspace.AgentWorkspaceDir, workspace.AgentDirectoryName, workspace.AgentContextFileName),
		filepath.Join(workspace.AgentWorkspaceDir, workspace.AgentDirectoryName, workspace.AgentRulesFileName),
	} {
		content, err := os.ReadFile(filepath.Join(taskPath, relative))
		if err != nil {
			t.Fatalf("read task document %s: %v", relative, err)
		}
		snapshot.documents[relative] = content
	}
	routerPath := filepath.Join(taskPath, workspace.AgentRouterFileName)
	if _, err := os.Lstat(routerPath); err == nil {
		snapshot.routerPresent = true
		snapshot.router, err = os.ReadFile(routerPath)
		if err != nil {
			t.Fatalf("read optional task router %s: %v", routerPath, err)
		}
	} else if !os.IsNotExist(err) {
		t.Fatalf("inspect optional task router %s: %v", routerPath, err)
	}
	return snapshot
}

func assertQATaskLifecycleSnapshotUnchanged(t *testing.T, taskPath string, before qaTaskLifecycleSnapshot) {
	t.Helper()
	after := captureQATaskLifecycleSnapshot(t, taskPath)
	if !bytes.Equal(after.manifest, before.manifest) {
		t.Fatal("invalid lifecycle transition changed manifest bytes or UpdatedAt")
	}
	if after.routerPresent != before.routerPresent {
		t.Fatalf("invalid lifecycle transition changed optional %s presence from %t to %t", workspace.AgentRouterFileName, before.routerPresent, after.routerPresent)
	}
	if before.routerPresent && !bytes.Equal(after.router, before.router) {
		t.Errorf("invalid lifecycle transition changed optional %s bytes", workspace.AgentRouterFileName)
	}
	for relative, expected := range before.documents {
		if !bytes.Equal(after.documents[relative], expected) {
			t.Errorf("invalid lifecycle transition changed document %s", relative)
		}
	}
}

func TestQATaskLifecycleRejectsInvalidPublicTransitionsWithoutWrites(t *testing.T) {
	app := qaTestApp(t)
	ctx := context.Background()
	created, err := app.Dispatch(ctx, Action{Kind: "task.create", Payload: map[string]any{
		"folderName": "lifecycle-contract", "taskNotes": "preserve lifecycle notes", "workspaceToolIds": []string{},
	}})
	if err != nil {
		t.Fatalf("create lifecycle test task: %v", err)
	}
	taskPath := created.Path
	manifest, err := app.Manifests.Load(taskPath)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.LifecycleStatus != domain.TaskActive {
		t.Fatalf("new task status = %q, want active", manifest.LifecycleStatus)
	}

	// Public actions intentionally contain only Kind and TaskID; there is no
	// private desktop scope payload or path confirmation.
	before := captureQATaskLifecycleSnapshot(t, taskPath)
	if _, err := app.Dispatch(ctx, Action{Kind: "task.restore", TaskID: manifest.TaskDirectoryName}); err == nil || !strings.Contains(err.Error(), "只有已归档任务可以恢复") {
		t.Fatalf("restore-active error = %v, want archived-only rejection", err)
	}
	assertQATaskLifecycleSnapshotUnchanged(t, taskPath, before)

	if _, err := app.Dispatch(ctx, Action{Kind: "task.archive", TaskID: manifest.TaskDirectoryName}); err != nil {
		t.Fatalf("valid active-to-archived transition: %v", err)
	}
	manifest, err = app.Manifests.Load(taskPath)
	if err != nil || manifest.LifecycleStatus != domain.TaskArchived {
		t.Fatalf("archive transition status = %q, err=%v", manifest.LifecycleStatus, err)
	}

	before = captureQATaskLifecycleSnapshot(t, taskPath)
	if _, err := app.Dispatch(ctx, Action{Kind: "task.archive", TaskID: manifest.TaskDirectoryName}); err == nil || !strings.Contains(err.Error(), "任务已经归档") {
		t.Fatalf("archive-archived error = %v, want already-archived rejection", err)
	}
	assertQATaskLifecycleSnapshotUnchanged(t, taskPath, before)

	if _, err := app.Dispatch(ctx, Action{Kind: "task.restore", TaskID: manifest.TaskDirectoryName}); err != nil {
		t.Fatalf("valid archived-to-active transition: %v", err)
	}
	manifest, err = app.Manifests.Load(taskPath)
	if err != nil || manifest.LifecycleStatus != domain.TaskActive {
		t.Fatalf("restore transition status = %q, err=%v", manifest.LifecycleStatus, err)
	}
}
