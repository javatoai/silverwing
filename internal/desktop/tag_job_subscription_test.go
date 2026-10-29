package desktop

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

// The window queue is pumped only by the test thread, just like MyGO's UI
// queue. Backends never get permission to mutate app state directly.
type tagDesktopTestWindow struct{ updates chan func() }

func newTagDesktopTestWindow() *tagDesktopTestWindow {
	return &tagDesktopTestWindow{updates: make(chan func(), 128)}
}
func (w *tagDesktopTestWindow) Update(fn func()) { w.updates <- fn }
func (*tagDesktopTestWindow) IsDestroyed() bool  { return false }
func (w *tagDesktopTestWindow) until(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for !ready() {
		select {
		case fn := <-w.updates:
			fn()
		case <-tick.C:
		case <-deadline:
			t.Fatal("UI update timed out")
		}
	}
}
func (w *tagDesktopTestWindow) drain() {
	for {
		select {
		case fn := <-w.updates:
			fn()
		default:
			return
		}
	}
}

func tagDesktopAwaitWatch(t *testing.T, w *tagDesktopTestWindow, b *tagDesktopTestBackend) tagDesktopTestWatch {
	t.Helper()
	var watch tagDesktopTestWatch
	w.until(t, func() bool {
		select {
		case watch = <-b.watches:
			return true
		default:
			return false
		}
	})
	return watch
}

type tagDesktopTestWatch struct {
	ctx   context.Context
	scope application.TagJobScope
	emit  func(application.TagJobSnapshot)
}
type tagDesktopTestBackend struct {
	mu        sync.Mutex
	snapshots map[application.TagJobScope]application.TagJobSnapshot
	start     application.TagJobSnapshot
	actions   chan application.Action
	watches   chan tagDesktopTestWatch
	appends   int
	cancels   int
	closed    bool
}

func newTagDesktopTestBackend() *tagDesktopTestBackend {
	return &tagDesktopTestBackend{snapshots: map[application.TagJobScope]application.TagJobSnapshot{}, actions: make(chan application.Action, 16), watches: make(chan tagDesktopTestWatch, 16)}
}
func (*tagDesktopTestBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, errors.New("unexpected full snapshot")
}
func (*tagDesktopTestBackend) Dispatch(context.Context, application.Action) (application.ActionResult, error) {
	return application.ActionResult{}, errors.New("Tag used global dispatch")
}
func (b *tagDesktopTestBackend) StartTagJob(_ context.Context, scope application.TagJobScope, action application.Action) (application.TagJobSnapshot, error) {
	b.mu.Lock()
	snapshot := b.start
	snapshot.Scope = scope
	b.snapshots[scope] = snapshot
	b.mu.Unlock()
	b.actions <- action
	return snapshot, nil
}
func (b *tagDesktopTestBackend) AppendTagJob(_ context.Context, scope application.TagJobScope, id string, action application.Action) (application.TagJobSnapshot, error) {
	b.mu.Lock()
	snapshot := b.snapshots[scope]
	b.appends++
	snapshot.Sequence++
	if action.Kind == "tag.build" {
		record := application.TagRecord{ID: "three", TaskID: scope.TaskID, ServiceID: "service-free", ModuleID: "module", BatchID: snapshot.BatchID, State: "CREATED"}
		snapshot.Items = append(append([]application.TagJobItem(nil), snapshot.Items...), application.TagJobItem{ID: "three", Kind: "tag.build", OperationID: "three", ServiceID: record.ServiceID, ModuleID: record.ModuleID, Phase: application.TagJobPhaseQueued, Queued: true, Record: &record})
	}
	b.snapshots[scope] = snapshot
	b.mu.Unlock()
	if id != snapshot.ID {
		return snapshot, errors.New("wrong job append")
	}
	b.actions <- action
	return snapshot, nil
}
func (b *tagDesktopTestBackend) TagJobSnapshot(_ context.Context, scope application.TagJobScope) (application.TagJobSnapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.snapshots[scope], nil
}
func (b *tagDesktopTestBackend) WatchTagJob(ctx context.Context, scope application.TagJobScope, _ string, _ uint64, emit func(application.TagJobSnapshot)) error {
	b.watches <- tagDesktopTestWatch{ctx, scope, func(next application.TagJobSnapshot) {
		b.mu.Lock()
		current := b.snapshots[scope]
		if next.Scope == scope && next.ID == current.ID && next.Sequence > current.Sequence {
			b.snapshots[scope] = next
		}
		b.mu.Unlock()
		emit(next)
	}}
	<-ctx.Done()
	return ctx.Err()
}
func (b *tagDesktopTestBackend) CancelTagJob(context.Context, application.TagJobScope, string) error {
	b.mu.Lock()
	b.cancels++
	b.mu.Unlock()
	return nil
}
func (b *tagDesktopTestBackend) CloseTagJobs(context.Context) error {
	b.mu.Lock()
	b.closed = true
	b.mu.Unlock()
	return nil
}

func tagDesktopTestApp() (*app, *tagDesktopTestBackend, *tagDesktopTestWindow) {
	b := newTagDesktopTestBackend()
	w := newTagDesktopTestWindow()
	config := `{"taskRoot":"C:/fixture/tasks","tagEnabled":true}`
	s := &app{
		taskUnit:     taskController{state: taskState{tasks: []task{{id: "task-a", path: "C:/fixture/tasks/task-a", createdAt: "2026-10-15T10:00:00Z"}, {id: "task-b", path: "C:/fixture/tasks/task-b", createdAt: "2026-10-15T10:01:00Z"}}}},
		settingsUnit: settingsController{state: settingsState{configJSON: config}},
		backend:      b,
		page:         "tasks",
		tab:          0,
		inputs:       map[string]*string{},
		snapshot:     application.Snapshot{Home: "C:/fixture/home", ConfigJSON: config},
	}
	s.ensureTagDesktopJobs().window = w
	return s, b, w
}
func tagDesktopTestSnapshot(scope application.TagJobScope) application.TagJobSnapshot {
	snapshot := application.TagJobSnapshot{Scope: scope, ID: "job-a", BatchID: "batch-a", Sequence: 1, Busy: true}
	for _, id := range []string{"one", "two"} {
		record := application.TagRecord{ID: id, TaskID: scope.TaskID, ServiceID: "service-" + id, ModuleID: "module", BatchID: "batch-a", CreatedAt: scope.TaskCreatedAt, State: "CREATED", Updated: scope.TaskCreatedAt}
		snapshot.Items = append(snapshot.Items, application.TagJobItem{ID: id, Kind: "tag.build", OperationID: id, ServiceID: record.ServiceID, ModuleID: record.ModuleID, WorkspacePath: "C:/fixture/" + id, Phase: application.TagJobPhaseQueued, Queued: true, Record: &record})
	}
	return snapshot
}
func tagDesktopClose(t *testing.T, s *app, w *tagDesktopTestWindow) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.closeTagDesktopJobs(ctx); err != nil {
		t.Fatal(err)
	}
	w.drain()
}

func TestTagDesktopJobStartIndependentAllPlaceholders(t *testing.T) {
	s, b, w := tagDesktopTestApp()
	defer tagDesktopClose(t, s, w)
	s.busy = true
	s.operationKind = "task.refresh"
	b.start = tagDesktopTestSnapshot(s.tagJobScope())
	if !s.queueTagActionWindow(application.Action{Kind: "tag.build", TaskID: "task-a", Payload: map[string]any{"selectionKeys": []string{"service-one:module", "service-two:module"}}}, w) {
		t.Fatal(s.lastError)
	}
	w.until(t, func() bool { return s.tab == taskTagTab && len(s.snapshot.TagHistory) == 2 })
	if !s.busy || s.operationKind != "task.refresh" {
		t.Fatal("Tag borrowed global busy")
	}
	progress := s.tagBatchProgress("batch-a")
	if progress.Total != 2 || progress.Running != 2 || !progress.Busy {
		t.Fatalf("first accepted frame missing items: %+v", progress)
	}
	if !s.consumeTagBatchFocus("batch-a") || s.consumeTagBatchFocus("batch-a") {
		t.Fatal("batch focus must be consumed once")
	}
	watch := tagDesktopAwaitWatch(t, w, b)
	next := tagDesktopTestSnapshot(watch.scope)
	next.Sequence = 2
	next.Items[0].Phase = application.TagJobPhaseCompleted
	next.Items[0].Queued = false
	next.Items[0].Done = true
	next.Items[0].Record.State = "SUCCESS"
	next.Items[0].Record.Tag = "complete-tag"
	go watch.emit(next)
	w.until(t, func() bool { return s.tagBatchProgress("batch-a").Completed == 1 })
	if s.snapshot.TagHistory[0].ID != "one" || s.snapshot.TagHistory[1].ID != "two" || s.tagRecordPhase(s.snapshot.TagHistory[1]) != "等待执行" {
		t.Fatal("partial update changed stable item identity")
	}
	checked := "2026-10-15T10:03:00Z"
	s.snapshot.TagHistory[0].GenbuStatus = domain.GenbuTagProbeStatus{Build: domain.GenbuBuilding, CheckedAt: &checked}
	newer := next
	newer.Sequence = 3
	newer.Items = append([]application.TagJobItem(nil), next.Items...)
	go watch.emit(newer)
	w.until(t, func() bool { return s.currentTagJobView().snapshot.Sequence == 3 })
	if s.snapshot.TagHistory[0].GenbuStatus.Build != domain.GenbuBuilding {
		t.Fatal("local phase overwrote Genbu state")
	}
	stale := next
	stale.Sequence = 1
	done := make(chan struct{})
	go func() { watch.emit(stale); close(done) }()
	w.until(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	})
	if s.currentTagJobView().snapshot.Sequence != 3 {
		t.Fatal("stale sequence applied")
	}
	if !s.queueTagActionWindow(application.Action{Kind: "tag.retry", TaskID: "task-a", ServiceID: "service-one", ModuleID: "module", Payload: map[string]any{"operationId": "one"}}, w) {
		t.Fatal(s.lastError)
	}
	w.until(t, func() bool { return !s.currentTagJobView().commandRunning })
	b.mu.Lock()
	appends := b.appends
	b.mu.Unlock()
	if appends != 1 {
		t.Fatalf("retry did not use existing job queue: %d", appends)
	}
	if !s.queueTagActionWindow(application.Action{Kind: "tag.build", TaskID: "task-a", Payload: map[string]any{"selectionKeys": []string{"service-free:module"}, "batch": true}}, w) {
		t.Fatal(s.lastError)
	}
	w.until(t, func() bool { return !s.currentTagJobView().commandRunning })
	b.mu.Lock()
	appends = b.appends
	b.mu.Unlock()
	if appends != 2 {
		t.Fatal("new build bypassed active job append admission")
	}
	if progress := s.tagBatchProgress("batch-a"); progress.Total != 3 || progress.Completed != 1 || progress.Running != 2 {
		t.Fatalf("append did not grow the stable batch: %+v", progress)
	}
}

func TestTagDesktopJobSwitchRevisitAndIdentity(t *testing.T) {
	s, b, w := tagDesktopTestApp()
	defer tagDesktopClose(t, s, w)
	s.tab = taskTagTab
	scope := s.tagJobScope()
	initial := tagDesktopTestSnapshot(scope)
	b.snapshots[scope] = initial
	s.observeTagJobsWindow(w)
	w.until(t, func() bool { return len(s.snapshot.TagHistory) == 2 })
	old := tagDesktopAwaitWatch(t, w, b)
	s.taskUnit.state.selected = 1
	s.observeTagJobsWindow(w)
	if old.ctx.Err() == nil {
		t.Fatal("old subscription not detached")
	}
	late := initial
	late.Sequence = 9
	late.Items[0].Record.Tag = "late-a"
	old.emit(late)
	if s.taskUnit.currentTask().id != "task-b" || len(s.taskTagHistoryRecords("task-b")) != 0 {
		t.Fatal("late result contaminated other task")
	}
	updated := tagDesktopTestSnapshot(scope)
	updated.Sequence = 4
	updated.Items[0].Record.Tag = "restored-a"
	b.mu.Lock()
	b.snapshots[scope] = updated
	b.mu.Unlock()
	s.taskUnit.state.selected = 0
	s.observeTagJobsWindow(w)
	w.until(t, func() bool { return s.currentTagJobView() != nil && s.currentTagJobView().snapshot.Sequence == 4 })
	if s.snapshot.TagHistory[0].Tag != "restored-a" {
		t.Fatal("revisit failed to restore latest job")
	}
	current := s.currentTagJobView()
	foreign := updated
	foreign.Sequence = 5
	foreign.Scope.Home = "C:/another-home"
	if s.acceptTagJobSnapshot(current, scope, foreign, false) {
		t.Fatal("foreign home accepted")
	}
	foreign = updated
	foreign.Sequence = 5
	foreign.Scope.AccountIdentity = "another-account"
	if s.acceptTagJobSnapshot(current, scope, foreign, false) {
		t.Fatal("foreign account accepted")
	}
	if s.queueTagActionWindow(application.Action{Kind: "tag.retry", TaskID: scope.TaskID, Payload: map[string]any{"tagHistoryTaskPath": "C:/other-task"}}, w) {
		t.Fatal("stale action scope was rewritten instead of rejected")
	}
	s.snapshot.TagHistory = nil
	s.restoreActiveTagJobRecords()
	if len(s.snapshot.TagHistory) != 2 {
		t.Fatal("local refresh erased queued placeholders")
	}
	b.mu.Lock()
	cancels := b.cancels
	b.mu.Unlock()
	if cancels != 0 {
		t.Fatal("navigation canceled backend job")
	}
}

func TestTagDesktopJobRepositoryBusyAndSnapshotValidation(t *testing.T) {
	s, _, w := tagDesktopTestApp()
	defer tagDesktopClose(t, s, w)
	scope := s.tagJobScope()
	snapshot := tagDesktopTestSnapshot(scope)
	s.taskUnit.state.tasks[0].workspaces = []application.Workspace{{ServiceID: "service-one", ModuleID: "module", Path: "C:/fixture/shared"}, {ServiceID: "service-three", ModuleID: "module", Path: "C:/fixture/shared"}, {ServiceID: "service-free", ModuleID: "module", Path: "C:/fixture/free"}}
	snapshot.Items[0].WorkspacePath = "C:/fixture/shared"
	view := s.tagJobView(scope)
	s.acceptTagJobSnapshot(view, scope, snapshot, true)
	if !s.tagWorkspaceBusy(s.taskUnit.state.tasks[0], s.taskUnit.state.tasks[0].workspaces[1]) || s.tagWorkspaceBusy(s.taskUnit.state.tasks[0], s.taskUnit.state.tasks[0].workspaces[2]) {
		t.Fatal("repository busy leaked or was not shared")
	}
	bad := tagDesktopTestSnapshot(scope)
	bad.Sequence = 2
	bad.Items[0].Record.ServiceID = "other-owner"
	s.acceptTagJobSnapshot(view, scope, bad, false)
	if len(s.snapshot.TagHistory) != 2 || s.snapshot.TagHistory[0].ServiceID != "service-one" {
		t.Fatal("record owner mismatch inserted duplicate")
	}
}
