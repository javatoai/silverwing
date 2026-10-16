package workspace

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestWorkspaceMainBranchMergeBatchContinuesAfterWorkspaceFailures(t *testing.T) {
	taskPath := t.TempDir()
	service := NewWorkspaceMainBranchMergeService(nil, storage.NewManifestStore(), NewOperationLocks(filepath.Join(t.TempDir(), "locks")), nil)
	requested := []domain.ServiceWorkspace{
		{WorktreePath: filepath.Join(taskPath, "one"), ServiceName: "Service A", ModuleName: "API", Branch: "feature/a", BaseRef: mergeBatchStringPointer("origin/main")},
		{WorktreePath: filepath.Join(taskPath, "two"), ServiceName: "Service B", ModuleName: "worker", Branch: "feature/b", BaseRef: mergeBatchStringPointer("origin/main")},
	}

	result := service.MergeBatch(context.Background(), domain.AppConfig{}, taskPath, domain.TaskManifest{TaskDirectoryName: "task", CreatedAt: "created"}, requested)

	if len(result.Items) != len(requested) {
		t.Fatalf("MergeBatch returned %d items, want %d", len(result.Items), len(requested))
	}
	for index, item := range result.Items {
		if item.Outcome != MainBranchFailed || item.WorkspacePath != requested[index].WorktreePath || item.ServiceName != requested[index].ServiceName || item.ModuleName != requested[index].ModuleName {
			t.Errorf("item %d lost its independent failure result: %#v", index, item)
		}
		if !strings.Contains(item.Message, "silverwing.json") {
			t.Errorf("item %d did not retain its own manifest error: %q", index, item.Message)
		}
	}
}

func TestWorkspaceMainBranchMergeBatchMarksUnstartedItemsAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	requested := []domain.ServiceWorkspace{
		{WorktreePath: `C:\synthetic\one`, ServiceName: "Service A", Branch: "feature/a"},
		{WorktreePath: `C:\synthetic\two`, ServiceName: "Service B", Branch: "feature/b"},
	}
	service := NewWorkspaceMainBranchMergeService(nil, storage.NewManifestStore(), NewOperationLocks(t.TempDir()), nil)

	result := service.MergeBatch(ctx, domain.AppConfig{}, t.TempDir(), domain.TaskManifest{}, requested)

	if len(result.Items) != len(requested) {
		t.Fatalf("MergeBatch returned %d pending items, want %d", len(result.Items), len(requested))
	}
	for index, item := range result.Items {
		if item.Outcome != MainBranchFailed || item.WorkspacePath != requested[index].WorktreePath || !strings.Contains(item.Message, "批量操作已中断，未执行") {
			t.Errorf("item %d did not report cancellation before execution: %#v", index, item)
		}
	}
}

func TestWorkspaceMainBranchMergeBatchConflictMessage(t *testing.T) {
	got := workspaceMainBranchMergeConflictMessage("origin/main", "feature/payment")
	want := "我将origin/main合并到feature/payment时出现冲突，请你帮忙解决。"
	if got != want {
		t.Fatalf("conflict message = %q, want %q", got, want)
	}
}

func TestWorkspaceMainBranchMergeBatchConfirmationRejectsChangedRecordsTaskAndSource(t *testing.T) {
	featureBranch := "feature/payment"
	workspaceRecord := domain.ServiceWorkspace{
		GroupServiceID: "service", ModuleID: "api", WorktreePath: `C:\tasks\payment\api`,
		Branch: featureBranch, Health: domain.WorkspaceReady, Strategy: domain.WorkspaceStandardWorktree,
		TargetBranch: &featureBranch,
	}
	manifest := domain.TaskManifest{TaskDirectoryName: "payment", CreatedAt: "created", GroupID: "group", FeatureBranch: featureBranch}
	config := domain.DefaultAppConfig()
	config.Groups = []domain.GroupConfig{{ID: "group", Services: []domain.GroupServiceConfig{{
		ID: "service", MasterBranch: "origin/main", Modules: []domain.ServiceModuleConfig{{ID: "api"}},
	}}}}
	confirmation := WorkspaceMainBranchMergeBatchConfirmation{
		TaskDirectoryName: manifest.TaskDirectoryName, CreatedAt: manifest.CreatedAt,
		GroupID: manifest.GroupID, FeatureBranch: manifest.FeatureBranch,
		Workspaces: []WorkspaceMainBranchMergeBatchConfirmedWorkspace{{Record: workspaceRecord, SourceRef: "origin/main"}},
	}

	t.Run("record changed with same module key", func(t *testing.T) {
		changed := workspaceRecord
		changed.Branch = "feature/replaced"
		if _, err := confirmation.ConfirmedWorkspaces(config, manifest, []domain.ServiceWorkspace{changed}); err == nil || !strings.Contains(err.Error(), "工作区信息已变化") {
			t.Fatalf("replaced workspace record should be rejected, err=%v", err)
		}
	})

	t.Run("task replaced at same path", func(t *testing.T) {
		replaced := manifest
		replaced.CreatedAt = "replacement-created"
		if _, err := confirmation.ConfirmedWorkspaces(config, replaced, []domain.ServiceWorkspace{workspaceRecord}); err == nil || !strings.Contains(err.Error(), "任务信息已变化") {
			t.Fatalf("replacement task identity should be rejected, err=%v", err)
		}
	})

	t.Run("configured main branch changed", func(t *testing.T) {
		withoutSavedBase := workspaceRecord
		withoutSavedBase.BaseRef = nil
		confirmation.Workspaces[0].Record = withoutSavedBase
		changed := config
		changed.Groups = []domain.GroupConfig{{ID: "group", Services: []domain.GroupServiceConfig{{
			ID: "service", MasterBranch: "origin/develop", Modules: []domain.ServiceModuleConfig{{ID: "api"}},
		}}}}
		if _, err := confirmation.ConfirmedWorkspaces(changed, manifest, []domain.ServiceWorkspace{withoutSavedBase}); err == nil || !strings.Contains(err.Error(), "主分支配置已变化") {
			t.Fatalf("changed configured source branch should be rejected, err=%v", err)
		}
	})
}

func mergeBatchStringPointer(value string) *string { return &value }
