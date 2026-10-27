package application

import (
	"encoding/json"

	"path/filepath"
	"reflect"

	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/integrations"
	"silverwing-go/internal/storage"
)

func taskIdentityFixture(t *testing.T, app *App, root, directory, declared string) domain.TaskManifest {
	t.Helper()
	manifest := domain.DefaultTaskManifest()
	manifest.TaskDirectoryName, manifest.FolderName = declared, directory

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
