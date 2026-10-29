package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

type tagPendingReply struct {
	snapshot application.TagJobSnapshot
	err      error
}
type tagPendingCall struct {
	ctx    context.Context
	scope  application.TagJobScope
	action application.Action
	reply  chan tagPendingReply
}
type tagPendingBackend struct {
	*tagDesktopTestBackend
	calls chan tagPendingCall
}

func (b *tagPendingBackend) StartTagJob(ctx context.Context, scope application.TagJobScope, action application.Action) (application.TagJobSnapshot, error) {
	call := tagPendingCall{ctx: ctx, scope: scope, action: action, reply: make(chan tagPendingReply, 1)}
	b.calls <- call
	select {
	case reply := <-call.reply:
		if reply.err == nil {
			b.mu.Lock()
			b.snapshots[scope] = reply.snapshot
			b.mu.Unlock()
		}
		return reply.snapshot, reply.err
	case <-ctx.Done():
		return application.TagJobSnapshot{}, ctx.Err()
	}
}
func (b *tagPendingBackend) AppendTagJob(ctx context.Context, scope application.TagJobScope, _ string, action application.Action) (application.TagJobSnapshot, error) {
	return b.StartTagJob(ctx, scope, action)
}

func tagPendingFixture() (*app, *tagPendingBackend, *tagDesktopTestWindow) {
	s, base, window := tagDesktopTestApp()
	b := &tagPendingBackend{tagDesktopTestBackend: base, calls: make(chan tagPendingCall, 16)}
	s.backend = b
	s.snapshot.Groups = []application.Group{{ID: "group", Name: "fixture-group"}}
	for i := range s.taskUnit.state.tasks {
		s.taskUnit.state.tasks[i].groupID = "group"
		s.taskUnit.state.tasks[i].title = "fixture-task-" + s.taskUnit.state.tasks[i].id
		for _, module := range []string{"api", "job"} {
			target := "release/test"
			s.taskUnit.state.tasks[i].workspaces = append(s.taskUnit.state.tasks[i].workspaces, application.Workspace{ServiceID: "service", ModuleID: module, Name: module, Path: s.taskUnit.state.tasks[i].path + "/" + module, State: "READY", Record: domain.ServiceWorkspace{ServiceName: "fixture-service-" + module, ModuleName: "fixture-service-" + module, Branch: "feature/fixture", TagEnabled: true, TagMode: domain.TagBuildMergeToTargetBranch, TagTargetRef: &target}})
		}
	}
	return s, b, window
}

func awaitTagPendingCall(t *testing.T, w *tagDesktopTestWindow, b *tagPendingBackend) tagPendingCall {
	t.Helper()
	var call tagPendingCall
	w.until(t, func() bool {
		select {
		case call = <-b.calls:
			return true
		default:
			return false
		}
	})
	return call
}

func tagPendingSnapshot(call tagPendingCall, id string) application.TagJobSnapshot {
	snapshot := application.TagJobSnapshot{Scope: call.scope, ID: id, BatchID: id + "-batch", Sequence: 1, Busy: true}
	keys := tagJobSelectionKeys(call.action.Payload)
	if call.action.ServiceID != "" {
		keys = []string{call.action.ServiceID + ":" + call.action.ModuleID}
	}
	for i, key := range keys {
		parts := strings.SplitN(key, ":", 2)
		record := application.TagRecord{ID: fmt.Sprintf("%s-op-%d", id, i), TaskID: call.scope.TaskID, ServiceID: parts[0], ModuleID: parts[1], ServiceName: "fixture-service-" + parts[1], BatchID: snapshot.BatchID, State: "CREATED", SourceBranch: "feature/fixture", TargetBranch: "release/test", CreatedAt: "2026-10-15 12:00:00", Updated: "2026-10-15 12:00:00", GenbuStatus: domain.GenbuTagProbeStatus{Build: domain.GenbuUnknown, UAT: domain.GenbuUnknown, Production: domain.GenbuUnknown}}
		snapshot.Items = append(snapshot.Items, application.TagJobItem{ID: record.ID, Kind: "tag.build", SelectionKey: key, OperationID: record.ID, ServiceID: record.ServiceID, ModuleID: record.ModuleID, Record: &record, Phase: application.TagJobPhaseQueued, Queued: true})
	}
	return snapshot
}

func pendingSelectionCount(s *app, key string) int {
	view := s.currentTagJobView()
	if view == nil {
		return 0
	}
	count := 0
	for _, command := range view.commands {
		if command.pending != nil {
			for _, item := range visiblePendingTagItems(view, command.pending) {
				if item.selectionKey == key {
					count++
				}
			}
		}
	}
	for _, item := range view.snapshot.Items {
		if item.Kind == "tag.build" && item.SelectionKey == key && item.Record != nil {
			count++
		}
	}
	return count
}

func TestTagPendingSubmissionBlockedStartFirstFrame(t *testing.T) {
	for _, width := range []int{360, 1100} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, b, w := tagPendingFixture()
				defer tagDesktopClose(t, s, w)
				s.openTagBuildBatch(s.taskUnit.currentTask())
				for _, workspace := range s.releaseUnit.state.tagBuild.Workspaces {
					s.releaseUnit.state.tagBuild.Selected[tagBuildWorkspaceKey(workspace)] = true
				}
				tester := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, dark)
					ui.Column(c).Fill().Children(func() {
						if s.tab == taskTagTab {
							s.renderTaskTagHistory(c, p, s.taskUnit.currentTask())
						}
					})
					s.renderTagBuildDialog(c, p)
				}, width, 780)
				if err := tester.Click("开始构建"); err != nil {
					t.Fatal(err)
				}
				call := awaitTagPendingCall(t, w, b)
				if s.tab != taskTagTab || s.releaseUnit.state.tagBuild != nil || len(s.snapshot.TagHistory) != 0 {
					t.Fatal("click did not switch immediately, or fake operation records were inserted")
				}
				tester.Frame()
				view := s.currentTagJobView()
				nonce := view.commands[0].pending.Nonce
				if !tester.HasText("本次操作 2 项 · 准备中 2") || !tester.HasText("准备构建") || tester.HasText("没有构建记录") {
					t.Fatal("blocked Start first frame did not expose all selected items/count/phase")
				}
				for _, key := range []string{"service:api", "service:job"} {
					row, ok := tester.Find("Tag 准备条目 · " + nonce + ":" + key)
					if !ok || row.X < 0 || row.X+row.W > float32(width) || row.H <= 0 {
						t.Fatalf("selected pending row missing/clipped: %s %+v", key, row)
					}
				}
				for range 10 {
					if !s.queueTagActionWindow(call.action, w) {
						t.Fatal(s.lastError)
					}
				}
				if !s.queueTagActionWindow(application.Action{Kind: "tag.build", TaskID: call.scope.TaskID, Payload: map[string]any{"selectionKeys": []string{"service:job", "service:api"}}}, w) {
					t.Fatal(s.lastError)
				}
				if len(view.commands) != 1 || view.commands[0].pending.Nonce != nonce {
					t.Fatal("repeat click duplicated the pending submission")
				}
				select {
				case <-b.calls:
					t.Fatal("repeat click called Start again")
				default:
				}
				call.reply <- tagPendingReply{snapshot: tagPendingSnapshot(call, "server-job")}
				w.until(t, func() bool {
					tester.Frame()
					for _, key := range []string{"service:api", "service:job"} {
						if count := pendingSelectionCount(s, key); count != 1 {
							t.Fatalf("non-atomic admission for %s: %d visible items", key, count)
						}
					}
					return !view.commandRunning && len(s.snapshot.TagHistory) == 2
				})
				tester.Frame()
				if tester.HasText("准备构建") || tester.HasText("本次操作 2 项 · 准备中 2") || !tester.HasText("本次操作 2 项 · 执行中 2 · 完成 0 · 失败 0") {
					t.Fatal("server snapshot did not replace the pending view in one update")
				}
			})
		}
	}
}

func TestTagPendingSubmissionNonceFailureCancellationAndScope(t *testing.T) {
	s, b, w := tagPendingFixture()
	defer tagDesktopClose(t, s, w)
	action := func(taskID, key string) application.Action {
		return application.Action{Kind: "tag.build", TaskID: taskID, Payload: map[string]any{"selectionKeys": []string{key}}}
	}
	s.queueTagActionWindow(action("task-a", "service:api"), w)
	first := awaitTagPendingCall(t, w, b)
	s.queueTagActionWindow(action("task-a", "service:job"), w)
	view := s.currentTagJobView()
	firstNonce, secondNonce := view.commands[0].pending.Nonce, view.commands[1].pending.Nonce
	first.reply <- tagPendingReply{err: errors.New("fixture plan failure")}
	second := awaitTagPendingCall(t, w, b)
	if len(view.commands) != 1 || view.commands[0].pending.Nonce != secondNonce || firstNonce == secondNonce {
		t.Fatal("first failure retired another pending nonce")
	}
	second.reply <- tagPendingReply{err: context.Canceled}
	w.until(t, func() bool { return len(view.commands) == 0 })
	if hasPendingTagSubmission(view) {
		t.Fatal("canceled admission left a pending ghost")
	}
	s.queueTagActionWindow(action("task-a", "service:api"), w)
	old := awaitTagPendingCall(t, w, b)
	oldScope := s.tagJobScope()
	s.taskUnit.state.selected = 1
	s.queueTagActionWindow(action("task-b", "service:job"), w)
	current := awaitTagPendingCall(t, w, b)
	currentView := s.currentTagJobView()
	currentNonce := currentView.commands[0].pending.Nonce
	tester := tagLayoutTester(s, 1100, false)
	if tester.HasText("fixture-service-api") || !tester.HasText("fixture-service-job") || old.ctx.Err() != nil {
		t.Fatal("task switch leaked/canceled the old captured submission")
	}
	old.reply <- tagPendingReply{err: errors.New("fixture late old-scope failure")}
	w.until(t, func() bool { return len(s.releaseUnit.state.tagJobs.views[oldScope].commands) == 0 })
	if len(currentView.commands) != 1 || currentView.commands[0].pending.Nonce != currentNonce || s.ensureTagHistoryView().Error != "" {
		t.Fatal("old failure erased or contaminated new-scope pending state")
	}
	s.taskUnit.state.selected = 0
	tester.Frame()
	if tester.HasText("准备构建") {
		t.Fatal("returning to old task resurrected a retired pending view")
	}
	s.taskUnit.state.selected = 1
	current.reply <- tagPendingReply{snapshot: tagPendingSnapshot(current, "task-b-job")}
	w.until(t, func() bool { return len(currentView.commands) == 0 })
	for _, record := range s.snapshot.TagHistory {
		if record.TaskID != "task-b" {
			t.Fatal("late scope result leaked records")
		}
	}
}

func TestTagPendingSubmissionWatchBeforeAdmissionReply(t *testing.T) {
	s, b, w := tagPendingFixture()
	defer tagDesktopClose(t, s, w)
	s.tab = taskTagTab
	scope := s.tagJobScope()
	initial := tagPendingSnapshot(tagPendingCall{scope: scope, action: application.Action{Payload: map[string]any{"selectionKeys": []string{"service:api"}}}}, "existing-job")
	b.snapshots[scope] = initial
	s.acceptTagJobSnapshot(s.tagJobView(scope), scope, initial, true)
	s.observeTagJobsWindow(w)
	watch := tagDesktopAwaitWatch(t, w, b.tagDesktopTestBackend)
	s.queueTagActionWindow(application.Action{Kind: "tag.build", TaskID: scope.TaskID, Payload: map[string]any{"selectionKeys": []string{"service:job"}}}, w)
	call := awaitTagPendingCall(t, w, b)
	if pendingSelectionCount(s, "service:job") != 1 {
		t.Fatal("append pending row was not visible")
	}
	next := initial
	next.Sequence = 2
	admitted := tagPendingSnapshot(call, "appended-job").Items[0]
	admitted.Record.BatchID = initial.BatchID
	next.Items = append(append([]application.TagJobItem(nil), initial.Items...), admitted)
	go watch.emit(next)
	w.until(t, func() bool { return s.currentTagJobView().snapshot.Sequence == 2 })
	if pendingSelectionCount(s, "service:job") != 1 || len(visiblePendingTagItems(s.currentTagJobView(), s.currentTagJobView().commands[0].pending)) != 0 {
		t.Fatal("Watch and pending rendered the same admitted item twice")
	}
	call.reply <- tagPendingReply{snapshot: next}
	w.until(t, func() bool { return len(s.currentTagJobView().commands) == 0 })
	if len(s.snapshot.TagHistory) != 2 {
		t.Fatal("admission reply duplicated or dropped Watch records")
	}
}

func TestTagPendingSubmissionShutdownCancelsPlanning(t *testing.T) {
	s, b, w := tagPendingFixture()
	s.queueTagActionWindow(application.Action{Kind: "tag.build", TaskID: "task-a", Payload: map[string]any{"selectionKeys": []string{"service:api", "service:job"}}}, w)
	call := awaitTagPendingCall(t, w, b)
	view := s.currentTagJobView()
	cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.closeTagDesktopJobs(cleanup); err != nil {
		t.Fatal(err)
	}
	w.drain()
	s.releaseUnit.state.tagJobs.mu.Lock()
	remaining := len(s.releaseUnit.state.tagJobs.workers)
	s.releaseUnit.state.tagJobs.mu.Unlock()
	if call.ctx.Err() == nil || remaining != 0 || len(view.commands) != 0 || hasPendingTagSubmission(view) || !b.closed {
		t.Fatal("shutdown missed a planning-stage worker or left pending state")
	}
	if tester := tagLayoutTester(s, 1100, false); tester.HasText("准备构建") {
		t.Fatal("closed admission remained visible")
	}
}

func TestTagPendingSubmissionLateSuccessStaysInCapturedScope(t *testing.T) {
	s, b, w := tagPendingFixture()
	defer tagDesktopClose(t, s, w)
	build := func(taskID, key string) {
		if !s.queueTagActionWindow(application.Action{Kind: "tag.build", TaskID: taskID, Payload: map[string]any{"selectionKeys": []string{key}}}, w) {
			t.Fatal(s.lastError)
		}
	}
	build("task-a", "service:api")
	old := awaitTagPendingCall(t, w, b)
	oldView := s.currentTagJobView()
	s.taskUnit.state.selected = 1
	build("task-b", "service:job")
	current := awaitTagPendingCall(t, w, b)
	currentView := s.currentTagJobView()
	nonce := currentView.commands[0].pending.Nonce
	old.reply <- tagPendingReply{snapshot: tagPendingSnapshot(old, "late-task-a-job")}
	w.until(t, func() bool { return len(oldView.commands) == 0 && old.ctx.Err() != nil })
	if len(currentView.commands) != 1 || currentView.commands[0].pending.Nonce != nonce || len(s.taskTagHistoryRecords("task-b")) != 0 || old.ctx.Err() == nil {
		t.Fatal("late success affected new task admission or failed to release its old command lifetime")
	}
	s.taskUnit.state.selected = 0
	tester := tagLayoutTester(s, 1100, false)
	if tester.HasText("准备构建") || !tester.HasText("fixture-service-api") || len(s.taskTagHistoryRecords("task-a")) != 1 {
		t.Fatal("revisit restored a pending ghost instead of the actual accepted job")
	}
	s.taskUnit.state.selected = 1
	tester.Frame()
	if !tester.HasText("准备构建") || tester.HasText("fixture-service-api") {
		t.Fatal("captured-scope history leaked into the new task")
	}
	current.reply <- tagPendingReply{err: context.Canceled}
	w.until(t, func() bool { return len(currentView.commands) == 0 })
}
