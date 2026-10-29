package desktop

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

type taskSelectionGitReply struct {
	result application.ActionResult
	err    error
}

type taskSelectionGitRead struct {
	action application.Action
	ctx    context.Context
	reply  chan taskSelectionGitReply
}

type taskSelectionGitBackend struct {
	reads chan taskSelectionGitRead
	done  chan struct{}
}

func (b *taskSelectionGitBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, nil
}

func (b *taskSelectionGitBackend) Dispatch(ctx context.Context, action application.Action) (application.ActionResult, error) {
	read := taskSelectionGitRead{action: action, ctx: ctx, reply: make(chan taskSelectionGitReply, 1)}
	select {
	case b.reads <- read:
	case <-b.done:
		return application.ActionResult{}, context.Canceled
	}
	// Deliberately allow a slow backend to finish after cancellation.
	select {
	case reply := <-read.reply:
		return reply.result, reply.err
	case <-b.done:
		return application.ActionResult{}, context.Canceled
	}
}

func taskSelectionGitFixture(t *testing.T) (*app, *taskSelectionGitBackend, *ui.Tester) {
	t.Helper()
	backend := &taskSelectionGitBackend{reads: make(chan taskSelectionGitRead, 16), done: make(chan struct{})}
	t.Cleanup(func() { close(backend.done) })
	s := newApp(backend, Options{CapturePath: "isolated-task-selection"})
	s.win, s.loading, s.page, s.tab = &mygo.Window{}, false, "tasks", 3
	s.snapshot.Home = filepath.Join(t.TempDir(), "home")
	s.settingsUnit.state.configJSON = `{}`
	for _, id := range []string{"task-a", "task-b"} {
		s.taskUnit.state.tasks = append(s.taskUnit.state.tasks, task{id: id, title: id, path: filepath.Join(t.TempDir(), id), createdAt: id + "-created",
			workspaces: []application.Workspace{{ID: id + "-service", Path: filepath.Join(t.TempDir(), "workspace"), Branch: "feature/example"}}})
	}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { s.taskIndex(c, setTheme(c, s.dark)) })
	}, 340, 400)
	return s, backend, tester
}

func taskSelectionGitWaitRead(t *testing.T, backend *taskSelectionGitBackend) taskSelectionGitRead {
	t.Helper()
	select {
	case read := <-backend.reads:
		if read.action.Kind != "workspace.status" || read.action.ServiceID != "" || read.action.ModuleID != "" {
			t.Fatalf("selection must only read all task workspaces: %+v", read.action)
		}
		return read
	case <-time.After(2 * time.Second):
		t.Fatal("task row did not query Git status")
		return taskSelectionGitRead{}
	}
}

func TestTaskSelectionGitRefreshKeepsCurrentReadingOnRepeatedRowClick(t *testing.T) {
	s, backend, tester := taskSelectionGitFixture(t)
	s.materialsUnit.state.documentPath, s.materialsUnit.state.documentContent, s.materialsUnit.state.documentDirty = "fixture.md", "unsaved fixture", true
	s.materialsUnit.state.documentDrafts["fixture.md"], s.materialsUnit.state.documentDirtyMap["fixture.md"] = "unsaved fixture", true
	scroll := s.taskWorkspaceDetailScroll(s.taskUnit.currentTask())
	if err := tester.Click("task-a"); err != nil {
		t.Fatal(err)
	}
	read := taskSelectionGitWaitRead(t, backend)
	if read.action.TaskID != "task-a" || s.tab != 3 || s.taskUnit.currentTask().id != "task-a" ||
		s.materialsUnit.state.documentPath != "fixture.md" || s.materialsUnit.state.documentContent != "unsaved fixture" || !s.materialsUnit.state.documentDirty ||
		s.materialsUnit.state.documentDrafts["fixture.md"] != "unsaved fixture" || !s.materialsUnit.state.documentDirtyMap["fixture.md"] ||
		s.taskWorkspaceDetailScroll(s.taskUnit.currentTask()) != scroll {
		t.Fatal("reclicking the selected task reset its tab, reading state or unsaved draft")
	}
}

func taskSelectionGitClick(t *testing.T, tester *ui.Tester, id string) {
	t.Helper()
	if err := tester.Click(id); err != nil {
		t.Fatal(err)
	}
}

func taskSelectionGitResult(s *app, count int) application.ActionResult {
	return application.ActionResult{Data: map[string]domain.WorkspaceGitHealth{
		s.taskUnit.currentTask().workspaces[0].Path: {State: domain.WorkspaceGitReady, DirtyFileCount: count, PushState: domain.LocalPushPushed},
	}}
}

// The headless UI does not run a native window loop. Deliver the real worker's
// reply at the same UI completion boundary that Window.Update uses in the app.
func taskSelectionGitFinish(s *app, read taskSelectionGitRead, reply taskSelectionGitReply) {
	state, request := s.workspaceUnit.state.workspaceGit, s.workspaceUnit.state.workspaceGit.current
	read.reply <- reply
	s.finishWorkspaceGitStatus(state, request, reply.result, reply.err)
}

func taskSelectionGitAssertHealth(t *testing.T, s *app, count int) {
	t.Helper()
	health, ok := s.workspaceHealth(s.taskUnit.currentTask().workspaces[0].Path)
	if !ok || health.State != domain.WorkspaceGitReady || health.DirtyFileCount != count {
		t.Fatalf("latest source observation not displayed: %+v / %t", health, ok)
	}
	card := ui.NewTester(func(c *ui.Context) {
		current := s.taskUnit.currentTask()
		ui.Column(c).Fill().Children(func() { s.workspaceCard(c, setTheme(c, s.dark), current, current.workspaces[0], 0) })
	}, 1200, 500)
	if !card.HasText(fmt.Sprintf("本地未提交 %d", count)) {
		t.Fatal("current task's native Git status card did not render the latest query result")
	}
}

func TestTaskSelectionGitRefreshReadsAgainAfterCompletedObservation(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, width := range []int{340, 960} {
			s, backend, tester := taskSelectionGitFixture(t)
			s.dark, s.taskUnit.state.selected = dark, -1
			tester.SetSize(width, 400)
			taskSelectionGitClick(t, tester, "task-a")
			first := taskSelectionGitWaitRead(t, backend)
			taskSelectionGitFinish(s, first, taskSelectionGitReply{result: taskSelectionGitResult(s, 1)})
			taskSelectionGitAssertHealth(t, s, 1)
			// Ordinary frames retain the observation; a user click must expire it.
			s.requestWorkspaceGitStatus(false)
			if s.workspaceUnit.state.workspaceGit.current != nil {
				t.Fatal("ordinary rendering queried again without a refresh intent")
			}
			taskSelectionGitClick(t, tester, "task-a")
			second := taskSelectionGitWaitRead(t, backend)
			taskSelectionGitFinish(s, second, taskSelectionGitReply{result: taskSelectionGitResult(s, 4)})
			taskSelectionGitAssertHealth(t, s, 4)
		}
	}
}

func TestTaskSelectionGitRefreshCoalescesBusyRowClicks(t *testing.T) {
	s, backend, tester := taskSelectionGitFixture(t)
	s.busy = true // An unrelated feature's busy presentation must not drop reads.
	taskSelectionGitClick(t, tester, "task-a")
	first := taskSelectionGitWaitRead(t, backend)
	request := s.workspaceUnit.state.workspaceGit.current
	for i := 0; i < 20; i++ {
		taskSelectionGitClick(t, tester, "task-a")
	}
	if s.workspaceUnit.state.workspaceGit.current != request || !s.workspaceUnit.state.workspaceGit.pending || first.ctx.Err() != nil {
		t.Fatal("busy clicks should retain one read and one latest refresh intent")
	}
	select {
	case extra := <-backend.reads:
		t.Fatalf("clicks fanned out backend requests: %+v", extra.action)
	default:
	}
	taskSelectionGitFinish(s, first, taskSelectionGitReply{result: taskSelectionGitResult(s, 1)})
	second := taskSelectionGitWaitRead(t, backend)
	if _, ok := s.workspaceUnit.state.workspaceGit.health[workspaceHealthPathKey(s.taskUnit.currentTask().workspaces[0].Path)]; ok {
		t.Fatal("superseded observation flashed before the pending refresh")
	}
	taskSelectionGitFinish(s, second, taskSelectionGitReply{result: taskSelectionGitResult(s, 2)})
	taskSelectionGitAssertHealth(t, s, 2)
	if s.workspaceUnit.state.workspaceGit.loading || s.workspaceUnit.state.workspaceGit.pending || s.workspaceUnit.state.workspaceGit.current != nil || !s.busy {
		t.Fatal("coalesced completion left a request active or changed another feature's busy state")
	}
}

func TestTaskSelectionGitRefreshSwitchRejectsSlowPreviousTask(t *testing.T) {
	s, backend, tester := taskSelectionGitFixture(t)
	taskSelectionGitClick(t, tester, "task-a")
	first := taskSelectionGitWaitRead(t, backend)
	oldState, oldRequest := s.workspaceUnit.state.workspaceGit, s.workspaceUnit.state.workspaceGit.current
	oldResult := taskSelectionGitResult(s, 99)
	taskSelectionGitClick(t, tester, "task-b")
	if first.ctx.Err() == nil || s.taskUnit.currentTask().id != "task-b" || s.tab != 3 {
		t.Fatal("task switch did not cancel the old read while retaining the detail tab")
	}
	taskSelectionGitFinish(s, first, taskSelectionGitReply{result: oldResult})
	second := taskSelectionGitWaitRead(t, backend)
	if second.action.TaskID != "task-b" || len(s.workspaceUnit.state.workspaceGit.health) != 0 {
		t.Fatal("slow task A result entered task B")
	}
	taskSelectionGitFinish(s, second, taskSelectionGitReply{result: taskSelectionGitResult(s, 2)})
	s.finishWorkspaceGitStatus(oldState, oldRequest, oldResult, nil)
	taskSelectionGitAssertHealth(t, s, 2)
}

func TestTaskSelectionGitRefreshRejectsChangedContext(t *testing.T) {
	for _, change := range []struct {
		name  string
		apply func(*app)
	}{
		{"home", func(s *app) { s.snapshot.Home = filepath.Join(s.snapshot.Home, "another-home") }},
		{"task-root", func(s *app) { s.settingsUnit.state.configJSON = `{"taskRoot":"C:/synthetic/another-root"}` }},
		{"git-executable", func(s *app) { s.settingsUnit.state.configJSON = `{"gitExecutablePath":"C:/synthetic/git.exe"}` }},
		{"account", func(s *app) {
			s.settingsUnit.state.settingsCLIStatus = map[string]map[string]any{"meegle": {"accountId": "synthetic-account-b"}}
		}},
		{"task-identity", func(s *app) { s.taskUnit.state.tasks[s.taskUnit.state.selected].createdAt = "replacement-created" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			s, backend, tester := taskSelectionGitFixture(t)
			taskSelectionGitClick(t, tester, "task-a")
			first := taskSelectionGitWaitRead(t, backend)
			oldResult := taskSelectionGitResult(s, 99)
			change.apply(s)
			taskSelectionGitFinish(s, first, taskSelectionGitReply{result: oldResult})
			if len(s.workspaceUnit.state.workspaceGit.health) != 0 || s.workspaceUnit.state.workspaceGit.taskKey != "" {
				t.Fatal("late observation was accepted in a changed context")
			}
			s.requestWorkspaceGitStatus(false)
			second := taskSelectionGitWaitRead(t, backend)
			taskSelectionGitFinish(s, second, taskSelectionGitReply{result: taskSelectionGitResult(s, 3)})
			taskSelectionGitAssertHealth(t, s, 3)
		})
	}
}

func TestTaskSelectionGitRefreshFailureCanRetryFromSameRow(t *testing.T) {
	s, backend, tester := taskSelectionGitFixture(t)
	taskSelectionGitClick(t, tester, "task-a")
	first := taskSelectionGitWaitRead(t, backend)
	taskSelectionGitFinish(s, first, taskSelectionGitReply{err: errors.New("synthetic inspection failure")})
	health, ok := s.workspaceHealth(s.taskUnit.currentTask().workspaces[0].Path)
	if !ok || health.State != domain.WorkspaceGitFailed || health.Message == nil || *health.Message != "synthetic inspection failure" {
		t.Fatalf("failed read was not visible: %+v", health)
	}
	s.requestWorkspaceGitStatus(false)
	if s.workspaceUnit.state.workspaceGit.current != nil {
		t.Fatal("failed read retried on every frame")
	}
	taskSelectionGitClick(t, tester, "task-a")
	second := taskSelectionGitWaitRead(t, backend)
	taskSelectionGitFinish(s, second, taskSelectionGitReply{result: taskSelectionGitResult(s, 5)})
	taskSelectionGitAssertHealth(t, s, 5)
}

func TestTaskSelectionGitRefreshDefersAndKeepsLatestIntentDuringSnapshotLoading(t *testing.T) {
	s, backend, tester := taskSelectionGitFixture(t)
	s.taskUnit.state.selected = -1
	s.loading = true
	taskSelectionGitClick(t, tester, "task-a")
	state := s.workspaceUnit.state.workspaceGit
	if state == nil || !state.afterLoad || state.queuedKey != workspaceHealthTaskKey(s.taskUnit.currentTask()) || !s.workspaceGitStatusBusy() {
		t.Fatal("row activation during initial loading was not retained as a deferred intent")
	}
	select {
	case read := <-backend.reads:
		t.Fatalf("status read started before the snapshot settled: %+v", read.action)
	default:
	}

	// A later row click replaces the queued target; the old selection must not
	// be read once loading completes.
	taskSelectionGitClick(t, tester, "task-b")
	if state.queuedKey != workspaceHealthTaskKey(s.taskUnit.currentTask()) || s.taskUnit.currentTask().id != "task-b" {
		t.Fatal("loading-time row activation did not retain the latest task selection")
	}
	s.loading = false
	s.requestWorkspaceGitStatus(false) // the normal visible-page read drains the queued intent
	read := taskSelectionGitWaitRead(t, backend)
	if read.action.TaskID != "task-b" || state.afterLoad {
		t.Fatalf("loading completion did not refresh the latest selected task: action=%+v state=%+v", read.action, state)
	}
	taskSelectionGitFinish(s, read, taskSelectionGitReply{result: taskSelectionGitResult(s, 2)})
	taskSelectionGitAssertHealth(t, s, 2)
}

func TestTaskSelectionGitRefreshClickDuringLoadingRunsAfterCurrentRead(t *testing.T) {
	s, backend, tester := taskSelectionGitFixture(t)
	taskSelectionGitClick(t, tester, "task-a")
	first := taskSelectionGitWaitRead(t, backend)
	s.loading = true
	taskSelectionGitClick(t, tester, "task-a")
	if !s.workspaceUnit.state.workspaceGit.afterLoad || s.workspaceUnit.state.workspaceGit.current == nil || s.workspaceUnit.state.workspaceGit.current.key != workspaceHealthTaskKey(s.taskUnit.currentTask()) {
		t.Fatal("explicit refresh during snapshot loading was dropped or replaced the in-flight read")
	}
	taskSelectionGitFinish(s, first, taskSelectionGitReply{result: taskSelectionGitResult(s, 1)})
	if s.workspaceUnit.state.workspaceGit.current != nil || !s.workspaceUnit.state.workspaceGit.afterLoad {
		t.Fatal("completed read should leave the loading-time refresh queued until snapshot loading ends")
	}
	select {
	case read := <-backend.reads:
		t.Fatalf("queued refresh overlapped the active loading phase: %+v", read.action)
	default:
	}
	s.loading = false
	s.requestWorkspaceGitStatus(false)
	second := taskSelectionGitWaitRead(t, backend)
	if second.action.TaskID != "task-a" {
		t.Fatalf("deferred refresh targeted the wrong task: %+v", second.action)
	}
	taskSelectionGitFinish(s, second, taskSelectionGitReply{result: taskSelectionGitResult(s, 2)})
	taskSelectionGitAssertHealth(t, s, 2)
}

func TestTaskSelectionGitRefreshDropsRemovedTaskIntentAfterLoading(t *testing.T) {
	s, backend, tester := taskSelectionGitFixture(t)
	s.loading = true
	taskSelectionGitClick(t, tester, "task-a")
	state := s.workspaceUnit.state.workspaceGit
	if !state.afterLoad {
		t.Fatal("test setup did not queue the selected task refresh")
	}
	// Emulate a settled snapshot which removed the clicked task and selected
	// another task that already has a current observation.
	s.taskUnit.state.tasks = []task{s.taskUnit.state.tasks[1]}
	s.taskUnit.state.selected = 0
	scope := s.workspaceGitStatusScope()
	key := workspaceHealthTaskKey(s.taskUnit.currentTask())
	state.scope, state.taskKey = scope, key
	s.loading = false
	s.requestWorkspaceGitStatus(false)
	if state.afterLoad || state.current != nil || state.taskKey != key {
		t.Fatal("removed-task click was applied to a different current task")
	}
	select {
	case read := <-backend.reads:
		t.Fatalf("removed task's stale refresh unexpectedly dispatched: %+v", read.action)
	default:
	}
}

func TestTaskSelectionGitRefreshQueuedForSameTaskUsesOnlyCurrentScope(t *testing.T) {
	for _, change := range []struct {
		name  string
		apply func(*app)
	}{
		{"account", func(s *app) {
			s.settingsUnit.state.settingsCLIStatus = map[string]map[string]any{"meegle": {"accountId": "synthetic-account-after-load"}}
		}},
		{"task-root", func(s *app) {
			s.settingsUnit.state.configJSON = `{"taskRoot":"C:/synthetic/task-root-after-load"}`
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			s, backend, tester := taskSelectionGitFixture(t)
			state := s.ensureWorkspaceGitState()
			oldScope := s.workspaceGitStatusScope()
			taskKey := workspaceHealthTaskKey(s.taskUnit.currentTask())
			path := s.taskUnit.currentTask().workspaces[0].Path
			state.scope = oldScope
			state.taskKey = taskKey
			state.health[path] = domain.WorkspaceGitHealth{State: domain.WorkspaceGitReady, DirtyFileCount: 99}

			s.loading = true
			taskSelectionGitClick(t, tester, "task-a") // activate the already-selected row
			if !state.afterLoad || state.queuedKey != taskKey || state.queuedScope != oldScope {
				t.Fatal("same-task row activation did not queue its refresh")
			}
			queuedScope := state.queuedScope
			change.apply(s)
			currentScope := s.workspaceGitStatusScope()
			if currentScope == queuedScope {
				t.Fatal("test context did not change the queued request scope")
			}

			s.loading = false
			s.requestWorkspaceGitStatus(false)
			if state.afterLoad || len(state.health) != 0 {
				t.Fatal("scope change retained the queued intent or stale health")
			}
			read := taskSelectionGitWaitRead(t, backend)
			request := state.current
			if request == nil || request.action.TaskID != "task-a" || request.key != taskKey || request.scope != currentScope {
				t.Fatalf("settled refresh retained the stale account/config scope: request=%+v current=%+v", request, currentScope)
			}
			taskSelectionGitFinish(s, read, taskSelectionGitReply{result: taskSelectionGitResult(s, 2)})
			taskSelectionGitAssertHealth(t, s, 2)
		})
	}
}

func TestTaskSelectionGitRefreshToolbarShowsBusySpinner(t *testing.T) {
	s, _, _ := taskSelectionGitFixture(t)
	s.workspaceUnit.state.workspaceGit = &workspaceGitState{loading: true, health: map[string]domain.WorkspaceGitHealth{}}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { s.taskDetails(c, setTheme(c, false)) })
	}, 1100, 700)
	if !tester.HasText("刷新中") {
		t.Fatal("the task toolbar did not show the Git refresh busy indicator")
	}
}
