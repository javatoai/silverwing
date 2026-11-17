package desktop

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

type archiveBatchDeleteTestBackend struct {
	mu       sync.Mutex
	actions  []application.Action
	preview  func(application.Action) (application.ActionResult, error)
	snapshot application.Snapshot
}

func archiveBatchPayloadBool(payload map[string]any, key string) bool {
	value, _ := payload[key].(bool)
	return value
}

func (b *archiveBatchDeleteTestBackend) Dispatch(_ context.Context, action application.Action) (application.ActionResult, error) {
	b.mu.Lock()
	b.actions = append(b.actions, action)
	b.mu.Unlock()
	if b.preview != nil {
		return b.preview(action)
	}
	return application.ActionResult{}, errors.New("unexpected action")
}

func (b *archiveBatchDeleteTestBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return b.snapshot, nil
}

func (b *archiveBatchDeleteTestBackend) recordedActions() []application.Action {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]application.Action(nil), b.actions...)
}

func archiveBatchDeleteFixture() (*app, *archiveBatchDeleteTestBackend) {
	config := `{"taskRoot":"D:\\tasks"}`
	backend := &archiveBatchDeleteTestBackend{snapshot: application.Snapshot{Home: "home"}}
	s := newApp(backend, Options{CapturePath: "isolated-archive-batch"})
	s.loading = false
	s.page = "archive"
	s.settingsUnit.state.configJSON = config
	s.snapshot.Home = "home"
	s.backendGate = make(chan struct{}, 1)
	s.taskUnit.state.taskContextUpdate = func(update func()) { update() }
	s.taskUnit.state.tasks = []task{
		{id: "archive-a", title: "归档任务 A", path: `D:\tasks\archive-a`, createdAt: "created-a", archived: true},
		{id: "archive-b", title: "归档任务 B", path: `D:\tasks\archive-b`, createdAt: "created-b", archived: true},
	}
	return s, backend
}

func TestArchiveBatchDeletePreviewsArchivedTasksWithCapturedScopes(t *testing.T) {
	s, backend := archiveBatchDeleteFixture()
	backend.preview = func(action application.Action) (application.ActionResult, error) {
		if action.Kind != "task.delete-risks" {
			t.Fatalf("preview dispatched %q", action.Kind)
		}
		if action.TaskID == "" || action.Path == "" || payloadString(action.Payload, "taskDeleteCreatedAt") == "" {
			t.Fatalf("preview omitted stable task identity: %+v", action)
		}
		if _, ok := action.Payload[applicationTaskContextPayloadKey].(map[string]any); !ok {
			t.Fatalf("preview omitted private task scope: %+v", action.Payload)
		}
		return application.ActionResult{Data: workspace.TaskDeletePreview{
			Materials: workspace.TaskMaterialsDeletePreview{Root: `D:\requirements\2026\REQ-` + action.TaskID, CanDelete: true, Guard: "materials-" + action.TaskID},
		}}, nil
	}

	s.openArchiveBatchDelete([]task{s.taskUnit.state.tasks[0], s.taskUnit.state.tasks[1], s.taskUnit.state.tasks[0]})
	state := s.taskUnit.state.archiveBatchDelete
	if state == nil || !state.Open || !state.PreviewLoading || len(state.Items) != 2 {
		t.Fatalf("dialog did not capture and deduplicate archived items: %+v", state)
	}
	job := state.PreviewJob
	select {
	case <-job.done:
	case <-time.After(2 * time.Second):
		t.Fatal("archive preview worker did not finish")
	}
	s.observeArchiveBatchDelete()

	if state.PreviewLoading || len(backend.recordedActions()) != 2 {
		t.Fatalf("preview did not complete once per task: loading=%v actions=%d", state.PreviewLoading, len(backend.recordedActions()))
	}
	for _, item := range state.Items {
		if !item.Previewed || item.PreviewError != "" || !item.Preview.Materials.CanDelete {
			t.Fatalf("valid scoped preview was not applied: %+v", item)
		}
	}
}

func TestArchiveBatchDeleteForceAndMaterialApprovalsArePerItem(t *testing.T) {
	knownRisk := domain.DeleteRisk{ServiceName: "api-service", Unstaged: true, UnpushedCommits: 1}
	checkFailure := "仓库配置不存在"
	force := archiveBatchDeleteItem{
		Task:      task{id: "task-force", path: `D:\tasks\task-force`, createdAt: "created-force", archived: true},
		Previewed: true,
		Preview: workspace.TaskDeletePreview{
			Risks: []domain.DeleteRisk{{ServiceName: "api-service", StatusCheckError: &checkFailure}},
			Force: workspace.TaskForceDeletePreview{CanForce: true, Guard: "force-guard", TaskPath: `D:\tasks\task-force`, Risks: []domain.DeleteRisk{knownRisk}},
		},
	}
	if archiveBatchItemCanDelete(force) {
		t.Fatal("force preview must require an explicit per-task approval")
	}
	force.ForceConfirmed = true
	if !archiveBatchItemCanDelete(force) {
		t.Fatal("one explicit force approval should cover the verified force risks")
	}
	action := archiveBatchDeleteAction(force, "request-1")
	if payloadString(action.Payload, "forceGuard") != "force-guard" || archiveBatchPayloadBool(action.Payload, "forceDiscard") {
		t.Fatalf("force approval should use the guard without ordinary discard authorization: %+v", action.Payload)
	}

	ordinary := archiveBatchDeleteItem{Task: task{id: "task-normal", path: `D:\tasks\task-normal`, createdAt: "created-normal", archived: true},
		Previewed: true, Preview: workspace.TaskDeletePreview{Risks: []domain.DeleteRisk{knownRisk}}}
	if archiveBatchItemCanDelete(ordinary) {
		t.Fatal("ordinary worktree changes require an explicit discard acknowledgement")
	}
	ordinary.DiscardChanges = true
	if !archiveBatchItemCanDelete(ordinary) || !archiveBatchPayloadBool(archiveBatchDeleteAction(ordinary, "request-2").Payload, "forceDiscard") {
		t.Fatal("ordinary discard acknowledgement was not transferred to the delete request")
	}

	unsafeMaterials := archiveBatchDeleteItem{Task: ordinary.Task, Previewed: true, DeleteMaterials: true,
		Preview: workspace.TaskDeletePreview{Materials: workspace.TaskMaterialsDeletePreview{CanDelete: true, Root: `D:\shared\REQ`, Guard: ""}}}
	if archiveBatchItemCanDelete(unsafeMaterials) {
		t.Fatal("materials deletion without the preview guard must be blocked")
	}
}

func TestArchiveBatchDeleteCompletionContinuesAfterFailureAndStopsOnUnknownRefresh(t *testing.T) {
	s, _ := archiveBatchDeleteFixture()
	first, second := s.taskUnit.state.tasks[0], s.taskUnit.state.tasks[1]
	state := &archiveBatchDeleteState{Open: true, Running: true, Started: true,
		Scope: archiveBatchDeleteScope{home: s.snapshot.Home, page: s.page, taskRoot: s.configString("taskRoot"), configJSON: s.settingsUnit.state.configJSON},
		Items: []archiveBatchDeleteItem{
			{Task: first, Status: "正在移入回收站"},
			{Task: second, Status: "等待回收"},
		},
	}
	state.Queue = []string{archiveBatchTaskKey(first), archiveBatchTaskKey(second)}
	s.taskUnit.state.archiveBatchDelete = state
	s.completeArchiveBatchDeleteItem(state, archiveBatchTaskKey(first), application.ActionResult{}, errors.New("回收失败"), nil)
	if state.StopRequested || !state.AdvancePending || state.Items[0].Status != "失败：回收失败" {
		t.Fatalf("an isolated item failure should be recorded and allow later items: %+v", state)
	}

	state.CurrentKey = archiveBatchTaskKey(second)
	state.Items[1].Status = "正在移入回收站"
	s.completeArchiveBatchDeleteItem(state, archiveBatchTaskKey(second), application.ActionResult{Message: "任务已移入回收站"}, nil, errors.New("snapshot unavailable"))
	if !state.StopRequested || state.Error == "" || state.Items[1].Status != "结果待核实：刷新归档失败" {
		t.Fatalf("an unknown refreshed state must stop later tasks and remain explicit: %+v", state)
	}
	if archiveBatchPayloadBool(archiveBatchDeleteAction(archiveBatchDeleteItem{Task: first, Previewed: true, Preview: workspace.TaskDeletePreview{Force: workspace.TaskForceDeletePreview{CanForce: true, Guard: "g"}}}, "force").Payload, "forceDiscard") {
		t.Fatal("force preview must not silently opt into ordinary ForceDiscard")
	}
}

func TestArchiveBatchDeleteStopAfterCurrentKeepsReceiptAndSkipsRemaining(t *testing.T) {
	s, _ := archiveBatchDeleteFixture()
	first, second := s.taskUnit.state.tasks[0], s.taskUnit.state.tasks[1]
	firstKey, secondKey := archiveBatchTaskKey(first), archiveBatchTaskKey(second)
	selection := s.taskIndexBrowsingFor(true)
	selection.archiveBatchSelected = map[string]task{firstKey: first, secondKey: second}
	state := &archiveBatchDeleteState{
		Open: true, Running: true, Started: true, ExternalWindowsClosed: true,
		Scope: archiveBatchDeleteScope{home: s.snapshot.Home, page: s.page, taskRoot: s.configString("taskRoot"), configJSON: s.settingsUnit.state.configJSON},
		Items: []archiveBatchDeleteItem{
			{Task: first, Status: "正在移入回收站", Previewed: true},
			{Task: second, Status: "等待回收", Previewed: true},
		},
		Queue: []string{firstKey, secondKey}, QueuePosition: 1, CurrentKey: firstKey,
	}
	s.taskUnit.state.archiveBatchDelete = state

	s.stopArchiveBatchDeleteQueue()
	s.completeArchiveBatchDeleteItem(state, firstKey, application.ActionResult{
		Message: application.TaskDeleteCleanupWarningMessage + "：登记清理待处理",
		Text:    "仓库登记稍后清理",
	}, nil, nil)
	if state.Items[0].Status != "已回收，工作区登记待清理" || state.Items[0].ResultText != "仓库登记稍后清理" {
		t.Fatalf("a warning receipt suffix must remain a successful partial outcome: %+v", state.Items[0])
	}
	if _, stillSelected := selection.archiveBatchSelected[firstKey]; stillSelected {
		t.Fatal("a task confirmed as recovered should be removed from the current batch selection")
	}

	s.advanceArchiveBatchDelete()
	if state.Running || state.Items[1].Status != "已跳过：已按要求停止后续任务。" {
		t.Fatalf("stop-after-current did not skip the remaining queue item: %+v", state)
	}
	if s.archiveBatchDeleteConfirmationEnabled(state) {
		t.Fatal("a started batch must not become confirmable again")
	}
}

func TestDecodeArchiveBatchDeletePreviewRejectsMissingData(t *testing.T) {
	if _, err := decodeArchiveBatchDeletePreview(nil); err == nil {
		t.Fatal("missing preview data must not be treated as a clean workspace")
	}
}

func TestArchiveBatchSamePathNormalizesSeparatorsAndWindowsCase(t *testing.T) {
	if !archiveBatchSamePath(`D:\tasks\sample\`, `d:\TASKS\sample`) {
		t.Fatal("equivalent Windows task paths should pass the force-preview identity check")
	}
	if archiveBatchSamePath(`D:\tasks\sample`, `D:\tasks\replacement`) {
		t.Fatal("different task directories must not share a force-preview identity")
	}
}
