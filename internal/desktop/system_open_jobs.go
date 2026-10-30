package desktop

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"silverwing-go/internal/application"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type systemOpenScope struct {
	home, sourcePath                           string
	taskID, taskPath, createdAt, materialsRoot string
	workspacePath                              string
	materialsConfigJSON                        string
}

type systemOpenJob struct {
	action    application.Action
	key       string
	scope     systemOpenScope
	taskScope *taskMenuScope
	update    func(func())
	current   func() bool
	owner     *systemOpenJobs
	backend   application.Backend
	window    *mygo.Window
	ctx       context.Context
	cancel    context.CancelFunc
	done      chan struct{}
}

// opening stays occupied until its backend and queued response have finished,
// including after cancellation. A native chooser cannot be undone by cancelling
// its context. current/running/closed also belong to the shutdown boundary.
type systemOpenJobs struct {
	mu      sync.Mutex
	current map[string]*systemOpenJob
	opening *systemOpenJob
	running map[*systemOpenJob]struct{}
	closed  bool

	message, error string // window thread only
}

func independentSystemOpenAction(kind string) bool {
	switch kind {
	case "system.open", "system.open-with", "system.reveal", "system.logs", "document.open", "document.reveal",
		"system.terminal", "system.cli-terminal":
		return true
	}
	return false
}

func systemOpenJobKey(action application.Action) string {
	switch action.Kind {
	case "system.logs":
		return "directory"
	case "system.terminal":
		return "terminal"
	case "system.cli-terminal":
		return "cli-terminal:" + strings.ToLower(payloadString(action.Payload, "tool"))
	}
	if action.Kind == "system.reveal" || action.Kind == "document.reveal" {
		return "reveal"
	}
	if action.Kind == "system.open" {
		// Match application.systemOpenLink for routing only. The application
		// still validates and submits the URI; no UI filesystem read is needed.
		if parsed, err := url.Parse(systemOpenSourcePath(action)); err == nil {
			switch strings.ToLower(parsed.Scheme) {
			case "http", "https":
				if parsed.Host != "" {
					return "link"
				}
			case "mailto":
				return "link"
			}
		}
	}
	if action.Kind == "system.open" || action.Kind == "document.open" {
		if directory, _ := action.Payload["desktopSystemOpenDirectory"].(bool); directory {
			return "directory"
		}
	}
	return "open"
}

func (s *app) ensureSystemOpenJobs() *systemOpenJobs {
	if s.systemOpenJobs == nil {
		s.systemOpenJobs = &systemOpenJobs{current: make(map[string]*systemOpenJob), running: make(map[*systemOpenJob]struct{})}
	}
	return s.systemOpenJobs
}

func systemOpenSourcePath(action application.Action) string {
	if action.Path != "" {
		return action.Path
	}
	return payloadString(action.Payload, "path")
}

func (s *app) systemOpenCurrentScope(action application.Action) (systemOpenScope, error) {
	return s.systemOpenScopeFor(action, nil)
}

// A task-menu directory request may target an unselected task. Other system
// opens retain their selected-task contract and cannot opt in via a payload.
func (s *app) systemOpenScopeFor(action application.Action, taskScope *taskMenuScope) (systemOpenScope, error) {
	scope := systemOpenScope{home: s.snapshot.Home, sourcePath: systemOpenSourcePath(action)}
	if action.Kind == "document.open" || action.Kind == "document.reveal" {
		scope.materialsConfigJSON = s.settingsUnit.state.configJSON
	}
	if scope.sourcePath == "" && action.Kind != "system.logs" {
		return scope, errors.New("路径不能为空")
	}
	if action.TaskID == "" {
		if taskScope != nil {
			return scope, errors.New("任务资料打开缺少目标任务。")
		}
		return scope, nil
	}
	task := s.taskUnit.taskByID(action.TaskID)
	if taskScope != nil {
		actual, valid := s.taskMenuScopeFor(task)
		directory, _ := action.Payload["desktopSystemOpenDirectory"].(bool)
		if !valid || actual != *taskScope || action.Kind != "system.open" || !directory || !task.materialsReady() ||
			scope.sourcePath != task.materialsPath || action.ServiceID != "" || action.ModuleID != "" {
			return scope, errors.New("任务资料目录或应用上下文已变化，请重新打开任务菜单。")
		}
	} else if task.id == "" || s.taskUnit.currentTask().id != task.id {
		return scope, errors.New("系统打开的任务已变化，请重新选择后重试。")
	}
	scope.taskID, scope.taskPath, scope.createdAt, scope.materialsRoot = task.id, task.path, task.createdAt, task.materialsPath
	if action.ServiceID != "" || action.ModuleID != "" {

		return scope, errors.New("系统打开的工作区已变化，请重新选择后重试。")
	}
	return scope, nil
}

func cloneSystemOpenAction(action application.Action) application.Action {
	if action.Payload != nil {
		payload := make(map[string]any, len(action.Payload))
		for key, value := range action.Payload {
			payload[key] = value
		}
		action.Payload = payload
	}
	return action
}

func (s *app) beginSystemOpenJob(action application.Action) (*systemOpenJob, error) {
	return s.beginScopedSystemOpenJob(action, nil)
}

func (s *app) beginScopedSystemOpenJob(action application.Action, taskScope *taskMenuScope) (*systemOpenJob, error) {
	if !independentSystemOpenAction(action.Kind) {
		return nil, errors.New("这不是系统打开或定位请求。")
	}
	var update func(func())
	var contextCurrent func() bool
	if taskScope != nil {
		owned := *taskScope
		window := s.win
		action = taskMenuContextAction(action, owned)
		update = taskContextUpdater(window, s.taskUnit.state.taskContextUpdate)
		if update == nil {
			return nil, errTaskContextChanged
		}
		contextCurrent = func() bool {
			return (window == nil || !window.IsDestroyed()) && s.taskMenuScopeCurrent(owned)
		}
	}
	scope, err := s.systemOpenScopeFor(action, taskScope)
	if err != nil {
		return nil, err
	}
	// Document wrappers validate this captured identity again at the backend
	// boundary. Keep the checks shared by every materials external-open entry.
	if action.Kind == "document.open" || action.Kind == "document.reveal" {
		action = cloneMaterialsFileJobAction(action)
		action.Payload["draftTaskPath"] = scope.taskPath
		action.Payload["draftMaterialsRoot"] = scope.materialsRoot
		action.Payload["desktopRenameTaskCreatedAt"] = scope.createdAt
		action.Payload["materialsHome"] = scope.home
		if config, ok := s.materialsGitConfig(); ok {
			action.Payload["materialsConfigScope"] = application.MaterialsGitScope(scope.home, config)
		}
	}
	jobs := s.ensureSystemOpenJobs()
	key := systemOpenJobKey(action)
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	if jobs.closed {
		return nil, errors.New("系统打开与定位已关闭。")
	}
	// Source DesktopActions.openFile returns Cancelled when its file-open
	// mutex is occupied. Do not replace the chooser or publish an error.
	if key == "open" && jobs.opening != nil {
		return nil, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	request := &systemOpenJob{action: cloneSystemOpenAction(action), key: key, scope: scope,
		owner: jobs, backend: s.backend, window: s.win, ctx: ctx, cancel: cancel, done: make(chan struct{}),
		update: update, current: contextCurrent}
	if taskScope != nil {
		owned := *taskScope
		request.taskScope = &owned
	}
	if previous := jobs.current[key]; previous != nil {
		previous.cancel()
	}
	jobs.current[key] = request
	jobs.running[request] = struct{}{}
	if key == "open" {
		jobs.opening = request
	}
	jobs.error = ""
	return request, nil
}

// This route runs before busy/backendGate and never requests a Snapshot.
// Document wrappers retain their application-layer materials path validation.
func (s *app) dispatchIndependentSystemOpen(action application.Action) bool {
	return s.dispatchScopedSystemOpen(action, nil)
}

func (s *app) dispatchScopedSystemOpen(action application.Action, taskScope *taskMenuScope) bool {
	if !independentSystemOpenAction(action.Kind) {
		return false
	}
	if s.backend == nil || (s.win != nil && s.win.IsDestroyed()) {
		s.ensureSystemOpenJobs().error = "系统打开后端或窗口不可用，请重试。"
		return true
	}
	request, err := s.beginScopedSystemOpenJob(action, taskScope)
	if err != nil {
		s.ensureSystemOpenJobs().error = err.Error()
		if s.win != nil {
			s.win.Invalidate()
		}
		return true
	}
	if request != nil {
		if s.win != nil {
			s.win.Invalidate()
		}
		go s.runSystemOpenJob(request)
	}
	return true
}

func executeSystemOpenJob(request *systemOpenJob) (application.ActionResult, error) {
	if request == nil || request.backend == nil {
		return application.ActionResult{}, errors.New("系统打开缺少后端或请求。")
	}
	if err := request.ctx.Err(); err != nil {
		return application.ActionResult{}, err
	}
	if request.taskScope != nil {
		if err := waitForTaskMenuScope(request.ctx, request.update, request.current,
			func() bool { return request.window != nil && request.window.IsDestroyed() }); err != nil {
			return application.ActionResult{}, err
		}
	}
	result, err := request.backend.Dispatch(request.ctx, request.action)
	if request.ctx.Err() != nil {
		return application.ActionResult{}, errors.Join(request.ctx.Err(), err)
	}
	return result, err
}

func (s *app) runSystemOpenJob(request *systemOpenJob) {
	if request == nil {
		return
	}
	defer s.completeSystemOpenJob(request)
	result, err := executeSystemOpenJob(request)
	win := request.window
	if win == nil || win.IsDestroyed() {
		return
	}
	updated := make(chan struct{})
	win.Update(func() {
		defer close(updated)
		s.finishSystemOpenJob(request, result, err)
	})
	// Window.Update is asynchronous; retain the slot until the UI consumes the
	// result, or cancellation makes that queued response ineligible.
	select {
	case <-updated:
	case <-request.ctx.Done():
	}
}

func (s *app) systemOpenJobRelevant(request *systemOpenJob) bool {
	if request == nil || request.owner != s.systemOpenJobs || request.ctx.Err() != nil || s.win != request.window {
		return false
	}
	jobs := request.owner
	jobs.mu.Lock()
	current, closed := jobs.current[request.key], jobs.closed
	jobs.mu.Unlock()
	if closed || current != request {
		return false
	}
	scope, err := s.systemOpenScopeFor(request.action, request.taskScope)
	return err == nil && scope == request.scope
}

func (s *app) finishSystemOpenJob(request *systemOpenJob, result application.ActionResult, err error) {
	if request == nil {
		return
	}
	defer request.cancel()
	if !s.systemOpenJobRelevant(request) {
		return
	}
	jobs := request.owner
	if err != nil {
		jobs.error = err.Error()
		return
	}
	if outcome, ok := result.Data.(application.FileOpeningResult); ok && outcome.Status == "CANCELLED" {
		return
	}
	jobs.error = ""
	if result.Message != "" {
		jobs.message = result.Message
	}
}

func (s *app) completeSystemOpenJob(request *systemOpenJob) {
	if request == nil || request.owner == nil {
		return
	}
	request.cancel()
	jobs := request.owner
	jobs.mu.Lock()
	if _, running := jobs.running[request]; running {
		delete(jobs.running, request)
		if jobs.current[request.key] == request {
			delete(jobs.current, request.key)
		}
		if jobs.opening == request {
			jobs.opening = nil
		}
		close(request.done)
	}
	jobs.mu.Unlock()
	if request.window != nil && !request.window.IsDestroyed() {
		request.window.Invalidate()
	}
}

func (s *app) cancelSystemOpenJob(key string) {
	jobs := s.systemOpenJobs
	if jobs == nil {
		return
	}
	jobs.mu.Lock()
	if request := jobs.current[key]; request != nil {
		request.cancel()
		delete(jobs.current, key)
	}
	jobs.mu.Unlock()
}

func (s *app) observeSystemOpenJobs() {
	jobs := s.systemOpenJobs
	if jobs == nil {
		return
	}
	jobs.mu.Lock()
	requests := make([]*systemOpenJob, 0, len(jobs.current))
	for _, request := range jobs.current {
		requests = append(requests, request)
	}
	jobs.mu.Unlock()
	for _, request := range requests {
		if !s.systemOpenJobRelevant(request) {
			s.cancelSystemOpenJob(request.key)
		}
	}
}

func (s *app) systemOpenBusy() bool {
	jobs := s.systemOpenJobs
	if jobs == nil {
		return false
	}
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	return jobs.closed || jobs.opening != nil
}

func (s *app) materialsSystemOpenBusy(state *materialsFileManagementState) bool {
	return s.backend == nil || s.systemOpenBusy() || (state != nil &&
		(state.flowActive || state.pickerBusy || state.pathCopyRequest != nil))
}

func (s *app) systemRevealBusy() bool {
	jobs := s.systemOpenJobs
	if jobs == nil {
		return false
	}
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	return jobs.closed
}

func (s *app) materialsSystemRevealBusy(state *materialsFileManagementState) bool {
	return s.backend == nil || s.systemRevealBusy() || (state != nil &&
		(state.flowActive || state.pickerBusy || state.pathCopyRequest != nil))
}

func (s *app) stopSystemOpenJobs() []*systemOpenJob {
	jobs := s.systemOpenJobs
	if jobs == nil {
		return nil
	}
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	jobs.closed = true
	requests := make([]*systemOpenJob, 0, len(jobs.running))
	for request := range jobs.running {
		request.cancel()
		requests = append(requests, request)
	}
	clear(jobs.current)
	return requests
}

func (s *app) closeSystemOpenJobs(ctx context.Context) error {
	for _, request := range s.stopSystemOpenJobs() {
		select {
		case <-request.done:
		case <-ctx.Done():
			return fmt.Errorf("等待系统打开与定位清理：%w", ctx.Err())
		}
	}
	return nil
}

func (s *app) renderSystemOpenStatus(c *ui.Context, p palette) {
	jobs := s.systemOpenJobs
	if jobs == nil {
		return
	}
	jobs.mu.Lock()
	opening := jobs.opening
	revealing := jobs.current["reveal"]
	linking := jobs.current["link"]
	directory := jobs.current["directory"]
	terminal := jobs.current["terminal"]
	cliTerminals := make([]*systemOpenJob, 0, 5)
	for _, tool := range []string{"git", "codex", "meegle"} {
		cliTerminals = append(cliTerminals, jobs.current["cli-terminal:"+tool])
	}
	jobs.mu.Unlock()
	requests := append([]*systemOpenJob{opening, revealing, linking, directory, terminal}, cliTerminals...)
	for _, request := range requests {
		if request == nil {
			continue
		}
		ui.Row(c).Height(36).Padding(0, 14).Gap(10).Background(p.selected).Children(func() {
			text := "正在交给系统打开…"
			switch request.key {
			case "reveal":
				text = "正在定位文件…"
			case "link":
				text = "正在交给系统打开链接…"
			case "directory":
				text = "正在交给系统打开目录…"
			case "terminal":
				text = "正在打开系统终端…"
			default:
				if strings.HasPrefix(request.key, "cli-terminal:") {
					text = "正在系统终端运行 " + payloadString(request.action.Payload, "tool") + "…"
				}
			}
			if request.ctx.Err() != nil {
				text = "正在等待系统操作结束…"
			}
			label(c, text, 12, p.blue).Grow(1)
			if request.ctx.Err() == nil && action(c, "system-open-stop-"+request.key, "close", "停止等待", p, false) {
				s.cancelSystemOpenJob(request.key)
			}
		})
	}
}
