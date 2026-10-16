package desktop

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sync"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/process"

	"github.com/egoist/mygo"
)

// workspaceCommandJobsState owns the one command slot across the whole app.
// active/running/closed are mutex protected because the backend, UI and window
// shutdown paths can observe them from different goroutines. current belongs
// to the window thread and identifies which response may update the UI.
type workspaceCommandJobsState struct {
	current *workspaceCommandJob

	mu         sync.Mutex
	active     *workspaceCommandJob
	owner      *workspaceCommandJob
	running    map[*workspaceCommandJob]struct{}
	closed     bool
	cleanupErr error
}

type workspaceCommandJob struct {
	action               application.Action
	backend              application.Backend
	window               *mygo.Window
	home                 string
	taskPath             string
	taskCreatedAt        string
	taskGroupID          string
	workspaceFingerprint string

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

func (s *app) ensureWorkspaceCommandJobs() *workspaceCommandJobsState {
	if s.workspaceCommandJobs == nil {
		s.workspaceCommandJobs = &workspaceCommandJobsState{running: make(map[*workspaceCommandJob]struct{})}
	} else if s.workspaceCommandJobs.running == nil {
		s.workspaceCommandJobs.running = make(map[*workspaceCommandJob]struct{})
	}
	return s.workspaceCommandJobs
}

func (s *app) workspaceCommandBusy() bool {
	jobs := s.workspaceCommandJobs
	if jobs == nil {
		return false
	}
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	return jobs.closed || jobs.active != nil
}

// dispatchWorkspaceCommandJob is called before the ordinary busy and backend
// gate checks. A custom command uses its own context and never takes that gate.
func (s *app) dispatchWorkspaceCommandJob(action application.Action) bool {
	if action.Kind != "workspace.command" {
		return false
	}
	if s.backend == nil || s.win == nil || s.win.IsDestroyed() {
		s.rejectWorkspaceCommandJob(action, errors.New("快捷命令执行后端或窗口不可用。"))
		return true
	}

	request, err := s.beginWorkspaceCommandJob(action)
	if err != nil {
		s.rejectWorkspaceCommandJob(action, err)
		return true
	}
	request.window.Invalidate()
	go s.runWorkspaceCommandJob(request)
	return true
}

func (s *app) beginWorkspaceCommandJob(action application.Action) (*workspaceCommandJob, error) {
	if !s.workspaceCommandActionRelevant(action) || s.workspaceCommand == nil {
		return nil, errors.New("快捷命令执行身份已变化，请重新选择后重试。")
	}
	state := s.workspaceCommand
	if state.Status != workspaceCommandRunning {
		return nil, errors.New("快捷命令不在可执行状态。")
	}
	backend, window := s.backend, s.win
	if backend == nil || window == nil || window.IsDestroyed() {
		return nil, errors.New("快捷命令执行后端或窗口不可用。")
	}

	ctx, cancel := context.WithCancel(context.Background())
	request := &workspaceCommandJob{
		action: cloneWorkspaceCommandAction(action), backend: backend, window: window,
		home: s.snapshot.Home, taskPath: state.TaskPath, taskCreatedAt: state.TaskCreatedAt,
		taskGroupID: state.TaskGroupID, workspaceFingerprint: state.WorkspaceFingerprint,
		ctx: ctx, cancel: cancel, done: make(chan struct{}),
	}
	if err := s.claimWorkspaceCommandJob(request); err != nil {
		cancel()
		return nil, err
	}
	return request, nil
}

func (s *app) claimWorkspaceCommandJob(request *workspaceCommandJob) error {
	if request == nil {
		return errors.New("快捷命令请求为空。")
	}
	jobs := s.ensureWorkspaceCommandJobs()
	jobs.mu.Lock()
	if jobs.closed {
		jobs.mu.Unlock()
		return errors.New("快捷命令服务正在关闭。")
	}
	if jobs.active != nil {
		jobs.mu.Unlock()
		return errors.New("另一条快捷命令仍在清理，请稍后重试。")
	}
	jobs.active = request
	// Keep the latest successful claim after completion. A canceled request's
	// queued cleanup failure may arrive after active/current have been cleared.
	jobs.owner = request
	jobs.running[request] = struct{}{}
	jobs.mu.Unlock()
	jobs.current = request
	return nil
}

func (s *app) rejectWorkspaceCommandJob(action application.Action, err error) {
	message := "快捷命令执行失败。"
	if err != nil {
		message = err.Error()
	}
	if s.workspaceCommand != nil && payloadString(action.Payload, "commandToken") == s.workspaceCommand.Token {
		if s.workspaceCommand.Status == workspaceCommandRunning && s.workspaceCommandActionRelevant(action) {
			state := s.workspaceCommand
			state.Status = workspaceCommandFailed
			state.StopRequested = false
			state.Duration = time.Since(state.StartedAt)
			state.Error = message
		}
	}
	s.lastError = message
	if s.win != nil && !s.win.IsDestroyed() {
		s.win.Invalidate()
	}
}

func (s *app) runWorkspaceCommandJob(request *workspaceCommandJob) {
	defer func() {
		if recovered := recover(); recovered != nil {
			s.postWorkspaceCommandJobResult(request, application.ActionResult{}, fmt.Errorf("快捷命令执行异常：%v", recovered))
		}
	}()

	var result application.ActionResult
	var err error
	if request == nil || request.backend == nil {
		err = errors.New("快捷命令执行缺少后端。")
	} else if request.ctx.Err() != nil {
		err = request.ctx.Err()
	} else if streamed, ok := request.backend.(application.WorkspaceCommandBackend); ok {
		result, err = streamed.DispatchWorkspaceCommand(request.ctx, request.action, s.workspaceCommandJobStream(request))
	} else {
		result, err = request.backend.Dispatch(request.ctx, request.action)
	}
	if request != nil && request.ctx.Err() != nil {
		err = errors.Join(request.ctx.Err(), err)
	}
	s.postWorkspaceCommandJobResult(request, result, err)
}

func (s *app) workspaceCommandJobStream(request *workspaceCommandJob) func(process.StreamEvent) {
	return func(event process.StreamEvent) {
		if request == nil || request.ctx.Err() != nil || request.window == nil || request.window.IsDestroyed() {
			return
		}
		request.window.Update(func() {
			if !s.workspaceCommandJobRelevant(request) || s.workspaceCommand == nil {
				return
			}
			state := s.workspaceCommand
			if state.Status != workspaceCommandRunning {
				return
			}
			if state.Scroll.Y >= state.Scroll.MaxY {
				state.Scroll.Y = math.MaxFloat32
			}
			appendWorkspaceCommandEvent(state, event.Stream, event.Text)
		})
	}
}

func (s *app) postWorkspaceCommandJobResult(request *workspaceCommandJob, result application.ActionResult, err error) {
	if request == nil {
		return
	}
	s.recordWorkspaceCommandCleanupFailure(err)
	if request.window == nil || request.window.IsDestroyed() {
		request.cancel()
		s.completeWorkspaceCommandJob(request)
		return
	}
	updateDone := make(chan struct{})
	request.window.Update(func() {
		defer close(updateDone)
		s.finishWorkspaceCommandJob(request, result, err)
	})
	// Window.Update posts asynchronously in MyGO v0.4.2. Keep the singleton
	// slot until the UI has consumed the final response. Cancellation unblocks
	// shutdown if the queued callback cannot run.
	select {
	case <-updateDone:
	case <-request.ctx.Done():
	}
	s.completeWorkspaceCommandJob(request)
}

func (s *app) workspaceCommandJobRelevant(request *workspaceCommandJob) bool {
	if request == nil || request.ctx.Err() != nil || request.window == nil || request.window.IsDestroyed() {
		return false
	}
	jobs := s.workspaceCommandJobs
	if jobs == nil || jobs.current != request {
		return false
	}
	jobs.mu.Lock()
	closed, active := jobs.closed, jobs.active == request
	jobs.mu.Unlock()
	if closed || !active || s.snapshot.Home != request.home || s.workspaceCommand == nil ||
		s.workspaceCommand.Token != payloadString(request.action.Payload, "commandToken") ||
		s.workspaceCommand.WorkspaceFingerprint != request.workspaceFingerprint {
		return false
	}
	state := s.workspaceCommand
	if state.TaskPath != request.taskPath || state.TaskCreatedAt != request.taskCreatedAt || state.TaskGroupID != request.taskGroupID {
		return false
	}
	return s.workspaceCommandActionRelevant(request.action)
}

func (s *app) finishWorkspaceCommandJob(request *workspaceCommandJob, result application.ActionResult, err error) {
	if request == nil {
		return
	}
	defer request.cancel()
	if process.IsCleanupFailure(err) {
		s.recordWorkspaceCommandCleanupFailure(err)
		s.finishWorkspaceCommandCleanupFailure(request, err)
		return
	}
	if !s.workspaceCommandJobRelevant(request) {
		return
	}
	jobs := s.workspaceCommandJobs
	if jobs != nil && jobs.current == request {
		jobs.current = nil
	}
	if err != nil {
		s.applyWorkspaceCommandFailure(request.action, err)
	} else {
		s.applyWorkspaceCommandResult(request.action, result)
	}
}

// Cleanup uncertainty survives a canceled context and a retired window, but
// is retained only once for this command manager. Close returns this diagnosis.
func (s *app) recordWorkspaceCommandCleanupFailure(err error) {
	if !process.IsCleanupFailure(err) || s.workspaceCommandJobs == nil {
		return
	}
	jobs := s.workspaceCommandJobs
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	if jobs.cleanupErr == nil {
		jobs.cleanupErr = err
	}
}

// Only cleanup failure may finish an explicitly stopped command. This does
// not relax jobRelevant for stream events or ordinary results, and never
// starts a backend action. The latest claim and full UI identity must match.
func (s *app) finishWorkspaceCommandCleanupFailure(request *workspaceCommandJob, err error) {
	if request == nil || request.window == nil || request.window != s.win || request.window.IsDestroyed() ||
		s.workspaceCommandJobs == nil {
		return
	}
	jobs := s.workspaceCommandJobs
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	state := s.workspaceCommand
	if jobs.closed || jobs.owner != request || state == nil || s.snapshot.Home != request.home ||
		(state.Status != workspaceCommandRunning && !(state.Status == workspaceCommandCancelled && state.StopRequested)) ||
		!sameWorkspaceCommandPath(state.TaskPath, request.taskPath) || state.TaskCreatedAt != request.taskCreatedAt ||
		state.TaskGroupID != request.taskGroupID || state.WorkspaceFingerprint != request.workspaceFingerprint ||
		!workspaceCommandActionMatchesState(request.action, state) || !s.workspaceCommandIdentityCurrent(state) {
		return
	}
	if jobs.current == request {
		jobs.current = nil
	}
	finishWorkspaceCommandFailure(state, err)
}

func (s *app) completeWorkspaceCommandJob(request *workspaceCommandJob) {
	if request == nil {
		return
	}
	jobs := s.workspaceCommandJobs
	if jobs == nil {
		request.cancel()
		return
	}
	jobs.mu.Lock()
	if _, exists := jobs.running[request]; exists {
		delete(jobs.running, request)
		if jobs.active == request {
			jobs.active = nil
		}
		close(request.done)
	}
	jobs.mu.Unlock()
	request.cancel()
}

// cancelWorkspaceCommandJob cancels only the command with this UI token. It
// deliberately leaves active occupied until the backend and its cleanup exit.
func (s *app) cancelWorkspaceCommandJob(token string) bool {
	jobs := s.workspaceCommandJobs
	if jobs == nil || token == "" {
		return false
	}
	jobs.mu.Lock()
	request := jobs.active
	if request == nil || payloadString(request.action.Payload, "commandToken") != token {
		jobs.mu.Unlock()
		return false
	}
	request.cancel()
	jobs.mu.Unlock()
	if jobs.current == request {
		jobs.current = nil
	}
	return true
}

// observeWorkspaceCommandJobIdentity cancels only when task/workspace/home
// identity changes. It intentionally does not inspect the current page, so a
// hidden command dialog continues collecting output while another page is open.
func (s *app) observeWorkspaceCommandJobIdentity() {
	jobs := s.workspaceCommandJobs
	if jobs == nil || jobs.current == nil {
		return
	}
	request := jobs.current
	state := s.workspaceCommand
	if state == nil || state.Token != payloadString(request.action.Payload, "commandToken") ||
		!s.workspaceCommandIdentityCurrent(state) {
		if state != nil && state.Status == workspaceCommandRunning {
			state.StopRequested = true
			state.Status = workspaceCommandCancelled
			state.Duration = time.Since(state.StartedAt)
		}
		s.cancelWorkspaceCommandJob(payloadString(request.action.Payload, "commandToken"))
		s.workspaceCommand = nil
	}
}

func (s *app) closeWorkspaceCommandJobs(ctx context.Context) error {
	jobs := s.workspaceCommandJobs
	if jobs == nil {
		return nil
	}
	jobs.mu.Lock()
	jobs.closed = true
	requests := make([]*workspaceCommandJob, 0, len(jobs.running))
	for request := range jobs.running {
		request.cancel()
		requests = append(requests, request)
	}
	jobs.mu.Unlock()
	for _, request := range requests {
		select {
		case <-request.done:
		case <-ctx.Done():
			jobs.mu.Lock()
			cleanupErr := jobs.cleanupErr
			jobs.mu.Unlock()
			return errors.Join(fmt.Errorf("等待快捷命令清理：%w", ctx.Err()), cleanupErr)
		}
	}
	jobs.mu.Lock()
	cleanupErr := jobs.cleanupErr
	jobs.mu.Unlock()
	return cleanupErr
}
