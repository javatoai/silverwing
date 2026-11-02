package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

// A process belongs to a task incarnation, not a page or mutable task index.
// Only the UI thread changes owners/sessions. Workers deliver through mailbox.
type taskTerminalProcess interface {
	Close() error
	Done() <-chan struct{}
	ExitCode() int
	View(*ui.Context) ui.Element
}

type nativeTaskTerminal struct{ *terminal.Terminal }

func (t *nativeTaskTerminal) View(c *ui.Context) ui.Element { return terminal.View(c, t.Terminal) }

func (t *nativeTaskTerminal) Close() error {
	err := t.Terminal.Close()
	select {
	case <-t.Terminal.Done():
		return err
	case <-time.After(5 * time.Second):
		return errors.Join(err, fmt.Errorf("等待终端进程退出超时"))
	}
}

type taskTerminalFactory func(context.Context, string) (taskTerminalProcess, error)

type taskTerminalIdentity struct{ home, id, createdAt string }

type taskTerminalSession struct {
	id, generation uint64
	number         int
	directory      string
	ctx            context.Context
	cancel         context.CancelFunc
	process        taskTerminalProcess
	loading        bool
	error          string
	exited         bool
	exitCode       int
}

func (s *taskTerminalSession) failedStart() bool {
	return s.error != "" || (s.exited && s.exitCode == -1)
}

type taskTerminalCloseError struct {
	err  error
	done <-chan struct{}
}

type taskTerminalOwner struct {
	identity    taskTerminalIdentity
	root        string
	sessions    []*taskTerminalSession
	selected    uint64
	next        int
	focus       bool
	scroll      ui.ScrollState
	wg          sync.WaitGroup
	closeErrors []taskTerminalCloseError // protected by controller.mu
}

type taskTerminalEvent struct {
	owner      *taskTerminalOwner
	session    *taskTerminalSession
	generation uint64
	process    taskTerminalProcess
	err        error
	exited     bool
	exitCode   int
}

type taskTerminalController struct {
	owners   map[taskTerminalIdentity]*taskTerminalOwner
	current  *taskTerminalOwner
	deleting map[string]bool
	factory  taskTerminalFactory
	sequence uint64
	stopped  bool
	wg       sync.WaitGroup

	mu            sync.Mutex
	mailbox       []taskTerminalEvent
	mailboxClosed bool
	closeErrors   []taskTerminalCloseError
}

func newTaskTerminal(ctx context.Context, root string) (taskTerminalProcess, error) {
	directory, err := taskTerminalDirectory(root)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	resources, _ := mygo.App.Path(mygo.PathResources)
	executable, _ := os.Executable()
	if err := taskTerminalRuntimeAvailable(resources, executable, os.Getenv("MYGO_GHOSTTY_VT"), runtime.GOOS); err != nil {
		return nil, err
	}
	t, err := terminal.New(terminal.Options{Dir: directory})
	if err != nil {
		return nil, fmt.Errorf("启动终端：%w", err)
	}
	return &nativeTaskTerminal{t}, nil
}

// Our packages stage the library explicitly. Do not let a missing install turn
// into the SDK's two-minute development download or cache a failed Load forever.
func taskTerminalRuntimeAvailable(resources, executable, override, platform string) error {
	name := map[string]string{"windows": "ghostty-vt.dll", "darwin": "libghostty-vt.dylib", "linux": "libghostty-vt.so"}[platform]
	var candidates []string
	if override != "" {
		candidates = append(candidates, override)
	} else if name != "" {
		if resources != "" {
			candidates = append(candidates, filepath.Join(resources, name))
		}
		if executable != "" {
			candidates = append(candidates, filepath.Join(filepath.Dir(executable), name))
		}
	}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return nil
		}
	}
	return fmt.Errorf("终端组件缺失，请修复应用安装后重试")
}

func taskTerminalDirectory(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", fmt.Errorf("任务根目录尚未设置")
	}
	directory, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("解析任务根目录：%w", err)
	}
	f, err := os.Open(directory)
	if err != nil {
		return "", fmt.Errorf("无法访问任务根目录：%w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", fmt.Errorf("读取任务根目录：%w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("任务根目录不是文件夹：%s", directory)
	}
	return directory, nil
}

func (s *app) taskTerminalPageActive() bool {
	return (s.page == "tasks" || s.page == "archive") && s.tab == taskTerminalTab && !s.workspaceReviewPageVisible()
}

func (s *app) ensureTaskTerminalsCurrent() *taskTerminalOwner {
	u := &s.taskTerminalUnit
	if u.stopped || !s.taskTerminalPageActive() {
		return nil
	}
	t := s.taskUnit.currentTask()
	if t.id == "" {
		return nil
	}
	identity := taskTerminalIdentity{s.snapshot.Home, t.id, t.createdAt}
	if u.owners == nil {
		u.owners = make(map[taskTerminalIdentity]*taskTerminalOwner)
	}
	owner := u.owners[identity]
	if owner == nil {
		owner = &taskTerminalOwner{identity: identity, root: t.path}
		u.owners[identity] = owner
		s.addTaskTerminal(owner)
	}
	owner.root = t.path
	if u.current != owner {
		u.current, owner.focus = owner, true
	}
	return owner
}

func (s *app) taskTerminalInvalidate() func() {
	if win := s.win; win != nil {
		return win.Invalidate
	}
	return nil
}

func (s *app) addTaskTerminal(owner *taskTerminalOwner) {
	u := &s.taskTerminalUnit
	if owner == nil || u.stopped || u.deleting[owner.identity.id] || u.owners[owner.identity] != owner {
		return
	}
	u.sequence++
	owner.next++
	session := &taskTerminalSession{id: u.sequence, number: owner.next}
	owner.sessions = append(owner.sessions, session)
	owner.selected, owner.focus = session.id, true
	s.startTaskTerminal(owner, session)
}

func (s *app) startTaskTerminal(owner *taskTerminalOwner, session *taskTerminalSession) {
	u := &s.taskTerminalUnit
	if u.stopped || u.deleting[owner.identity.id] || u.owners[owner.identity] != owner {
		return
	}
	if session.cancel != nil {
		session.cancel()
	}
	session.generation++
	session.directory = owner.root
	session.ctx, session.cancel = context.WithCancel(context.Background())
	session.process, session.error, session.loading, session.exited = nil, "", true, false
	ctx, generation, directory := session.ctx, session.generation, session.directory
	factory := u.factory
	if factory == nil {
		factory = newTaskTerminal
	}
	invalidate := s.taskTerminalInvalidate()
	u.wg.Add(1)
	owner.wg.Add(1)
	go func() {
		defer u.wg.Done()
		defer owner.wg.Done()
		process, err := factory(ctx, directory)
		if process != nil {
			defer func() {
				if closeErr := process.Close(); closeErr != nil {
					failure := taskTerminalCloseError{err: closeErr, done: process.Done()}
					u.mu.Lock()
					owner.closeErrors = append(owner.closeErrors, failure)
					u.closeErrors = append(u.closeErrors, failure)
					u.mu.Unlock()
				}
			}()
		}
		if err == nil && process == nil {
			err = fmt.Errorf("终端组件未返回会话")
		}
		u.publish(ctx, taskTerminalEvent{owner: owner, session: session, generation: generation, process: process, err: err}, invalidate)
		if err != nil || ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-process.Done():
			u.publish(ctx, taskTerminalEvent{owner: owner, session: session, generation: generation, exited: true, exitCode: process.ExitCode()}, invalidate)
		}
		// Natural exit keeps the emulator and its final output until tab close.
		<-ctx.Done()
	}()
}

func (u *taskTerminalController) publish(ctx context.Context, event taskTerminalEvent, invalidate func()) {
	u.mu.Lock()
	accepted := !u.mailboxClosed && ctx.Err() == nil
	if accepted {
		u.mailbox = append(u.mailbox, event)
	}
	u.mu.Unlock()
	if accepted && invalidate != nil {
		invalidate()
	}
}

func (s *app) drainTaskTerminalEvents() {
	u := &s.taskTerminalUnit
	u.mu.Lock()
	events := u.mailbox
	u.mailbox = nil
	u.mu.Unlock()
	for _, event := range events {
		owner, session := event.owner, event.session
		if u.stopped || u.owners[owner.identity] != owner || session.ctx.Err() != nil || session.generation != event.generation {
			continue
		}
		if event.exited {
			session.exited, session.exitCode = true, event.exitCode
			continue
		}
		session.loading = false
		if event.err != nil {
			session.error = event.err.Error()
		} else {
			session.process = event.process
		}
	}
}

func (s *app) closeTaskTerminal(owner *taskTerminalOwner, id uint64) {
	for index, session := range owner.sessions {
		if session.id != id {
			continue
		}
		session.cancel()
		owner.sessions = append(owner.sessions[:index], owner.sessions[index+1:]...)
		if owner.selected == id {
			owner.selected = 0
			if len(owner.sessions) > 0 {
				owner.selected = owner.sessions[min(index, len(owner.sessions)-1)].id
				owner.focus = true
			}
		}
		return
	}
}

func (s *app) focusTaskTerminal() {
	if owner := s.ensureTaskTerminalsCurrent(); owner != nil {
		owner.focus = true
	}
}

func (s *app) stopTaskTerminals() {
	u := &s.taskTerminalUnit
	if u.stopped {
		return
	}
	u.stopped = true
	u.mu.Lock()
	u.mailboxClosed, u.mailbox = true, nil
	u.mu.Unlock()
	for _, owner := range u.owners {
		for _, session := range owner.sessions {
			session.cancel()
		}
	}
}

func (s *app) closeTaskTerminals(ctx context.Context) error {
	s.stopTaskTerminals()
	err := waitTaskTerminals(ctx, &s.taskTerminalUnit.wg)
	u := &s.taskTerminalUnit
	u.mu.Lock()
	defer u.mu.Unlock()
	return errors.Join(err, unsettledTaskTerminalCloseErrors(u.closeErrors))
}

// A failed close may settle later. Only unresolved process cleanup may block
// deletion/shutdown; a historical error must not permanently poison the task.
func unsettledTaskTerminalCloseErrors(failures []taskTerminalCloseError) error {
	var pending []error
	for _, failure := range failures {
		select {
		case <-failure.done:
		default:
			pending = append(pending, failure.err)
		}
	}
	return errors.Join(pending...)
}

func waitTaskTerminals(ctx context.Context, wg *sync.WaitGroup) error {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// The confirmed delete waits for only this task's processes, not background
// terminals owned by other tasks. New tabs remain blocked until it settles.
func (s *app) prepareTaskTerminalDelete(taskID string) func(context.Context) error {
	u := &s.taskTerminalUnit
	if u.deleting == nil {
		u.deleting = make(map[string]bool)
	}
	u.deleting[taskID] = true
	var owners []*taskTerminalOwner
	for _, owner := range u.owners {
		if owner.identity.id != taskID || owner.identity.home != s.snapshot.Home {
			continue
		}
		owners = append(owners, owner)
		for _, session := range owner.sessions {
			session.cancel()
		}
		owner.sessions, owner.selected = nil, 0
	}
	return func(ctx context.Context) error {
		for _, owner := range owners {
			if err := waitTaskTerminals(ctx, &owner.wg); err != nil {
				return err
			}
			u.mu.Lock()
			err := unsettledTaskTerminalCloseErrors(owner.closeErrors)
			u.mu.Unlock()
			if err != nil {
				return err
			}
		}
		return nil
	}
}
