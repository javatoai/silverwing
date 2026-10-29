package desktop

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"silverwing-go/internal/application"
)

type taskDeleteRiskScope struct {
	taskID, taskPath, taskCreatedAt string
	home, taskRoot, config, page    string
}

type taskDeleteRiskJob struct {
	action     application.Action
	dialog     *taskDeleteDialogState
	scope      taskDeleteRiskScope
	generation uint64
	ctx        context.Context
	cancel     context.CancelFunc
	done       chan struct{}
}

// The window owns current and generation. Workers share only the cleanup set.
// Replaced and closed-dialog requests stay in running until the backend exits.
type taskDeleteRiskJobState struct {
	current    *taskDeleteRiskJob
	generation uint64
	mu         sync.Mutex
	running    map[*taskDeleteRiskJob]struct{}
	closed     bool
}

func (s *app) ensureTaskDeleteRiskJobs() *taskDeleteRiskJobState {
	if s.taskUnit.state.taskDeleteRiskJobs == nil {
		s.taskUnit.state.taskDeleteRiskJobs = &taskDeleteRiskJobState{running: make(map[*taskDeleteRiskJob]struct{})}
	}
	return s.taskUnit.state.taskDeleteRiskJobs
}

func (s *app) taskDeleteRiskCurrentScope(dialog *taskDeleteDialogState) (taskDeleteRiskScope, bool) {
	if dialog == nil || !dialog.Open || dialog.Submitting ||
		taskNavigationKey(s.taskUnit.currentTask()) != dialog.SelectionKey || s.taskUnit.currentTask().path != dialog.SelectionPath ||
		(dialog.Page != "" && dialog.Page != s.page) {
		return taskDeleteRiskScope{}, false
	}
	task := s.taskUnit.taskByID(dialog.TaskID)
	if task.id == "" || task.path != dialog.TaskPath || task.createdAt != dialog.TaskCreatedAt || task.archived != dialog.TaskArchived {
		return taskDeleteRiskScope{}, false
	}
	return taskDeleteRiskScope{
		taskID: task.id, taskPath: task.path, taskCreatedAt: task.createdAt,
		home: s.snapshot.Home, taskRoot: s.configString("taskRoot"), config: s.settingsUnit.state.configJSON, page: s.page,
	}, true
}

func (s *app) beginTaskDeleteRiskJob(action application.Action) (*taskDeleteRiskJob, error) {
	dialog := s.taskUnit.state.taskDeleteDialog
	if action.Kind != "task.delete-risks" || !s.taskDeleteStateMatches(action) {
		return nil, errors.New("删除检查请求已变化，请重新检查。")
	}
	scope, ok := s.taskDeleteRiskCurrentScope(dialog)
	if !ok {
		return nil, errors.New("任务身份已变化，请重新打开删除确认。")
	}
	state := s.ensureTaskDeleteRiskJobs()
	state.mu.Lock()
	closed := state.closed
	state.mu.Unlock()
	if closed {
		return nil, errors.New("删除风险读取已关闭。")
	}
	s.cancelTaskDeleteRiskRead()
	ctx, cancel := context.WithCancel(context.Background())
	state.generation++
	request := &taskDeleteRiskJob{
		action: application.Action{Kind: action.Kind, TaskID: action.TaskID, Path: action.Path,
			Payload: map[string]any{"taskDeleteRequestID": payloadString(action.Payload, "taskDeleteRequestID")}},
		dialog: dialog, scope: scope, generation: state.generation,
		ctx: ctx, cancel: cancel, done: make(chan struct{}),
	}
	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		cancel()
		return nil, errors.New("删除风险读取已关闭。")
	}
	state.running[request] = struct{}{}
	state.mu.Unlock()
	state.current = request
	dialog.RiskScope = &request.scope
	dialog.Loading = true
	return request, nil
}

func (s *app) taskDeleteRiskJobRelevant(request *taskDeleteRiskJob) bool {
	state := s.taskUnit.state.taskDeleteRiskJobs
	if request == nil || state == nil || state.current != request || state.generation != request.generation ||
		request.ctx.Err() != nil || s.taskUnit.state.taskDeleteDialog != request.dialog || !s.taskDeleteStateMatches(request.action) {
		return false
	}
	state.mu.Lock()
	closed := state.closed
	state.mu.Unlock()
	current, ok := s.taskDeleteRiskCurrentScope(request.dialog)
	return !closed && ok && current == request.scope
}

// dispatchIndependentTaskDeleteRisks is called before the ordinary busy gate.
// Only this read receives its context; task.delete continues through the
// existing shared mutation gate and confirmation rules.
func (s *app) dispatchIndependentTaskDeleteRisks(action application.Action) bool {
	if action.Kind != "task.delete-risks" {
		return false
	}
	if s.backend == nil || s.win == nil || s.win.IsDestroyed() {
		s.applyTaskDeleteRiskFailure(action, errors.New("删除风险读取后端或窗口不可用，请重试。"))
		return true
	}
	request, err := s.beginTaskDeleteRiskJob(action)
	if err != nil {
		s.applyTaskDeleteRiskFailure(action, err)
		return true
	}
	backend, win := s.backend, s.win
	win.Invalidate()
	go func() {
		result, err := executeTaskDeleteRiskJob(backend, request)
		if win.IsDestroyed() {
			request.cancel()
			s.completeTaskDeleteRiskJob(request)
			return
		}
		updated := make(chan struct{})
		win.Update(func() {
			defer close(updated)
			s.finishTaskDeleteRiskJob(request, result, err)
		})
		select {
		case <-updated:
		case <-request.ctx.Done():
		}
		s.completeTaskDeleteRiskJob(request)
	}()
	return true
}

func executeTaskDeleteRiskJob(backend application.Backend, request *taskDeleteRiskJob) (application.ActionResult, error) {
	if backend == nil || request == nil {
		return application.ActionResult{}, errors.New("删除风险读取缺少后端或请求。")
	}
	if err := request.ctx.Err(); err != nil {
		return application.ActionResult{}, err
	}
	result, err := backend.Dispatch(request.ctx, request.action)
	if request.ctx.Err() != nil {
		return application.ActionResult{}, errors.Join(request.ctx.Err(), err)
	}
	return result, err
}

func (s *app) finishTaskDeleteRiskJob(request *taskDeleteRiskJob, result application.ActionResult, err error) {
	if request == nil {
		return
	}
	defer request.cancel()
	if !s.taskDeleteRiskJobRelevant(request) {
		if state := s.taskUnit.state.taskDeleteRiskJobs; state != nil && state.current == request {
			if s.taskUnit.state.taskDeleteDialog != request.dialog {
				s.cancelTaskDeleteRiskRead()
			} else {
				s.observeTaskDeleteRiskJobs()
			}
		}
		return
	}
	s.taskUnit.state.taskDeleteRiskJobs.current = nil
	if err != nil {
		s.applyTaskDeleteRiskFailure(request.action, err)
	} else {
		s.applyTaskDeleteRiskResult(request.action, result)
	}
}

func (s *app) completeTaskDeleteRiskJob(request *taskDeleteRiskJob) {
	state := s.taskUnit.state.taskDeleteRiskJobs
	if state == nil || request == nil {
		return
	}
	state.mu.Lock()
	if _, exists := state.running[request]; exists {
		delete(state.running, request)
		close(request.done)
	}
	state.mu.Unlock()
}

func (s *app) cancelTaskDeleteRiskRead() {
	if state := s.taskUnit.state.taskDeleteRiskJobs; state != nil && state.current != nil {
		request := state.current
		state.current = nil
		request.cancel()
	}
}

func (s *app) invalidateTaskDeleteRiskRead() {
	s.cancelTaskDeleteRiskRead()
	if dialog := s.taskUnit.state.taskDeleteDialog; dialog != nil && !dialog.Submitting {
		dialog.RequestID = taskDeleteRequestSequence.Add(1)
		dialog.RiskScope = nil
		dialog.Loading = false
		dialog.Risks = nil
		dialog.Error = "任务或应用配置已变化，请重新检查。"
	}
}

// renderTaskDeleteDialog calls this even when the dialog is not shown. Reads
// are canceled on navigation/task changes, and successful old inspections are
// invalidated when their home/configuration changes before confirmation.
func (s *app) observeTaskDeleteRiskJobs() {
	dialog := s.taskUnit.state.taskDeleteDialog
	if dialog == nil || !dialog.Open {
		s.cancelTaskDeleteRiskRead()
		return
	}
	if dialog.Submitting {
		return
	}
	if state := s.taskUnit.state.taskDeleteRiskJobs; state != nil && state.current != nil && state.current.dialog != dialog {
		s.cancelTaskDeleteRiskRead()
	}
	current, ok := s.taskDeleteRiskCurrentScope(dialog)
	if !ok || (dialog.RiskScope != nil && current.page != dialog.RiskScope.page) {
		s.closeTaskDeleteDialog()
		return
	}
	if dialog.RiskScope != nil && current != *dialog.RiskScope {
		s.invalidateTaskDeleteRiskRead()
		return
	}
	if state := s.taskUnit.state.taskDeleteRiskJobs; state != nil && state.current != nil && !s.taskDeleteRiskJobRelevant(state.current) {
		s.invalidateTaskDeleteRiskRead()
	}
}

func (s *taskController) closeTaskDeleteRiskJobs(ctx context.Context) error {
	state := s.state.taskDeleteRiskJobs
	if state == nil {
		return nil
	}
	state.mu.Lock()
	state.closed = true
	if state.current != nil {
		state.current.cancel()
		state.current = nil
	}
	running := make([]*taskDeleteRiskJob, 0, len(state.running))
	for request := range state.running {
		request.cancel()
		running = append(running, request)
	}
	state.mu.Unlock()
	for _, request := range running {
		select {
		case <-request.done:
		case <-ctx.Done():
			return fmt.Errorf("等待删除风险读取清理：%w", ctx.Err())
		}
	}
	return nil
}
