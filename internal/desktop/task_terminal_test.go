package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

type fakeTaskTerminal struct {
	done     chan struct{}
	once     sync.Once
	closed   atomic.Int32
	code     atomic.Int32
	closeErr error
}

func (f *fakeTaskTerminal) Done() <-chan struct{} { return f.done }
func (f *fakeTaskTerminal) ExitCode() int         { return int(f.code.Load()) }
func (f *fakeTaskTerminal) Close() error {
	f.closed.Add(1)
	f.once.Do(func() { close(f.done) })
	return f.closeErr
}
func (f *fakeTaskTerminal) View(c *ui.Context) ui.Element {
	return ui.Box(c).Focusable().Label("测试终端控制台")
}
func (f *fakeTaskTerminal) exit(code int) {
	f.code.Store(int32(code))
	f.once.Do(func() { close(f.done) })
}

type fakeTerminalLaunch struct {
	root    string
	process *fakeTaskTerminal
}

func taskTerminalFixture(t *testing.T) (*app, chan fakeTerminalLaunch) {
	t.Helper()
	s := newApp(nil, Options{Page: "tasks", Tab: taskTerminalTab})
	s.snapshot.Home = t.TempDir()
	s.taskUnit.state.tasks = []task{{id: "a", path: t.TempDir(), createdAt: "first"}, {id: "b", path: t.TempDir(), createdAt: "second"}}
	launches := make(chan fakeTerminalLaunch, 32)
	s.taskTerminalUnit.factory = func(ctx context.Context, root string) (taskTerminalProcess, error) {
		f := &fakeTaskTerminal{done: make(chan struct{})}
		launches <- fakeTerminalLaunch{root, f}
		return f, nil
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.closeTaskTerminals(ctx); err != nil {
			t.Error(err)
		}
	})
	return s, launches
}

func awaitTaskTerminal(t *testing.T, s *app, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		s.drainTaskTerminalEvents()
		if ready() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("terminal state did not settle")
}

func TestTaskTerminalFirstEntryTabsAndCloseSelection(t *testing.T) {
	s, launches := taskTerminalFixture(t)
	o := s.ensureTaskTerminalsCurrent()
	if s.ensureTaskTerminalsCurrent() != o || len(o.sessions) != 1 {
		t.Fatal("first entry recreated session")
	}
	s.addTaskTerminal(o)
	s.addTaskTerminal(o)
	awaitTaskTerminal(t, s, func() bool { return !o.sessions[0].loading && !o.sessions[1].loading && !o.sessions[2].loading })
	if len(launches) != 3 || o.selected != o.sessions[2].id {
		t.Fatal("new tab did not select independent terminal")
	}
	first, middle, last := o.sessions[0], o.sessions[1], o.sessions[2]
	o.selected = middle.id
	s.closeTaskTerminal(o, middle.id)
	if o.selected != last.id {
		t.Fatal("closing active tab must select right neighbour")
	}
	s.closeTaskTerminal(o, last.id)
	if o.selected != first.id {
		t.Fatal("closing last tab must select left neighbour")
	}
	s.closeTaskTerminal(o, first.id)
	s.ensureTaskTerminalsCurrent()
	if len(o.sessions) != 0 || o.selected != 0 || len(launches) != 3 {
		t.Fatal("last close silently restarted shell")
	}
	s.addTaskTerminal(o)
	if o.sessions[0].number != 4 {
		t.Fatal("new tab reused retired identity")
	}
}

func TestTaskTerminalTaskSwitchRenameAndRootChangePreserveProcesses(t *testing.T) {
	s, _ := taskTerminalFixture(t)
	a := s.ensureTaskTerminalsCurrent()
	awaitTaskTerminal(t, s, func() bool { return a.sessions[0].process != nil })
	pa := a.sessions[0].process.(*fakeTaskTerminal)
	s.taskUnit.state.selected = 1
	b := s.ensureTaskTerminalsCurrent()
	awaitTaskTerminal(t, s, func() bool { return b.sessions[0].process != nil })
	s.taskUnit.state.selected = 0
	if s.ensureTaskTerminalsCurrent() != a || pa.closed.Load() != 0 {
		t.Fatal("switch terminated original shell")
	}
	s.taskUnit.state.tasks[0].title = "renamed"
	newRoot := t.TempDir()
	s.taskUnit.state.tasks[0].path = newRoot
	if s.ensureTaskTerminalsCurrent() != a {
		t.Fatal("rename/root change discarded live sessions")
	}
	s.addTaskTerminal(a)
	awaitTaskTerminal(t, s, func() bool { return a.sessions[1].process != nil })
	if a.sessions[0].directory == newRoot || a.sessions[1].directory != newRoot {
		t.Fatal("root change altered old cwd or missed new cwd")
	}
	s.page = "settings"
	s.taskTerminalUnit.current = nil
	if s.ensureTaskTerminalsCurrent() != nil || pa.closed.Load() != 0 {
		t.Fatal("leaving page terminated shell")
	}
	s.page = "tasks"
	if s.ensureTaskTerminalsCurrent() != a || !a.focus {
		t.Fatal("return did not restore terminal")
	}
	s.taskUnit.state.tasks[0].createdAt = "replacement"
	if s.ensureTaskTerminalsCurrent() == a {
		t.Fatal("new task incarnation inherited old session")
	}
}

func TestTaskTerminalLateCreationAfterCloseIsDisposed(t *testing.T) {
	s, _ := taskTerminalFixture(t)
	release := make(chan struct{})
	started := make(chan struct{})
	f := &fakeTaskTerminal{done: make(chan struct{})}
	s.taskTerminalUnit.factory = func(context.Context, string) (taskTerminalProcess, error) { close(started); <-release; return f, nil }
	o := s.ensureTaskTerminalsCurrent()
	<-started
	s.closeTaskTerminal(o, o.sessions[0].id)
	close(release)
	awaitTaskTerminal(t, s, func() bool { return f.closed.Load() == 1 })
	if len(o.sessions) != 0 {
		t.Fatal("late creation resurrected closed tab")
	}
}

func TestTaskTerminalBackgroundCompletionAndNaturalExit(t *testing.T) {
	s, _ := taskTerminalFixture(t)
	release := make(chan struct{})
	f := &fakeTaskTerminal{done: make(chan struct{})}
	s.taskTerminalUnit.factory = func(ctx context.Context, root string) (taskTerminalProcess, error) {
		if root == s.taskUnit.state.tasks[0].path {
			<-release
			return f, nil
		}
		return &fakeTaskTerminal{done: make(chan struct{})}, nil
	}
	a := s.ensureTaskTerminalsCurrent()
	s.taskUnit.state.selected = 1
	b := s.ensureTaskTerminalsCurrent()
	close(release)
	awaitTaskTerminal(t, s, func() bool { return a.sessions[0].process != nil && b.sessions[0].process != nil })
	if a.sessions[0].process != f || b.sessions[0].process == f {
		t.Fatal("background result crossed task boundary")
	}
	f.exit(7)
	awaitTaskTerminal(t, s, func() bool { return a.sessions[0].exited })
	if a.sessions[0].exitCode != 7 || a.sessions[0].process != f || f.closed.Load() != 0 {
		t.Fatal("natural exit discarded final output")
	}
	s.closeTaskTerminal(a, a.sessions[0].id)
	awaitTaskTerminal(t, s, func() bool { return f.closed.Load() == 1 })
}

func TestTaskTerminalFailureRetryAndCanceledGeneration(t *testing.T) {
	s, _ := taskTerminalFixture(t)
	s.taskTerminalUnit.factory = func(context.Context, string) (taskTerminalProcess, error) { return nil, errors.New("isolated failure") }
	o := s.ensureTaskTerminalsCurrent()
	session := o.sessions[0]
	awaitTaskTerminal(t, s, func() bool { return !session.loading })
	if session.error != "isolated failure" {
		t.Fatal("failure reason disappeared")
	}
	s.taskTerminalUnit.factory = func(context.Context, string) (taskTerminalProcess, error) {
		return &fakeTaskTerminal{done: make(chan struct{})}, nil
	}
	oldGeneration := session.generation
	s.startTaskTerminal(o, session)
	s.taskTerminalUnit.publish(context.Background(), taskTerminalEvent{owner: o, session: session, generation: oldGeneration, err: errors.New("late old error")}, nil)
	awaitTaskTerminal(t, s, func() bool { return session.process != nil })
	if session.error != "" {
		t.Fatal("retry overwritten by previous attempt")
	}
}

func TestTaskTerminalDeleteWaitsOnlyForTargetTask(t *testing.T) {
	s, _ := taskTerminalFixture(t)
	a := s.ensureTaskTerminalsCurrent()
	awaitTaskTerminal(t, s, func() bool { return a.sessions[0].process != nil })
	pa := a.sessions[0].process.(*fakeTaskTerminal)
	s.taskUnit.state.selected = 1
	b := s.ensureTaskTerminalsCurrent()
	awaitTaskTerminal(t, s, func() bool { return b.sessions[0].process != nil })
	pb := b.sessions[0].process.(*fakeTaskTerminal)
	barrier := s.prepareTaskTerminalDelete("a")
	s.addTaskTerminal(a)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := barrier(ctx); err != nil {
		t.Fatal(err)
	}
	if len(a.sessions) != 0 || pa.closed.Load() != 1 || pb.closed.Load() != 0 {
		t.Fatal("task deletion missed cleanup or closed another task")
	}
}

func TestTaskTerminalShutdownReleasesLateCreation(t *testing.T) {
	s, _ := taskTerminalFixture(t)
	release := make(chan struct{})
	started := make(chan struct{})
	f := &fakeTaskTerminal{done: make(chan struct{})}
	s.taskTerminalUnit.factory = func(context.Context, string) (taskTerminalProcess, error) { close(started); <-release; return f, nil }
	s.ensureTaskTerminalsCurrent()
	<-started
	s.stopTaskTerminals()
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.closeTaskTerminals(ctx); err != nil {
		t.Fatal(err)
	}
	s.drainTaskTerminalEvents()
	if f.closed.Load() != 1 || s.ensureTaskTerminalsCurrent() != nil {
		t.Fatal("shutdown leaked or resurrected terminal")
	}
}

func TestTaskTerminalDirectoryNeverFallsBackToHome(t *testing.T) {
	root := filepath.Join(t.TempDir(), "任务 root 😀")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if dir, err := taskTerminalDirectory(root); err != nil || dir != root {
		t.Fatal(dir, err)
	}
	file := filepath.Join(root, "file")
	os.WriteFile(file, []byte("x"), 0o644)
	for _, bad := range []string{"", filepath.Join(root, "missing"), file} {
		if dir, err := taskTerminalDirectory(bad); err == nil || dir != "" {
			t.Fatal("invalid root silently changed directory", bad, dir, err)
		}
	}
}

func TestTaskTerminalButtonsAndCompactTabLayout(t *testing.T) {
	for _, dark := range []bool{false, true} {
		s, _ := taskTerminalFixture(t)
		o := s.ensureTaskTerminalsCurrent()
		awaitTaskTerminal(t, s, func() bool { return o.sessions[0].process != nil })
		tester := ui.NewTester(func(c *ui.Context) { s.renderTaskTerminals(c, setTheme(c, dark)) }, 500, 320)
		tester.Click("新建终端")
		if len(o.sessions) != 2 || o.selected != o.sessions[1].id {
			t.Fatal("plus did not create/select tab")
		}
		awaitTaskTerminal(t, s, func() bool { return o.sessions[1].process != nil })
		tester.Frame()
		tester.Click("切换到终端 1")
		if o.selected != o.sessions[0].id {
			t.Fatal("cannot select first terminal")
		}
		tester.Click("关闭终端 1")
		if len(o.sessions) != 1 || o.sessions[0].number != 2 {
			t.Fatal("close action affected wrong tab")
		}
		tester.Click("关闭终端 2")
		if len(o.sessions) != 0 {
			t.Fatal("last tab not closed")
		}
		for i := 0; i < 10; i++ {
			s.addTaskTerminal(o)
		}
		tester.Frame()
		if o.scroll.MaxX <= 0 {
			t.Fatal("many tabs must scroll without squeezing console")
		}
		if bounds, ok := tester.Find("新建终端"); !ok || bounds.X+bounds.W > 500 || bounds.Y < 280 {
			t.Fatal("plus must remain visible in fixed bottom bar", bounds)
		}
	}
}

func TestTaskTerminalEntryAndTaskSwitchDoNotLaunchMaterialsOrGit(t *testing.T) {
	s, backend, _ := taskSelectionGitFixture(t)
	s.taskTerminalUnit.factory = func(context.Context, string) (taskTerminalProcess, error) {
		return &fakeTaskTerminal{done: make(chan struct{})}, nil
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.closeTaskTerminals(ctx); err != nil {
			t.Error(err)
		}
	})
	if !s.selectTaskContentTab(taskTerminalTab) {
		t.Fatal("terminal tab unavailable")
	}
	s.refreshWorkspaceTaskSelection(true)
	if !s.selectTaskIndex(1) {
		t.Fatal("cannot select another task")
	}
	s.refreshWorkspaceTaskSelection(true)
	if s.materialsUnit.state.documentsRequested || s.workspaceUnit.state.workspaceGit != nil || s.taskServicesUnit.current != nil {
		t.Fatal("terminal entry started another feature")
	}
	select {
	case read := <-backend.reads:
		t.Fatal("terminal started backend operation", read.action.Kind)
	default:
	}
}

func TestTaskTerminalRuntimeLookupIsOffline(t *testing.T) {
	for _, pair := range [][2]string{{"windows", "ghostty-vt.dll"}, {"darwin", "libghostty-vt.dylib"}, {"linux", "libghostty-vt.so"}} {
		resources := t.TempDir()
		if err := taskTerminalRuntimeAvailable(resources, "", "", pair[0]); err == nil {
			t.Fatal("missing runtime accepted")
		}
		file := filepath.Join(resources, pair[1])
		if err := os.WriteFile(file, []byte("isolated lookup fixture"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := taskTerminalRuntimeAvailable(resources, "", "", pair[0]); err != nil {
			t.Fatal(err)
		}
		if err := taskTerminalRuntimeAvailable(resources, "", filepath.Join(resources, "missing-override"), pair[0]); err == nil {
			t.Fatal("invalid override silently selected another library")
		}
		if err := taskTerminalRuntimeAvailable("", "", resources, pair[0]); err == nil {
			t.Fatal("directory accepted as runtime file")
		}
	}
}

type fakeLateTerminalClose struct{ *fakeTaskTerminal }

func (p *fakeLateTerminalClose) Close() error {
	p.closed.Add(1)
	return errors.New("close timed out before process settled")
}

func TestTaskTerminalCloseErrorAllowsDeleteAfterProcessSettles(t *testing.T) {
	s, _ := taskTerminalFixture(t)
	p := &fakeLateTerminalClose{&fakeTaskTerminal{done: make(chan struct{})}}
	s.taskTerminalUnit.factory = func(context.Context, string) (taskTerminalProcess, error) { return p, nil }
	o := s.ensureTaskTerminalsCurrent()
	awaitTaskTerminal(t, s, func() bool { return o.sessions[0].process != nil })
	s.closeTaskTerminal(o, o.sessions[0].id)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := waitTaskTerminals(ctx, &o.wg); err != nil {
		t.Fatal(err)
	}
	if err := s.prepareTaskTerminalDelete("a")(ctx); err == nil {
		t.Fatal("unresolved process cleanup must block deletion")
	}
	p.exit(0)
	if err := s.prepareTaskTerminalDelete("a")(ctx); err != nil {
		t.Fatal("settled historical close error blocked retry", err)
	}
}

func TestTaskTerminalDeferredSpawnFailureRetriesSameTab(t *testing.T) {
	s, _ := taskTerminalFixture(t)
	o := s.ensureTaskTerminalsCurrent()
	awaitTaskTerminal(t, s, func() bool { return o.sessions[0].process != nil })
	session := o.sessions[0]
	failed := session.process.(*fakeTaskTerminal)
	failed.exit(-1)
	awaitTaskTerminal(t, s, func() bool { return session.exited })
	if !session.failedStart() || session.process != failed {
		t.Fatal("spawn failure must retain console output")
	}
	tester := ui.NewTester(func(c *ui.Context) { s.renderTaskTerminals(c, setTheme(c, false)) }, 600, 400)
	tester.Click("重试启动终端")
	if len(o.sessions) != 1 || o.sessions[0] != session {
		t.Fatal("retry must preserve tab identity")
	}
	awaitTaskTerminal(t, s, func() bool { return session.process != nil && session.process != failed })
	if session.failedStart() {
		t.Fatal("retry retained old failure state")
	}
	session.process.(*fakeTaskTerminal).exit(3)
	awaitTaskTerminal(t, s, func() bool { return session.exited })
	if session.failedStart() {
		t.Fatal("ordinary nonzero exit classified as spawn failure")
	}
}
