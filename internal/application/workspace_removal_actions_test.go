package application

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func TestWorkspaceRemovalActionRechecksTaskDirectoryIdentity(t *testing.T) {
	app, _, manifest, taskPath := taskContextScopeFixture(t)
	path := filepath.Join(taskPath, "missing-module")
	manifest.Services = []domain.ServiceWorkspace{{
		RepositoryID: "repo", GroupServiceID: "service", ModuleID: "module",
		WorktreePath: path, RepositoryPath: path, Branch: "feature/task",
		Strategy: domain.WorkspaceIndependentClone, Health: domain.WorkspaceReady,
		ModuleSource: domain.TaskModuleConfigured, DevelopmentTool: domain.DevelopmentToolIntelliJIdea,
		TagMode: domain.TagBuildMergeToTargetBranch,
	}}
	if err := app.Manifests.Save(taskPath, manifest); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	request := Action{
		Kind: "workspace.remove-preview", TaskID: manifest.TaskDirectoryName, ServiceID: "service", ModuleID: "module", Path: path,
		Payload: map[string]any{
			"workspaceRemovalRequestID": "1", "workspaceRemovalHome": app.Paths.Home,
			"workspaceRemovalTaskPath": taskPath, "workspaceRemovalTaskCreatedAt": "stale",
		},
	}
	if _, err := app.Dispatch(ctx, request); err == nil {
		t.Fatal("the preview accepted stale desktop task identity")
	}
	request.Payload["workspaceRemovalTaskCreatedAt"] = manifest.CreatedAt
	previewResult, err := app.Dispatch(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	preview, ok := previewResult.Data.(workspace.WorkspaceModuleRemovalPreview)
	if !ok || !preview.PathMissing {
		t.Fatalf("missing directory preview: %+v", previewResult)
	}
	request.Kind = "workspace.remove"
	request.Payload["preview"] = preview
	request.Payload["confirmation"] = workspace.WorkspaceModuleRemovalConfirmation{Fingerprint: preview.Fingerprint}
	if err := os.Rename(taskPath, taskPath+"-parked"); err != nil {
		t.Fatal(err)
	}
	if err := app.Manifests.Save(taskPath, manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Dispatch(ctx, request); err == nil {
		t.Fatal("confirmation accepted replacement task at the original path with matching manifest")
	}
	saved, err := app.Manifests.Load(taskPath)
	if err != nil || len(saved.Services) != 1 {
		t.Fatalf("replacement task changed: %+v %v", saved, err)
	}
	delete(request.Payload, "preview")
	previewResponse, err := app.Dispatch(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	preview = previewResponse.Data.(map[string]any)["preview"].(workspace.WorkspaceModuleRemovalPreview)
	request.Payload["preview"] = preview
	request.Payload["confirmation"] = workspace.WorkspaceModuleRemovalConfirmation{Fingerprint: preview.Fingerprint}
	removed, err := app.Dispatch(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	result := removed.Data.(workspace.WorkspaceModuleRemovalResult)
	if result.DirectoryRecycled || len(result.Manifest.Services) != 0 {
		t.Fatalf("missing directory removal result: %+v", result)
	}
}
