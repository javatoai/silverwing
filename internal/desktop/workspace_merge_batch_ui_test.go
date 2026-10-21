package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo/ui"
)

type workspaceMergeBatchTestBackend struct{}

func (workspaceMergeBatchTestBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, nil
}

func (workspaceMergeBatchTestBackend) Dispatch(context.Context, application.Action) (application.ActionResult, error) {
	return application.ActionResult{}, nil
}

func TestOpenWorkspaceMergeBatchSelectsEligibleModulesAndShowsUnavailableOnes(t *testing.T) {
	base, target := "origin/main", "feature/payment"
	ready := application.Workspace{
		ServiceID: "service", ModuleID: "api", Name: "api-service", Path: `C:\tasks\payment\api`, State: "READY",
		Record: domain.ServiceWorkspace{GroupServiceID: "service", ModuleID: "api", ServiceName: "api-service", ModuleName: "api", WorktreePath: `C:\tasks\payment\api`, Branch: target, Health: domain.WorkspaceReady, Strategy: domain.WorkspaceStandardWorktree, BaseRef: &base, TargetBranch: &target},
	}
	withoutTarget := application.Workspace{
		ServiceID: "service", ModuleID: "worker", Name: "api-service", Path: `C:\tasks\payment\worker`, State: "READY",
		Record: domain.ServiceWorkspace{GroupServiceID: "service", ModuleID: "worker", ServiceName: "api-service", ModuleName: "worker", WorktreePath: `C:\tasks\payment\worker`, Branch: "feature/payment", Health: domain.WorkspaceReady, Strategy: domain.WorkspaceStandardWorktree, BaseRef: &base},
	}
	configJSON, err := json.Marshal(domain.DefaultAppConfig())
	if err != nil {
		t.Fatal(err)
	}
	taskState := task{id: "task", path: `C:\tasks\payment`, createdAt: "created", groupID: "group", branch: target, workspaces: []application.Workspace{ready, withoutTarget}}
	appState := &app{backend: workspaceMergeBatchTestBackend{}, page: "tasks", tab: 0, selected: 0, tasks: []task{taskState}, configJSON: string(configJSON)}

	appState.openWorkspaceMergeBatch(taskState)

	state := appState.workspaceMergeBatch
	if state == nil || len(state.Items) != 2 {
		t.Fatalf("batch dialog should contain every task module: %#v", state)
	}
	if !state.Items[0].Selected || state.Items[0].DisabledReason != "" {
		t.Errorf("ready feature branch was not selected by default: %#v", state.Items[0])
	}
	if state.Items[1].Selected || !strings.Contains(state.Items[1].DisabledReason, "目标分支") {
		t.Errorf("module without a task target should remain visible and disabled: %#v", state.Items[1])
	}
}

func TestWorkspaceMergeBatchResultIdentityAndConflictCopy(t *testing.T) {
	base, target := "origin/main", "feature/payment"
	item := application.Workspace{
		ServiceID: "service", ModuleID: "api", Name: "api-service", Path: `C:\tasks\payment\api`,
		Record: domain.ServiceWorkspace{GroupServiceID: "service", ModuleID: "api", ServiceName: "api-service", ModuleName: "api", WorktreePath: `C:\tasks\payment\api`, Branch: target, BaseRef: &base, TargetBranch: &target},
	}
	state := &workspaceMergeBatchUIState{Items: []workspaceMergeBatchItemState{{Workspace: item, Selected: true}}}
	result := workspace.WorkspaceMainBranchMergeResult{WorkspacePath: item.Path, ServiceName: item.Record.ServiceName, ModuleName: item.Record.ModuleName,
		SourceRef: "origin/main", TargetBranch: target, Outcome: workspace.MainBranchConflict,
		Message: "我将origin/main合并到feature/payment时出现冲突，请你帮忙解决。", ConflictFiles: []string{"src/a.go", "src/b.go"}}
	if !workspaceMergeBatchResultsMatch(state, []workspace.WorkspaceMainBranchMergeResult{result}) {
		t.Fatal("result matching the selected module was rejected")
	}
	wrong := result
	wrong.TargetBranch = "feature/other"
	if workspaceMergeBatchResultsMatch(state, []workspace.WorkspaceMainBranchMergeResult{wrong}) {
		t.Fatal("result for a different target branch was accepted")
	}
	copyText := workspaceMergeBatchCopyText(result)
	if copyText != "我将origin/main合并到feature/payment时出现冲突，请你帮忙解决。\n合并方向：origin/main → feature/payment\n工作区：C:\\tasks\\payment\\api\n冲突文件：\nsrc/a.go\nsrc/b.go" {
		t.Fatalf("conflict copy text changed: %q", copyText)
	}
	if strings.Contains(copyText, "请先解决冲突后再继续") || strings.Contains(copyText, "recordID") {
		t.Fatalf("conflict copy text includes stale guidance or record IDs: %q", copyText)
	}
}

func TestWorkspaceMergeBatchRetiresCompletionWhenTaskWorkspaceChanges(t *testing.T) {
	for _, completion := range []string{"result", "failure", "snapshot"} {
		t.Run(completion, func(t *testing.T) {
			appState, state, action, result := workspaceMergeBatchStaleCompletionFixture(t)
			current := appState.tasks[0].workspaces[0]
			current.Record.Branch = "feature/changed"
			appState.tasks[0].workspaces[0] = current

			switch completion {
			case "result":
				appState.applyWorkspaceMergeBatchResult(action, result)
			case "failure":
				appState.applyWorkspaceMergeBatchFailure(action, errors.New("late failure"))
			case "snapshot":
				state.Loading = false
				state.Results = &workspace.WorkspaceMainBranchMergeBatchResult{Items: []workspace.WorkspaceMainBranchMergeResult{}}
				appState.reconcileWorkspaceMergeBatchAfterSnapshot()
			}

			if appState.workspaceMergeBatch != state || state.Loading || !state.Open || state.Results != nil {
				t.Fatalf("stale completion was not retired into a closable warning state: appState=%#v state=%#v", appState.workspaceMergeBatch, state)
			}
			if !strings.Contains(state.Error, "工作区信息已变化") {
				t.Fatalf("stale completion did not explain why it was ignored: %q", state.Error)
			}
		})
	}
}

func TestWorkspaceMergeBatchCancellationKeepsBusyUntilPartialResultsReturn(t *testing.T) {
	appState, state, action, operationCtx := workspaceMergeBatchRunningFixture(t)
	tester := ui.NewTester(func(c *ui.Context) {
		appState.renderWorkspaceMergeBatchDialog(c, setTheme(c, false))
	}, 760, 600)

	if !cancellableOperation("workspace.merge-batch") || !tester.HasText("取消合并") {
		t.Fatal("a running batch must expose its explicit cancellation action")
	}
	if err := tester.Click("取消合并"); err != nil {
		t.Fatal(err)
	}
	if operationCtx.Err() != context.Canceled || !appState.busy || !state.Loading || !appState.operationCancelling || !state.Open {
		t.Fatalf("cancel request closed or settled the running operation early: ctx=%v busy=%v loading=%v cancelling=%v open=%v", operationCtx.Err(), appState.busy, state.Loading, appState.operationCancelling, state.Open)
	}
	tester.Frame()
	if !tester.HasText("正在取消…") || appState.workspaceMergeBatchCanRequestCancellation(state) {
		t.Fatal("pending cancellation must show a waiting label and disable repeated requests")
	}

	// Mirror the dispatcher settling the operation before delivering its partial data.
	appState.busy, appState.operation, appState.operationKind = false, "", ""
	appState.operationCancelling, appState.cancelOperation = false, nil
	partial := workspace.WorkspaceMainBranchMergeBatchResult{Items: []workspace.WorkspaceMainBranchMergeResult{
		{WorkspacePath: state.Items[0].Workspace.Path, ServiceName: "api-service", ModuleName: "api", SourceRef: "origin/main", TargetBranch: "feature/payment", Outcome: workspace.MainBranchMerged, Message: "合并成功"},
		{WorkspacePath: state.Items[1].Workspace.Path, ServiceName: "worker-service", ModuleName: "worker", SourceRef: "origin/main", TargetBranch: "feature/payment", Outcome: workspace.MainBranchFailed, Message: "批量操作已中断，未执行：context canceled"},
	}}
	appState.applyWorkspaceMergeBatchResult(action, application.ActionResult{Data: partial})
	if state.Loading || state.Results == nil || len(state.Results.Items) != 2 {
		t.Fatalf("partial operation results were not retained: %#v", state)
	}
	if state.Results.Items[0].Outcome != workspace.MainBranchMerged || state.Results.Items[1].Outcome != workspace.MainBranchFailed || !strings.Contains(state.Results.Items[1].Message, "未执行") {
		t.Fatalf("completed and unstarted results were not preserved: %#v", state.Results.Items)
	}
}

func TestWorkspaceMergeBatchCancellationCannotTargetAnotherTaskOrAction(t *testing.T) {
	appState, state, _, _ := workspaceMergeBatchRunningFixture(t)
	if !appState.workspaceMergeBatchOwnsActiveOperation(state) {
		t.Fatal("the current batch should own its matching active operation")
	}

	appState.operationKind = "task.create" // This operation is cancellable, but belongs to another action.
	if appState.workspaceMergeBatchOwnsActiveOperation(state) || appState.workspaceMergeBatchCanRequestCancellation(state) {
		t.Fatal("batch dialog exposed cancellation for a different action")
	}
	appState.operationKind = "workspace.merge-batch"
	current := appState.tasks[0]
	current.branch = "feature/replaced"
	appState.tasks[0] = current
	if appState.workspaceMergeBatchOwnsActiveOperation(state) || appState.workspaceMergeBatchCanRequestCancellation(state) {
		t.Fatal("batch dialog exposed cancellation after its task identity changed")
	}
}

func TestWorkspaceMergeBatchPreDispatchFailureReleasesDialogBusyState(t *testing.T) {
	appState, state, action, _ := workspaceMergeBatchRunningFixture(t)
	appState.busy, appState.operation, appState.operationKind = false, "", ""
	appState.operationCancelling, appState.cancelOperation = false, nil
	appState.applyWorkspaceMergeBatchFailure(action, context.Canceled)
	if state.Loading || state.Results != nil || !state.Open || !strings.Contains(state.Error, context.Canceled.Error()) {
		t.Fatalf("pre-dispatch cancellation failure did not release the dialog: %#v", state)
	}
}

func workspaceMergeBatchRunningFixture(t *testing.T) (*app, *workspaceMergeBatchUIState, application.Action, context.Context) {
	t.Helper()
	taskPath, target := `C:\tasks\payment`, "feature/payment"
	workspaceItems := []application.Workspace{
		{ServiceID: "service", ModuleID: "api", Name: "api-service", Path: filepath.Join(taskPath, "api"), Record: domain.ServiceWorkspace{
			GroupServiceID: "service", ModuleID: "api", ServiceName: "api-service", ModuleName: "api", WorktreePath: filepath.Join(taskPath, "api"), Branch: target,
		}},
		{ServiceID: "service", ModuleID: "worker", Name: "worker-service", Path: filepath.Join(taskPath, "worker"), Record: domain.ServiceWorkspace{
			GroupServiceID: "service", ModuleID: "worker", ServiceName: "worker-service", ModuleName: "worker", WorktreePath: filepath.Join(taskPath, "worker"), Branch: target,
		}},
	}
	state := &workspaceMergeBatchUIState{
		TaskID: "task", TaskPath: taskPath, TaskCreatedAt: "created", TaskGroupID: "group", TaskFeatureBranch: target,
		Token: 42, Open: true, Loading: true,
		Items: []workspaceMergeBatchItemState{
			{Workspace: workspaceItems[0], SourceRef: "origin/main", Selected: true},
			{Workspace: workspaceItems[1], SourceRef: "origin/main", Selected: true},
		},
	}
	taskState := task{id: state.TaskID, path: taskPath, createdAt: state.TaskCreatedAt, groupID: state.TaskGroupID, branch: target, workspaces: workspaceItems}
	operationCtx, cancel := context.WithCancel(context.Background())
	action := application.Action{Kind: "workspace.merge-batch", TaskID: state.TaskID, Path: state.TaskPath, Payload: map[string]any{"batchToken": state.Token}}
	appState := &app{
		page: "tasks", tab: 0, selected: 0, tasks: []task{taskState}, workspaceMergeBatch: state,
		busy: true, operation: "批量合并主分支", operationKind: action.Kind, cancelOperation: cancel,
	}
	state.scope, _ = appState.taskActionScopeFor(taskState)
	state.configJSON = appState.configJSON
	return appState, state, action, operationCtx
}

func workspaceMergeBatchStaleCompletionFixture(t *testing.T) (*app, *workspaceMergeBatchUIState, application.Action, application.ActionResult) {
	t.Helper()
	base, target := "origin/main", "feature/payment"
	item := application.Workspace{
		ServiceID: "service", ModuleID: "api", Name: "api-service", Path: `C:\tasks\payment\api`,
		Record: domain.ServiceWorkspace{GroupServiceID: "service", ModuleID: "api", ServiceName: "api-service", ModuleName: "api", WorktreePath: `C:\tasks\payment\api`, Branch: target, BaseRef: &base, TargetBranch: &target},
	}
	state := &workspaceMergeBatchUIState{
		TaskID: "task", TaskPath: `C:\tasks\payment`, TaskCreatedAt: "created", TaskGroupID: "group", TaskFeatureBranch: target, Token: 17,
		Open: true, Loading: true, Items: []workspaceMergeBatchItemState{{Workspace: item, Selected: true}},
	}
	taskState := task{id: state.TaskID, path: state.TaskPath, createdAt: state.TaskCreatedAt, groupID: state.TaskGroupID, branch: state.TaskFeatureBranch, workspaces: []application.Workspace{item}}
	appState := &app{page: "tasks", tab: 0, selected: 0, tasks: []task{taskState}, workspaceMergeBatch: state}
	state.scope, _ = appState.taskActionScopeFor(taskState)
	state.configJSON = appState.configJSON
	action := application.Action{Kind: "workspace.merge-batch", TaskID: state.TaskID, Path: state.TaskPath, Payload: map[string]any{"batchToken": state.Token}}
	result := application.ActionResult{Data: workspace.WorkspaceMainBranchMergeBatchResult{Items: []workspace.WorkspaceMainBranchMergeResult{{
		WorkspacePath: item.Path, ServiceName: item.Record.ServiceName, ModuleName: item.Record.ModuleName,
		SourceRef: "origin/main", TargetBranch: target, Outcome: workspace.MainBranchMerged, Message: "merged", ConflictFiles: []string{},
	}}}}
	return appState, state, action, result
}

func TestWorkspaceMergeBatchDialogLayoutCapture(t *testing.T) {
	outputDir := os.Getenv("SILVERWING_WORKSPACE_MERGE_BATCH_PROOF_DIR")
	if outputDir == "" {
		outputDir = t.TempDir()
	} else if err := os.MkdirAll(outputDir, 0o700); err != nil {
		t.Fatal(err)
	}

	base, target := "origin/main", "feature/payment-channel-optimization"
	items := []workspaceMergeBatchItemState{
		{Workspace: application.Workspace{
			ServiceID: "payment", ModuleID: "api", Name: "payment-channel-service", Path: `C:\tasks\payment-channel\api`,
			Record: domain.ServiceWorkspace{GroupServiceID: "payment", ModuleID: "api", ServiceName: "payment-channel-service", ModuleName: "api", WorktreePath: `C:\tasks\payment-channel\api`, Branch: target, BaseRef: &base, TargetBranch: &target},
		}, SourceRef: base, Selected: true},
		{Workspace: application.Workspace{
			ServiceID: "payment", ModuleID: "worker", Name: "payment-channel-service", Path: `C:\tasks\payment-channel\worker`,
			Record: domain.ServiceWorkspace{GroupServiceID: "payment", ModuleID: "worker", ServiceName: "payment-channel-service", ModuleName: "worker", WorktreePath: `C:\tasks\payment-channel\worker`, Branch: target, BaseRef: &base},
		}, SourceRef: base, DisabledReason: "没有与当前分支匹配的任务目标分支"},
		{Workspace: application.Workspace{
			ServiceID: "reporting", ModuleID: "sql", Name: "reporting-service", Path: `C:\tasks\payment-channel\reporting\sql`,
			Record: domain.ServiceWorkspace{GroupServiceID: "reporting", ModuleID: "sql", ServiceName: "reporting-service", ModuleName: "sql", WorktreePath: `C:\tasks\payment-channel\reporting\sql`, Branch: target, BaseRef: &base, TargetBranch: &target},
		}, SourceRef: base, Selected: true},
	}

	for _, scene := range []struct {
		name       string
		width      int
		height     int
		dark       bool
		loading    bool
		cancelling bool
	}{
		{name: "wide-light", width: 1000, height: 700},
		{name: "narrow-dark", width: 460, height: 540, dark: true},
		{name: "running-cancel", width: 1000, height: 700, loading: true},
		{name: "running-cancel-pending", width: 1000, height: 700, loading: true, cancelling: true},
	} {
		t.Run(scene.name, func(t *testing.T) {
			state := &workspaceMergeBatchUIState{
				TaskID: "task", TaskPath: `C:\tasks\payment-channel`, TaskCreatedAt: "created",
				TaskGroupID: "group", TaskFeatureBranch: target, Open: true, Loading: scene.loading, Items: items,
			}
			currentTask := task{
				id: state.TaskID, path: state.TaskPath, createdAt: state.TaskCreatedAt,
				groupID: state.TaskGroupID, branch: state.TaskFeatureBranch,
				workspaces: []application.Workspace{items[0].Workspace, items[1].Workspace, items[2].Workspace},
			}
			appState := &app{page: "tasks", tab: 0, selected: 0, tasks: []task{currentTask}, workspaceMergeBatch: state}
			state.scope, _ = appState.taskActionScopeFor(currentTask)
			state.configJSON = appState.configJSON
			if scene.loading {
				appState.busy = true
				appState.operationKind = "workspace.merge-batch"
				appState.cancelOperation = func() {}
				appState.operationCancelling = scene.cancelling
			}
			tester := ui.NewTester(func(c *ui.Context) {
				p := setTheme(c, scene.dark)
				appState.renderWorkspaceMergeBatchDialog(c, p)
			}, scene.width, scene.height)
			tester.Frame()
			if !tester.HasText("批量合并主分支") || !tester.HasText("origin/main → "+target) || !tester.HasText("没有与当前分支匹配的任务目标分支") {
				t.Fatal("dialog frame omitted its title, merge direction, or disabled-module reason")
			}
			if scene.loading && !scene.cancelling && !tester.HasText("取消合并") || scene.cancelling && !tester.HasText("正在取消…") {
				t.Fatal("running dialog frame omitted the active cancel or waiting state")
			}
			path := filepath.Join(outputDir, scene.name+".png")
			file, err := os.Create(path)
			if err != nil {
				t.Fatal(err)
			}
			encodeErr := png.Encode(file, tester.Image())
			closeErr := file.Close()
			if encodeErr != nil {
				t.Fatal(encodeErr)
			}
			if closeErr != nil {
				t.Fatal(closeErr)
			}
			t.Logf("layout screenshot: %s", path)
		})
	}
}
