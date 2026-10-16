package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"silverwing-go/internal/application"
	"silverwing-go/internal/process"
)

// Service configuration is global. Switching tasks does not invalidate its reads.
type serviceRepositoryChoiceScope struct {
	home, session, groupID, serviceID, repositoryID, rootPath string
}

type serviceRepositoryChoiceJob struct {
	action application.Action
	scope  serviceRepositoryChoiceScope
	editor *serviceEditorState
	remote string
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

// Two slots own choices and addresses. Retired requests stay in running until
// their backend has exited, even when it does not immediately honour cancellation.
type serviceRepositoryChoiceJobs struct {
	mu         sync.Mutex
	current    map[string]*serviceRepositoryChoiceJob
	running    map[*serviceRepositoryChoiceJob]struct{}
	closed     bool
	cleanupErr error
}

func (s *app) serviceRepositoryChoiceScope() serviceRepositoryChoiceScope {
	state := s.serviceEditor
	if state == nil {
		return serviceRepositoryChoiceScope{}
	}
	repositoryID := strings.TrimSpace(*s.serviceEditorText("repositoryId", mapString(state.service, "repositoryId")))
	_, root := s.repositoryDetails(repositoryID)
	return serviceRepositoryChoiceScope{s.snapshot.Home, state.session, state.groupID, state.serviceID, repositoryID, root}
}

func (s *app) serviceRepositoryChoiceReadCurrent(kind, remote string) bool {
	jobs := s.serviceRepositoryChoices
	if jobs == nil {
		return false
	}
	jobs.mu.Lock()
	request := jobs.current[kind]
	closed := jobs.closed
	jobs.mu.Unlock()
	return !closed && request != nil && request.remote == remote && request.ctx.Err() == nil && request.editor == s.serviceEditor && request.scope == s.serviceRepositoryChoiceScope()
}

func (s *app) observeServiceRepositoryChoiceScope() {
	state := s.serviceEditor
	if state == nil || !s.dialogOpen {
		s.cancelServiceRepositoryChoiceReads()
		return
	}
	scope := s.serviceRepositoryChoiceScope()
	if state.repositoryScopeSet && state.repositoryScope != scope {
		s.cancelServiceRepositoryChoiceReads()
		state.remotes, state.branches, state.remoteAddresses = nil, nil, nil
		state.remotesLoaded, state.addressesLoaded = false, false
		state.branchChoices = make(map[string][]string)
		state.branchChoicesLoaded = make(map[string]bool)
		state.branchChoicesFailed = make(map[string]bool)
		state.pendingChoiceRemotes = nil
		state.choicesError, state.addressesError = "", ""
	}
	state.repositoryScope, state.repositoryScopeSet = scope, true
}

func clearServiceRepositoryChoiceLoading(request *serviceRepositoryChoiceJob) {
	if request == nil || request.editor == nil {
		return
	}
	state := request.editor
	key := payloadString(request.action.Payload, "requestKey")
	if request.action.Kind == "repository.choices" && state.choicesRequest == key {
		state.choicesRequest, state.choicesLoading = "", false
	}
	if request.action.Kind == "repository.addresses" && state.addressesRequest == key {
		state.addressesRequest, state.addressesLoading = "", false
	}
}

func (s *app) cancelServiceRepositoryChoiceSlot(kind string) {
	jobs := s.serviceRepositoryChoices
	if kind == "repository.choices" && s.serviceEditor != nil {
		s.serviceEditor.pendingChoiceRemotes = nil
	}
	if jobs == nil {
		return
	}
	jobs.mu.Lock()
	request := jobs.current[kind]
	delete(jobs.current, kind)
	jobs.mu.Unlock()
	if request != nil {
		request.cancel()
		clearServiceRepositoryChoiceLoading(request)
	}
}

func (s *app) cancelServiceRepositoryChoiceReads() {
	s.cancelServiceRepositoryChoiceSlot("repository.choices")
	s.cancelServiceRepositoryChoiceSlot("repository.addresses")
}

func (s *app) startServiceRepositoryChoiceRead(action application.Action) {
	if s.serviceRepositoryChoices == nil {
		s.serviceRepositoryChoices = &serviceRepositoryChoiceJobs{current: make(map[string]*serviceRepositoryChoiceJob), running: make(map[*serviceRepositoryChoiceJob]struct{})}
	}
	jobs := s.serviceRepositoryChoices
	ctx, cancel := context.WithCancel(context.Background())
	request := &serviceRepositoryChoiceJob{action: action, scope: s.serviceRepositoryChoiceScope(), editor: s.serviceEditor,
		remote: payloadString(action.Payload, "remote"), ctx: ctx, cancel: cancel, done: make(chan struct{})}
	jobs.mu.Lock()
	if jobs.closed {
		jobs.mu.Unlock()
		cancel()
		clearServiceRepositoryChoiceLoading(request)
		return
	}
	jobs.current[action.Kind] = request
	jobs.running[request] = struct{}{}
	jobs.mu.Unlock()
	backend, win := s.backend, s.win
	if backend == nil || win == nil || win.IsDestroyed() {
		s.finishServiceRepositoryChoiceRead(jobs, request, application.ActionResult{}, errors.New("仓库读取后端或窗口不可用，请重试。"))
		completeServiceRepositoryChoiceRead(jobs, request, nil)
		return
	}
	win.Invalidate()
	go func() {
		result, err := backend.Dispatch(request.ctx, request.action)
		if request.ctx.Err() != nil {
			err = errors.Join(request.ctx.Err(), err)
		}
		if !win.IsDestroyed() {
			win.Update(func() { s.finishServiceRepositoryChoiceRead(jobs, request, result, err) })
		}
		completeServiceRepositoryChoiceRead(jobs, request, err)
	}()
}

func (s *app) finishServiceRepositoryChoiceRead(jobs *serviceRepositoryChoiceJobs, request *serviceRepositoryChoiceJob, result application.ActionResult, err error) {
	defer request.cancel()
	jobs.mu.Lock()
	current := !jobs.closed && jobs.current[request.action.Kind] == request
	if jobs.current[request.action.Kind] == request {
		delete(jobs.current, request.action.Kind)
	}
	jobs.mu.Unlock()
	if !current || request.ctx.Err() != nil || request.editor != s.serviceEditor || !s.dialogOpen || request.scope != s.serviceRepositoryChoiceScope() {
		clearServiceRepositoryChoiceLoading(request)
		return
	}
	if err != nil {
		clearServiceRepositoryChoiceLoading(request)
		if errors.Is(err, context.Canceled) {
			return
		}
		state := request.editor
		if request.action.Kind == "repository.choices" {
			if request.remote != "" {
				if state.branchChoicesFailed == nil {
					state.branchChoicesFailed = make(map[string]bool)
				}
				state.branchChoicesFailed[serviceEditorBranchChoiceKey(request.scope, request.remote)] = true
			}
			data := mapData(result.Data)
			if dataString(data, "repositoryId") == request.scope.repositoryID && dataString(data, "rootPath") == request.scope.rootPath && request.remote != "" && request.remote == state.activeRemote {
				if stale := mapsString(data["branches"]); len(stale) != 0 {
					state.branches = stale
				}
			}
			if request.remote == "" {
				state.choicesError = err.Error()
			}
			if state.section == "git" && !state.addressesLoaded {
				s.requestServiceRepositoryAddresses(false)
			}
		} else {
			state.addressesError = err.Error()
		}
		s.startNextServiceRepositoryChoice()
		return
	}
	// Reuse the existing result handler so healthy choices still load addresses.
	s.applyServiceActionResult(request.action, result)
	s.startNextServiceRepositoryChoice()
}

func completeServiceRepositoryChoiceRead(jobs *serviceRepositoryChoiceJobs, request *serviceRepositoryChoiceJob, err error) {
	jobs.mu.Lock()
	if process.IsCleanupFailure(err) {
		jobs.cleanupErr = errors.Join(jobs.cleanupErr, err)
	}
	delete(jobs.running, request)
	close(request.done)
	jobs.mu.Unlock()
}

// OnClosed calls stop on the window thread. Close also cancels every retired
// worker before joining, so a replaced backend cannot escape shutdown cleanup.
func (s *app) stopServiceRepositoryChoices() []*serviceRepositoryChoiceJob {
	jobs := s.serviceRepositoryChoices
	if jobs == nil {
		return nil
	}
	jobs.mu.Lock()
	jobs.closed = true
	running := make([]*serviceRepositoryChoiceJob, 0, len(jobs.running))
	for request := range jobs.running {
		request.cancel()
		running = append(running, request)
	}
	current := make([]*serviceRepositoryChoiceJob, 0, len(jobs.current))
	for _, request := range jobs.current {
		current = append(current, request)
	}
	clear(jobs.current)
	jobs.mu.Unlock()
	for _, request := range current {
		clearServiceRepositoryChoiceLoading(request)
	}
	return running
}

func (s *app) closeServiceRepositoryChoices(ctx context.Context) error {
	for _, request := range s.stopServiceRepositoryChoices() {
		select {
		case <-request.done:
		case <-ctx.Done():
			return fmt.Errorf("等待服务仓库读取清理：%w", ctx.Err())
		}
	}
	jobs := s.serviceRepositoryChoices
	if jobs == nil {
		return nil
	}
	jobs.mu.Lock()
	defer jobs.mu.Unlock()
	return jobs.cleanupErr
}
