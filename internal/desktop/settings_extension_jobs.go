package desktop

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"silverwing-go/internal/application"
)

type settingsExtensionReadScope struct {
	home, config, page, settingsPage string
}

type settingsExtensionReadJob struct {
	action application.Action
	key    string
	scope  settingsExtensionReadScope
	owner  *settingsExtensionJobs
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

// The window owns current requests and presentation tokens. Workers share only
// running: canceled/replaced requests stay there until their backend exits.
type settingsExtensionJobs struct {
	current map[string]*settingsExtensionReadJob
	mu      sync.Mutex
	running map[*settingsExtensionReadJob]struct{}
	closed  bool
}

func (s *app) settingsExtensionCurrentScope() settingsExtensionReadScope {
	return settingsExtensionReadScope{home: s.snapshot.Home, config: s.settingsUnit.state.configJSON, page: s.page, settingsPage: s.settingsUnit.state.settingsPage}
}

func (s *app) beginSettingsExtensionRead(action application.Action) (*settingsExtensionReadJob, error) {
	if !settingsExtensionRead(action) {
		return nil, errors.New("这不是设置扩展读取请求。")
	}
	state := s.ensureSettingsExtensionRequests()
	if state.jobs == nil {
		state.jobs = &settingsExtensionJobs{current: make(map[string]*settingsExtensionReadJob), running: make(map[*settingsExtensionReadJob]struct{})}
	}
	s.prepareSettingsExtensionRead(&action)
	ctx, cancel := context.WithCancel(context.Background())
	request := &settingsExtensionReadJob{action: action, key: settingsExtensionReadKey(action),
		scope: s.settingsExtensionCurrentScope(), owner: state.jobs, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	if !s.settingsExtensionActionRelevant(action) {
		cancel()
		return request, errors.New("设置扩展读取的页面或来源已变化，请重新读取。")
	}
	jobs := state.jobs
	jobs.mu.Lock()
	if jobs.closed {
		jobs.mu.Unlock()
		cancel()
		return request, errors.New("设置扩展读取已关闭。")
	}
	jobs.running[request] = struct{}{}
	jobs.mu.Unlock()
	jobs.current[request.key] = request
	return request, nil
}

func (s *app) settingsExtensionReadRelevant(request *settingsExtensionReadJob) bool {
	state := s.settingsUnit.state.settingsExtensionRequests
	if request == nil || state == nil || state.jobs != request.owner || request.owner.current[request.key] != request ||
		request.ctx.Err() != nil || request.scope != s.settingsExtensionCurrentScope() || !s.settingsExtensionActionRelevant(request.action) {
		return false
	}
	request.owner.mu.Lock()
	closed := request.owner.closed
	request.owner.mu.Unlock()
	return !closed
}

// Called before the shared busy/backend gate. These reads own their context;
// source saves, installation and removal continue through the mutation runner.
func (s *app) dispatchIndependentSettingsExtensionRead(action application.Action) bool {
	if !settingsExtensionRead(action) {
		return false
	}
	request, err := s.beginSettingsExtensionRead(action)
	if err != nil {
		if request != nil {
			s.applySettingsExtensionFailure(request.action, err)
		}
		return true
	}
	if s.backend == nil || s.win == nil || s.win.IsDestroyed() {
		s.finishSettingsExtensionRead(request, application.ActionResult{}, errors.New("设置扩展读取后端或窗口不可用，请重试。"))
		s.completeSettingsExtensionRead(request)
		return true
	}
	backend, win := s.backend, s.win
	win.Invalidate()
	go func() {
		result, err := executeSettingsExtensionRead(backend, request)
		if win.IsDestroyed() {
			request.cancel()
			s.completeSettingsExtensionRead(request)
			return
		}
		updated := make(chan struct{})
		win.Update(func() {
			defer close(updated)
			s.finishSettingsExtensionRead(request, result, err)
		})
		select {
		case <-updated:
		case <-request.ctx.Done():
		}
		s.completeSettingsExtensionRead(request)
	}()
	return true
}

func executeSettingsExtensionRead(backend application.Backend, request *settingsExtensionReadJob) (application.ActionResult, error) {
	if backend == nil || request == nil {
		return application.ActionResult{}, errors.New("设置扩展读取缺少后端或请求。")
	}
	if err := request.ctx.Err(); err != nil {
		return application.ActionResult{}, err
	}
	action := request.action
	if action.Kind == "workspace.tools-options" {
		// The backend accepts the task-creation token only. Keep the settings
		// request identity in the window-owned job, outside the typed payload.
		action.Payload = map[string]any{"taskCreateToken": payloadString(request.action.Payload, "taskCreateToken")}
	}
	result, err := backend.Dispatch(request.ctx, action)
	if request.ctx.Err() != nil {
		return application.ActionResult{}, errors.Join(request.ctx.Err(), err)
	}
	return result, err
}

func (s *app) finishSettingsExtensionRead(request *settingsExtensionReadJob, result application.ActionResult, err error) {
	if request == nil {
		return
	}
	defer request.cancel()
	if !s.settingsExtensionReadRelevant(request) {
		if request.owner.current[request.key] == request {
			s.observeSettingsExtensionRequests()
			if request.owner.current[request.key] == request {
				s.invalidateSettingsExtensionRead(request.key, "读取已取消，可重新读取。")
			}
		}
		return
	}
	delete(request.owner.current, request.key)
	if err != nil {
		s.applySettingsExtensionFailure(request.action, err)
	} else {
		// Settings results must not pass through the Skills browser's handler.
		s.applySettingsActionResult(request.action, result)
	}
}

func (s *app) completeSettingsExtensionRead(request *settingsExtensionReadJob) {
	if request == nil || request.owner == nil {
		return
	}
	jobs := request.owner
	jobs.mu.Lock()
	if _, exists := jobs.running[request]; exists {
		delete(jobs.running, request)
		close(request.done)
	}
	jobs.mu.Unlock()
}

func (s *settingsController) closeSettingsExtensionReads(ctx context.Context) error {
	state := s.state.settingsExtensionRequests
	if state == nil || state.jobs == nil {
		return nil
	}
	jobs := state.jobs
	jobs.mu.Lock()
	jobs.closed = true
	running := make([]*settingsExtensionReadJob, 0, len(jobs.running))
	for request := range jobs.running {
		request.cancel()
		running = append(running, request)
	}
	jobs.current = make(map[string]*settingsExtensionReadJob)
	jobs.mu.Unlock()
	for _, request := range running {
		select {
		case <-request.done:
		case <-ctx.Done():
			return fmt.Errorf("等待设置扩展读取清理：%w", ctx.Err())
		}
	}
	return nil
}
