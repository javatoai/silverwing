package desktop

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"silverwing-go/internal/application"
)

type skillsBrowserRead struct {
	action application.Action
	home   string
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
}

// The window owns current requests; workers share only the cleanup set.
type skillsBrowserJobs struct {
	current map[string]*skillsBrowserRead
	mu      sync.Mutex
	running map[*skillsBrowserRead]struct{}
	closed  bool
}

func (s *app) observeSkillsBrowserNavigation() {
	if !s.localSkillsViewActive() {
		s.leaveLocalSkillsView()
		return
	}
	state := s.ensureSkillsBrowser()
	if !s.loading && !state.pageActive {
		state.pageActive = true
		if !state.loadingCatalog && (!state.catalogLoaded || state.catalogStale) {
			s.requestSkillsCatalog()
		}
	}
}

func (s *app) leaveLocalSkillsView() {
	state := s.skillsBrowser
	if state == nil || (!state.pageActive && state.pendingCatalog == "" && state.pendingFiles == "" && state.pendingPreview == "") {
		return
	}
	state.pageActive = false
	state.catalogStale = true
	s.cancelSkillsBrowserRead("skills.catalog")
	s.cancelSkillsBrowserRead("skills.files/list")
	s.cancelSkillsBrowserRead("skills.files/preview")
	state.requestSequence++ // Invalidate callbacks already queued on the UI thread.
	state.pendingCatalog = ""
	state.pendingFiles = ""
	state.pendingPreview = ""
	state.catalogRefreshAfterPending = false
	state.loadingCatalog = false
	state.loadingFiles = false
	state.loadingPreview = false
}

func (s *app) cancelSkillsBrowserRead(kind string) {
	if state := s.skillsBrowser; state != nil && state.jobs != nil {
		if request := state.jobs.current[kind]; request != nil {
			request.cancel()
			delete(state.jobs.current, kind)
		}
	}
}

func skillsBrowserReadAction(action application.Action) bool {
	if payloadString(action.Payload, "browserRequestID") == "" {
		return false
	}
	switch action.Kind {
	case "skills.catalog", "skills.files/list", "skills.files/preview":
		return true
	default:
		return false
	}
}

func (s *app) dispatchSkillsBrowserRead(action application.Action) bool {
	if !skillsBrowserReadAction(action) {
		return false
	}
	state := s.ensureSkillsBrowser()
	if !s.localSkillsViewActive() {
		state.catalogStale = true
		switch action.Kind {
		case "skills.catalog":
			if state.pendingCatalog == payloadString(action.Payload, "browserRequestID") {
				state.pendingCatalog = ""
				state.loadingCatalog = false
			}
		case "skills.files/list":
			if state.pendingFiles == payloadString(action.Payload, "browserRequestID") {
				state.pendingFiles = ""
				state.loadingFiles = false
			}
		case "skills.files/preview":
			if state.pendingPreview == payloadString(action.Payload, "browserRequestID") {
				state.pendingPreview = ""
				state.loadingPreview = false
			}
		}
		return true
	}
	if s.backend == nil || s.win == nil || s.win.IsDestroyed() {
		state.handleFailure(action, errors.New("Skills 读取后端或窗口不可用，请重试。"))
		return true
	}
	if state.jobs == nil {
		state.jobs = &skillsBrowserJobs{current: make(map[string]*skillsBrowserRead), running: make(map[*skillsBrowserRead]struct{})}
	}
	jobs := state.jobs
	jobs.mu.Lock()
	closed := jobs.closed
	jobs.mu.Unlock()
	if closed {
		state.handleFailure(action, errors.New("Skills 读取已关闭。"))
		return true
	}
	s.cancelSkillsBrowserRead(action.Kind)
	ctx, cancel := context.WithCancel(context.Background())
	request := &skillsBrowserRead{action: action, home: s.snapshot.Home, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	jobs.current[action.Kind] = request
	jobs.mu.Lock()
	jobs.running[request] = struct{}{}
	jobs.mu.Unlock()
	backend, win := s.backend, s.win
	win.Invalidate()
	go func() {
		result, err := backend.Dispatch(ctx, action)
		if !win.IsDestroyed() {
			updated := make(chan struct{})
			win.Update(func() {
				defer close(updated)
				jobs.mu.Lock()
				closed := jobs.closed
				jobs.mu.Unlock()
				if closed || ctx.Err() != nil || jobs.current[action.Kind] != request {
					return
				}
				delete(jobs.current, action.Kind)
				if s.snapshot.Home != request.home {
					state.handleFailure(action, errors.New("应用目录已变化，请重新读取 Skills。"))
					return
				}
				if !s.localSkillsViewActive() {
					return
				}
				if err != nil {
					state.handleFailure(action, err)
				} else {
					state.handleResult(action, result)
				}
			})
			select {
			case <-updated:
			case <-ctx.Done():
			}
		}
		cancel()
		jobs.mu.Lock()
		delete(jobs.running, request)
		close(request.done)
		jobs.mu.Unlock()
	}()
	return true
}

func (s *app) closeSkillsBrowserReads(ctx context.Context) error {
	state := s.skillsBrowser
	if state == nil || state.jobs == nil {
		return nil
	}
	jobs := state.jobs
	jobs.mu.Lock()
	jobs.closed = true
	running := make([]*skillsBrowserRead, 0, len(jobs.running))
	for request := range jobs.running {
		request.cancel()
		running = append(running, request)
	}
	jobs.mu.Unlock()
	for _, request := range running {
		select {
		case <-request.done:
		case <-ctx.Done():
			return fmt.Errorf("等待 Skills 读取清理：%w", ctx.Err())
		}
	}
	return nil
}
