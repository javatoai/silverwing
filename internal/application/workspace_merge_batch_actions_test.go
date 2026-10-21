package application

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

func TestDispatchWorkspaceMergeBatchUsesSelectionKeysAndReturnsBatchData(t *testing.T) {
	paths := storage.NewPaths(t.TempDir())
	app := &App{Paths: paths, Config: storage.NewConfigStore(paths), Manifests: storage.NewManifestStore()}
	config := domain.DefaultAppConfig()
	config.TaskRoot = &paths.Tasks
	if err := app.Config.Save(config); err != nil {
		t.Fatal(err)
	}

	baseRef, targetBranch := "origin/main", "feature/batch-merge"
	manifest := domain.DefaultTaskManifest()
	manifest.FolderName = "Batch merge test"
	manifest.TaskDirectoryName = "batch-merge-test"
	manifest.FeatureBranch = targetBranch
	manifest.CreatedAt = "2026-10-16T11:00:00+08:00"
	manifest.Services = []domain.ServiceWorkspace{
		{
			RepositoryID: "repo-a", GroupServiceID: "service-a", ModuleID: "module-a", ModuleName: "module-a",
			ServiceName: "service-a", DevelopmentTool: domain.DevelopmentToolVisualStudioCode, RepositoryPath: filepath.Join(paths.Home, "repo-a"),
			WorktreePath: filepath.Join(paths.Tasks, "batch-merge-test", "service-a"),
			Branch:       targetBranch, Health: domain.WorkspaceCreating, Strategy: domain.WorkspaceStandardWorktree,
			Warnings: []string{}, ModuleSource: domain.TaskModuleConfigured, PushRemote: "origin",
			BaseRef: &baseRef, TargetBranch: &targetBranch,
		},
		{
			RepositoryID: "repo-b", GroupServiceID: "service-b", ModuleID: "module-b", ModuleName: "module-b",
			ServiceName: "service-b", DevelopmentTool: domain.DevelopmentToolVisualStudioCode, RepositoryPath: filepath.Join(paths.Home, "repo-b"),
			WorktreePath: filepath.Join(paths.Tasks, "batch-merge-test", "service-b"),
			Branch:       targetBranch, Health: domain.WorkspaceCreating, Strategy: domain.WorkspaceStandardWorktree,
			Warnings: []string{}, ModuleSource: domain.TaskModuleConfigured, PushRemote: "origin",
			BaseRef: &baseRef, TargetBranch: &targetBranch,
		},
	}
	taskPath := filepath.Join(paths.Tasks, manifest.TaskDirectoryName)
	if err := app.Manifests.Save(taskPath, manifest); err != nil {
		t.Fatal(err)
	}

	selectedPath := manifest.Services[1].WorktreePath
	confirmation := workspace.WorkspaceMainBranchMergeBatchConfirmation{
		TaskDirectoryName: manifest.TaskDirectoryName,
		CreatedAt:         manifest.CreatedAt,
		GroupID:           manifest.GroupID,
		FeatureBranch:     manifest.FeatureBranch,
		Workspaces: []workspace.WorkspaceMainBranchMergeBatchConfirmedWorkspace{{
			Record: manifest.Services[1], SourceRef: "origin/main",
		}},
	}
	action := Action{
		Kind:   "workspace.merge-batch",
		TaskID: manifest.TaskDirectoryName,
		Payload: map[string]any{
			"selectionKeys": []string{selectedPath}, "confirmation": confirmation,
		},
	}
	result, err := app.Dispatch(context.Background(), action)
	if err != nil {
		t.Fatalf("batch action returned a top-level error: %v", err)
	}
	if action.Kind != "workspace.merge-batch" || result.Message != "批量主分支合并已返回，请查看逐模块结果" {
		t.Fatalf("batch action route/result message mismatch: kind=%q result=%+v", action.Kind, result)
	}
	batch, ok := result.Data.(workspace.WorkspaceMainBranchMergeBatchResult)
	if !ok {
		t.Fatalf("batch result has unexpected type %T", result.Data)
	}
	if len(batch.Items) != 1 {
		t.Fatalf("selectionKeys returned %d items, want exactly one: %#v", len(batch.Items), batch.Items)
	}
	item := batch.Items[0]
	if item.WorkspacePath != selectedPath || item.ServiceName != "service-b" || item.ModuleName != "module-b" {
		t.Fatalf("batch result did not match the selected module: %#v", item)
	}
	if item.Outcome != workspace.MainBranchFailed || item.Message == "" {
		t.Fatalf("not-ready synthetic worktree did not return an item-level failure: %#v", item)
	}
}

func TestDispatchWorkspaceMergeBatchRejectsReplacedRecordBeforeGit(t *testing.T) {
	paths := storage.NewPaths(t.TempDir())
	app := &App{Paths: paths, Config: storage.NewConfigStore(paths), Manifests: storage.NewManifestStore()}
	config := domain.DefaultAppConfig()
	config.TaskRoot = &paths.Tasks
	missingGit := filepath.Join(paths.Home, "git-must-not-run.exe")
	config.GitExecutablePath = &missingGit
	if err := app.Config.Save(config); err != nil {
		t.Fatal(err)
	}

	baseRef, targetBranch := "origin/main", "feature/batch-merge"
	oldRecord := domain.ServiceWorkspace{
		RepositoryID: "repo", GroupServiceID: "service", ModuleID: "module", ModuleName: "module",
		ServiceName: "service", DevelopmentTool: domain.DevelopmentToolVisualStudioCode,
		RepositoryPath: filepath.Join(paths.Home, "repo"), WorktreePath: filepath.Join(paths.Tasks, "same-path"),
		Branch: targetBranch, Health: domain.WorkspaceReady, Strategy: domain.WorkspaceStandardWorktree,
		Warnings: []string{}, ModuleSource: domain.TaskModuleConfigured,
		PushRemote: "origin", BaseRef: &baseRef, TargetBranch: &targetBranch,
	}
	manifest := domain.DefaultTaskManifest()
	manifest.TaskDirectoryName, manifest.CreatedAt, manifest.GroupID, manifest.FeatureBranch = "same-path", "old-created", "group", targetBranch
	manifest.Services = []domain.ServiceWorkspace{oldRecord}
	taskPath := filepath.Join(paths.Tasks, manifest.TaskDirectoryName)
	if err := app.Manifests.Save(taskPath, manifest); err != nil {
		t.Fatal(err)
	}

	confirmation := workspace.WorkspaceMainBranchMergeBatchConfirmation{
		TaskDirectoryName: manifest.TaskDirectoryName, CreatedAt: manifest.CreatedAt,
		GroupID: manifest.GroupID, FeatureBranch: manifest.FeatureBranch,
		Workspaces: []workspace.WorkspaceMainBranchMergeBatchConfirmedWorkspace{{Record: oldRecord, SourceRef: "origin/main"}},
	}
	newRecord := oldRecord
	newRecord.Branch = "feature/replaced"
	newTarget := newRecord.Branch
	newRecord.TargetBranch = &newTarget
	manifest.Services[0] = newRecord
	if err := app.Manifests.Save(taskPath, manifest); err != nil {
		t.Fatal(err)
	}

	result, err := app.Dispatch(context.Background(), Action{
		Kind: "workspace.merge-batch", TaskID: manifest.TaskDirectoryName, Payload: map[string]any{
			"selectionKeys": []string{oldRecord.WorktreePath}, "confirmation": confirmation,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "工作区信息已变化") {
		t.Fatalf("replaced record should be rejected before any Git operation, result=%+v err=%v", result, err)
	}
	if result.Data != nil {
		t.Fatalf("rejected confirmation unexpectedly returned a batch result: %#v", result.Data)
	}
}

func TestDispatchWorkspaceMergeBatchRejectsTaskReplacementAtSamePath(t *testing.T) {
	paths := storage.NewPaths(t.TempDir())
	app := &App{Paths: paths, Config: storage.NewConfigStore(paths), Manifests: storage.NewManifestStore()}
	config := domain.DefaultAppConfig()
	config.TaskRoot = &paths.Tasks
	if err := app.Config.Save(config); err != nil {
		t.Fatal(err)
	}

	baseRef, targetBranch := "origin/main", "feature/batch-merge"
	record := domain.ServiceWorkspace{
		RepositoryID: "repo", GroupServiceID: "service", ModuleID: "module", ModuleName: "module",
		ServiceName: "service", DevelopmentTool: domain.DevelopmentToolVisualStudioCode,
		RepositoryPath: filepath.Join(paths.Home, "repo"), WorktreePath: filepath.Join(paths.Tasks, "same-path"), Branch: targetBranch,
		Health: domain.WorkspaceCreating, Strategy: domain.WorkspaceStandardWorktree, ModuleSource: domain.TaskModuleConfigured,
		Warnings: []string{}, PushRemote: "origin",
		BaseRef: &baseRef, TargetBranch: &targetBranch,
	}
	manifest := domain.DefaultTaskManifest()
	manifest.TaskDirectoryName, manifest.CreatedAt, manifest.GroupID, manifest.FeatureBranch = "same-path", "new-created", "group", targetBranch
	manifest.Services = []domain.ServiceWorkspace{record}
	taskPath := filepath.Join(paths.Tasks, manifest.TaskDirectoryName)
	if err := app.Manifests.Save(taskPath, manifest); err != nil {
		t.Fatal(err)
	}
	confirmation := workspace.WorkspaceMainBranchMergeBatchConfirmation{
		TaskDirectoryName: manifest.TaskDirectoryName, CreatedAt: "old-created",
		GroupID: manifest.GroupID, FeatureBranch: manifest.FeatureBranch,
		Workspaces: []workspace.WorkspaceMainBranchMergeBatchConfirmedWorkspace{{Record: record, SourceRef: "origin/main"}},
	}
	result, err := app.Dispatch(context.Background(), Action{
		Kind: "workspace.merge-batch", TaskID: manifest.TaskDirectoryName, Payload: map[string]any{
			"selectionKeys": []string{record.WorktreePath}, "confirmation": confirmation,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "任务信息已变化") {
		t.Fatalf("task replaced at same path should be rejected, result=%+v err=%v", result, err)
	}
}
