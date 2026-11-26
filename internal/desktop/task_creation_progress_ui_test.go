package desktop

import (
	"context"
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/workspace"
)

func taskCreationProgressFixture(t *testing.T, preflight bool) (*app, *taskCreationProgressRequest, *ui.Tester) {
	t.Helper()
	s, _ := taskCreationCompactFixture(t, 960, false, 1)
	form := s.taskUnit.state.taskCreation
	form.progressGeneration++
	form.preview = s.snapshotTaskCreationPreview(nil)
	form.creationProgress = newTaskCreationProgressState(s, form, form.progressGeneration)
	progress := form.creationProgress
	progress.services = []*taskCreationProgressService{
		{id: "alpha", name: "Alpha", modules: []*taskCreationProgressModule{{id: "default", name: "API"}}},
		{id: "beta", name: "Beta", modules: []*taskCreationProgressModule{{id: "default", name: "Worker"}}},
		{id: "gamma", name: "Gamma", modules: []*taskCreationProgressModule{{id: "default", name: "Scheduler"}}},
	}
	request := &taskCreationProgressRequest{
		ctx: context.Background(), state: form, progress: progress,
		token: form.token, generation: form.progressGeneration, home: s.snapshot.Home,
		configJSON: s.settingsUnit.state.configJSON, signature: form.preview.signature,
		kind: "task.create",
	}
	form.phase = "creating"
	s.busy, s.operationKind = true, "task.create"
	if preflight {
		progress.mode, form.phase = "preflight", "preview"
		action := application.Action{Kind: "task.create-preview", Payload: map[string]any{"taskCreateToken": form.token}}
		preflightRequest, err := s.prepareTaskCreationPreflight(action)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.completeTaskCreationPreflight(preflightRequest) })
		request.kind, request.ctx, request.preflight = action.Kind, preflightRequest.ctx, preflightRequest
	}
	tester := ui.NewTester(func(c *ui.Context) {
		s.renderTaskCreationProgress(c, setTheme(c, false), progress)
	}, 408, 840)
	return s, request, tester
}

func applyTestTaskCreationProgress(s *app, request *taskCreationProgressRequest, service string, phase workspace.TaskCreationProgressPhase, status workspace.TaskCreationProgressStatus, message string) {
	event := workspace.TaskCreationProgress{Sequence: request.progress.lastSeq + 1, ServiceID: service, Phase: phase, Status: status, Message: message}
	if service != "" {
		event.ModuleID = "default"
	}
	s.applyTaskCreationProgressEvent(request, event)
}

func assertTaskCreationProgressText(t *testing.T, tester *ui.Tester, texts ...string) {
	t.Helper()
	tester.Frame()
	for _, text := range texts {
		if !tester.HasText(text) {
			t.Errorf("progress is missing %q", text)
		}
	}
}

func TestTaskCreationProgressKeepsParallelWorkspacePhases(t *testing.T) {
	s, request, tester := taskCreationProgressFixture(t, false)
	progress := request.progress
	emit := func(service string, phase workspace.TaskCreationProgressPhase, status workspace.TaskCreationProgressStatus) {
		applyTestTaskCreationProgress(s, request, service, phase, status, "")
	}
	emit("", workspace.TaskCreationPhaseProvisioningWorkspaces, workspace.TaskCreationProgressStarted)
	emit("alpha", workspace.TaskCreationPhasePreparingWorktree, workspace.TaskCreationProgressStarted)
	emit("alpha", workspace.TaskCreationPhaseRefreshingRemote, workspace.TaskCreationProgressStarted)
	emit("beta", workspace.TaskCreationPhaseCloningRepository, workspace.TaskCreationProgressStarted)
	assertTaskCreationProgressText(t, tester, "准备任务工作区", "已完成 0/3 个模块", "检查远程中", "克隆中")

	emit("beta", workspace.TaskCreationPhaseCloningRepository, workspace.TaskCreationProgressCompleted)
	emit("beta", workspace.TaskCreationPhaseInitializingWorkspace, workspace.TaskCreationProgressStarted)
	emit("alpha", workspace.TaskCreationPhaseRefreshingRemote, workspace.TaskCreationProgressCompleted)
	assertTaskCreationProgressText(t, tester, "准备任务工作区", "已完成 0/3 个模块", "准备中", "初始化中")
	emit("beta", workspace.TaskCreationPhaseInitializingWorkspace, workspace.TaskCreationProgressCompleted)
	assertTaskCreationProgressText(t, tester, "准备任务工作区", "已完成 1/3 个模块", "已准备", "准备中", "等待开始")

	s.applyTaskCreationProgressEvent(request, workspace.TaskCreationProgress{
		Sequence: progress.lastSeq, ServiceID: "alpha", ModuleID: "default",
		Phase: workspace.TaskCreationPhasePreparingWorktree, Status: workspace.TaskCreationProgressFailed,
		Message: "stale progress",
	})
	expired := *request
	expired.generation--
	applyTestTaskCreationProgress(s, &expired, "alpha", workspace.TaskCreationPhasePreparingWorktree, workspace.TaskCreationProgressFailed, "retired progress")
	assertTaskCreationProgressText(t, tester, "准备中", "已完成 1/3 个模块")
	if tester.HasText("失败") {
		t.Fatal("outdated events replaced a live module state")
	}
}

func TestTaskCreationProgressCountsChecksInPreflight(t *testing.T) {
	s, request, tester := taskCreationProgressFixture(t, true)
	applyTestTaskCreationProgress(s, request, "", workspace.TaskCreationPhaseChecking, workspace.TaskCreationProgressStarted, "")
	applyTestTaskCreationProgress(s, request, "alpha", workspace.TaskCreationPhaseChecking, workspace.TaskCreationProgressStarted, "")
	applyTestTaskCreationProgress(s, request, "alpha", workspace.TaskCreationPhaseRefreshingRemote, workspace.TaskCreationProgressStarted, "")
	applyTestTaskCreationProgress(s, request, "beta", workspace.TaskCreationPhaseChecking, workspace.TaskCreationProgressStarted, "")
	applyTestTaskCreationProgress(s, request, "alpha", workspace.TaskCreationPhaseRefreshingRemote, workspace.TaskCreationProgressCompleted, "")
	assertTaskCreationProgressText(t, tester, "预检进度", "检查配置与分支", "已完成 0/3 个模块", "检查中")
	applyTestTaskCreationProgress(s, request, "beta", workspace.TaskCreationPhaseChecking, workspace.TaskCreationProgressCompleted, "")
	assertTaskCreationProgressText(t, tester, "检查配置与分支", "已完成 1/3 个模块", "已检查", "检查中")
}

func TestTaskCreationProgressKeepsParallelCleanupResults(t *testing.T) {
	s, request, tester := taskCreationProgressFixture(t, false)
	applyTestTaskCreationProgress(s, request, "", workspace.TaskCreationPhaseProvisioningWorkspaces, workspace.TaskCreationProgressStarted, "")
	applyTestTaskCreationProgress(s, request, "alpha", workspace.TaskCreationPhaseInitializingWorkspace, workspace.TaskCreationProgressCompleted, "")
	applyTestTaskCreationProgress(s, request, "beta", workspace.TaskCreationPhaseCloningRepository, workspace.TaskCreationProgressFailed, "克隆失败")
	applyTestTaskCreationProgress(s, request, "", workspace.TaskCreationPhaseRollback, workspace.TaskCreationProgressStarted, "")
	applyTestTaskCreationProgress(s, request, "alpha", workspace.TaskCreationPhaseRollback, workspace.TaskCreationProgressStarted, "")
	survivor := t.TempDir()
	applyTestTaskCreationProgress(s, request, "alpha", workspace.TaskCreationPhaseRollback, workspace.TaskCreationProgressFailed, "请检查保留的工作区 "+survivor)
	applyTestTaskCreationProgress(s, request, "", workspace.TaskCreationPhaseRollback, workspace.TaskCreationProgressCompleted, "")
	s.failTaskCreationProgress(request.state, errors.New("任务创建失败"))
	assertTaskCreationProgressText(t, tester, "创建未完成，清理结果需检查", "已完成 0/3 个模块", "清理需检查", survivor, "失败", "未执行")
	if tester.HasText("已回滚") {
		t.Fatal("task rollback mislabeled a failed or unattempted module")
	}
}

func TestTaskCreationProgressShowsCancellationWhileCommandsExit(t *testing.T) {
	s, request, tester := taskCreationProgressFixture(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	request.ctx = ctx
	applyTestTaskCreationProgress(s, request, "", workspace.TaskCreationPhaseProvisioningWorkspaces, workspace.TaskCreationProgressStarted, "")
	applyTestTaskCreationProgress(s, request, "alpha", workspace.TaskCreationPhaseInitializingWorkspace, workspace.TaskCreationProgressCompleted, "")
	applyTestTaskCreationProgress(s, request, "beta", workspace.TaskCreationPhaseCloningRepository, workspace.TaskCreationProgressStarted, "")
	s.operationCancelling = true
	cancel()
	assertTaskCreationProgressText(t, tester, "正在中断", "等待开始")
	applyTestTaskCreationProgress(s, request, "beta", workspace.TaskCreationPhaseCloningRepository, workspace.TaskCreationProgressCancelled, "")
	applyTestTaskCreationProgress(s, request, "alpha", workspace.TaskCreationPhaseRollback, workspace.TaskCreationProgressCompleted, "")
	s.failTaskCreationProgress(request.state, context.Canceled)
	assertTaskCreationProgressText(t, tester, "已中断任务创建。", "已中断", "已回滚", "未执行")
	if tester.HasText("正在中断") || tester.HasText("创建未完成") {
		t.Fatal("finished cancellation still displays an active or failed workflow")
	}
}
