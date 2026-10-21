package application

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/integrations"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

func taskIdentityFixture(t *testing.T, app *App, root, directory, declared string) domain.TaskManifest {
	t.Helper()
	manifest := domain.DefaultTaskManifest()
	manifest.FolderName, manifest.TaskDirectoryName, manifest.FeatureBranch = directory, declared, "feature/test"
	manifest.CreatedAt, manifest.UpdatedAt = "2026-10-15", "2026-10-15"
	if err := app.Manifests.Save(filepath.Join(root, directory), manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestTaskIdentityUsesCapturedConfigAndRejectsManifestAliases(t *testing.T) {
	paths := storage.NewPaths(t.TempDir())
	app := &App{Paths: paths, Config: storage.NewConfigStore(paths), Manifests: storage.NewManifestStore()}
	before := domain.DefaultAppConfig()
	before.TaskRoot = &paths.Tasks
	taskIdentityFixture(t, app, paths.Tasks, "task", "task")
	nextRoot := filepath.Join(paths.Home, "next")
	after := before
	after.TaskRoot = &nextRoot
	taskIdentityFixture(t, app, nextRoot, "task", "task")
	if err := app.Config.Save(after); err != nil {
		t.Fatal(err)
	}
	_, path, err := app.taskInConfig(Action{TaskID: "task"}, before)
	if err != nil || path != filepath.Join(paths.Tasks, "task") {
		t.Fatalf("captured configuration was replaced: %q %v", path, err)
	}
	taskIdentityFixture(t, app, nextRoot, "actual", "alias")
	for _, id := range []string{"actual", "alias"} {
		if _, _, err := app.task(Action{TaskID: id}); err == nil {
			t.Fatalf("mismatched manifest accepted through %q", id)
		}
	}
}

func TestWorkspaceDiffPrefetchRejectsChangedPathBeforeGit(t *testing.T) {
	paths := storage.NewPaths(t.TempDir())
	app := &App{Paths: paths, Config: storage.NewConfigStore(paths), Manifests: storage.NewManifestStore()}
	config := domain.DefaultAppConfig()
	config.TaskRoot = &paths.Tasks
	if err := app.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	manifest := taskIdentityFixture(t, app, paths.Tasks, "task", "task")
	manifest.Services = []domain.ServiceWorkspace{{GroupServiceID: "service", ModuleID: "module", RepositoryID: "repo", ServiceName: "service", RepositoryPath: paths.Home, WorktreePath: filepath.Join(paths.Tasks, "task", "current"), Branch: "feature/test", Strategy: domain.WorkspaceStandardWorktree, DevelopmentTool: domain.DevelopmentToolVisualStudioCode, Health: domain.WorkspaceReady, ModuleSource: domain.TaskModuleConfigured}}
	if err := app.Manifests.Save(filepath.Join(paths.Tasks, "task"), manifest); err != nil {
		t.Fatal(err)
	}
	err := app.PrefetchWorkspaceDiff(context.Background(), WorkspaceDiffPrefetchRequest{TaskID: "task", ServiceID: "service", ModuleID: "module", Path: filepath.Join(paths.Home, "stale"), SessionID: "opaque", Compare: workspace.CompareRequest{Mode: workspace.CompareUncommitted}})
	if err == nil || !strings.Contains(err.Error(), "工作区路径已变化") {
		t.Fatalf("stale path reached Git/session layer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (&App{}).PrefetchWorkspaceDiff(ctx, WorkspaceDiffPrefetchRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled prefetch touched state: %v", err)
	}
}

func TestRequirementCommentResourceViewPreservesScope(t *testing.T) {
	resource := integrations.RequirementCommentResource{CommentID: "comment-a", URL: "https://example.invalid/a.png", Path: "cache/account-a/image.png", Error: "read error", Generation: "generation-a"}
	for _, source := range []any{resource, integrations.RequirementCommentsReadResult{CommentResources: []integrations.RequirementCommentResource{resource}, Comments: []domain.RequirementComment{{ID: "comment-a", Content: "original\r\n"}}, Source: "CACHE", Stale: true, RefreshError: "refresh failed"}} {
		original, err := json.Marshal(source)
		if err != nil {
			t.Fatal(err)
		}
		projected, err := json.Marshal(requirementViewData(source))
		if err != nil || !reflect.DeepEqual(original, projected) {
			t.Fatalf("comment resource contract changed: %s / %s, %v", original, projected, err)
		}
	}
}
