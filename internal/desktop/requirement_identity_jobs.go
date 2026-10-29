package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"silverwing-go/internal/application"
)

func (s *app) observeRequirementIdentityJobs() {
	if _, ok := s.backend.(application.RequirementIdentityBackend); !ok {
		return
	}
	state := s.ensureRequirementIdentityJobs()
	entered := s.page == "requirements" && state.page != "requirements"
	state.page = s.page
	if state.hasContext && (state.context.home != s.snapshot.Home || state.context.executable != strings.TrimSpace(s.configString("meegleExecutablePath"))) {
		s.resetRequirementIdentityContext(state, s.currentRequirementIdentityContext())
	}
	s.observeRequirementListIdentity()
	// A normal re-entry reads the account-bound persistent list again, allowing
	// its source-compatible TTL to be enforced by the backend. Bootstrap that
	// same deduplicated read once for the sidebar, without requiring page entry.
	if (entered || !s.requirementUnit.state.requirementsRequested) && !s.loading && len(s.readConfigArray("meegleProjects")) > 0 {
		s.requestRequirements("", false)
	}
}

func (s *app) onRequirementWindowFocused() {
	if s.loading || len(s.readConfigArray("meegleProjects")) == 0 {
		return
	}
	if _, ok := s.backend.(application.RequirementIdentityBackend); !ok {
		return
	}
	state := s.ensureRequirementIdentityJobs()
	next := s.currentRequirementIdentityContext()
	if state.hasContext && state.context != next {
		s.resetRequirementIdentityContext(state, next)
	} else {
		state.context, state.hasContext = next, true
	}
	s.startRequirementIdentityProbe(state)
}

// Source shares an application-owned probe between consumers. Sprint changes
// replace only the pending list intent; they don't duplicate an identity read.
func (s *app) queueRequirementListIdentity(sprintKey string, refresh bool) bool {
	if _, ok := s.backend.(application.RequirementIdentityBackend); !ok {
		return false
	}
	state := s.ensureRequirementIdentityJobs()
	next := s.currentRequirementIdentityContext()
	if state.hasContext && state.context != next {
		s.resetRequirementIdentityContext(state, next)
		sprintKey = ""
	} else {
		state.context, state.hasContext = next, true
	}
	if !refresh && state.confirmed && state.current == nil {
		if state.state == application.RequirementIdentityLoggedOut {
			s.requirementUnit.state.requirementsRequested, s.requirementUnit.state.requirementsLoading = true, false
			s.requirementUnit.state.requirementsLoadError = "Meegle 已退出登录，请重新登录后读取需求。"
			return true
		}
		return false
	}
	list := s.ensureRequirementListJobs()
	s.retireRequirementListRequest(list)
	list.generation++
	state.pending = &requirementIdentityListRequest{sprintKey: sprintKey, refresh: refresh}
	if p := s.requirementUnit.state.requirementListProgress; refresh && s.requirementListProgressCurrent(p) && p.running {
		state.pending.progress = p
	}
	s.requirementUnit.state.requirementsRequested, s.requirementUnit.state.requirementsLoading = true, true
	s.requirementUnit.state.requirementsLoadError = ""
	s.startRequirementIdentityProbe(state)
	return true
}

func (s *app) startRequirementIdentityProbe(state *requirementIdentityJobState) {
	if state.current != nil {
		return
	}
	provider, ok := s.backend.(application.RequirementIdentityBackend)
	if !ok || s.win == nil || s.win.IsDestroyed() {
		s.requirementUnit.state.requirementsLoading = false
		s.requirementUnit.state.requirementsLoadError = "需求身份读取后端或窗口不可用，请重试。"
		if state.pending != nil {
			s.failRequirementListProgress(state.pending.progress, []string{s.requirementUnit.state.requirementsLoadError})
		}
		return
	}
	state.mu.Lock()
	closed := state.closed
	state.mu.Unlock()
	if closed {
		s.requirementUnit.state.requirementsLoading = false
		if state.pending != nil {
			s.failRequirementListProgress(state.pending.progress, []string{"需求身份读取已关闭。"})
		}
		return
	}
	state.generation++
	ctx, cancel := context.WithCancel(context.Background())
	request := &requirementIdentityJob{generation: state.generation, context: state.context, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	state.current = request
	state.mu.Lock()
	state.running[request] = struct{}{}
	state.mu.Unlock()
	win := s.win
	go func() {
		result, err := provider.RequirementIdentity(ctx)
		if !win.IsDestroyed() {
			updated := make(chan struct{})
			win.Update(func() {
				defer close(updated)
				s.finishRequirementIdentityProbe(state, request, result, err)
			})
			select {
			case <-updated:
			case <-ctx.Done():
			}
		}
		cancel()
		state.mu.Lock()
		delete(state.running, request)
		close(request.done)
		state.mu.Unlock()
	}()
}

func (s *app) finishRequirementIdentityProbe(state *requirementIdentityJobState, request *requirementIdentityJob, result application.RequirementIdentitySnapshot, probeErr error) {
	state.mu.Lock()
	closed := state.closed
	state.mu.Unlock()
	if closed || state.current != request || state.generation != request.generation || request.ctx.Err() != nil || state.context != request.context ||
		s.snapshot.Home != request.context.home || strings.TrimSpace(s.configString("meegleExecutablePath")) != request.context.executable {
		return
	}
	if next := s.currentRequirementIdentityContext(); next != request.context {
		s.resetRequirementIdentityContext(state, next)
		return
	}
	state.current = nil
	pending := state.pending
	state.pending = nil
	if probeErr != nil {
		if errors.Is(probeErr, context.Canceled) {
			return
		}
		result = application.RequirementIdentitySnapshot{State: application.RequirementIdentityUnavailable}
	}
	if result.State == application.RequirementIdentityAuthenticated && strings.TrimSpace(result.Identity) == "" {
		result.State = application.RequirementIdentityUnavailable
	}
	if result.State == application.RequirementIdentityAuthenticated || result.State == application.RequirementIdentityLoggedOut {
		changed := state.confirmed && (state.identity != result.Identity || state.state != result.State)
		state.confirmed, state.identity, state.state = true, result.Identity, result.State
		if changed || result.State == application.RequirementIdentityLoggedOut {
			s.clearMeegleIdentityBoundViews()
		}
	}
	// Unavailable non-strict checks preserve the last confirmed snapshot.
	if pending == nil {
		if result.State == application.RequirementIdentityLoggedOut {
			s.requirementUnit.state.requirementsRequested, s.requirementUnit.state.requirementsLoading = true, false
			s.requirementUnit.state.requirementsLoadError = "Meegle 已退出登录，请重新登录后读取需求。"
		}
		return
	}
	if result.State != application.RequirementIdentityAuthenticated && result.State != application.RequirementIdentityLoggedOut && pending.refresh {
		s.requirementUnit.state.requirementsRequested, s.requirementUnit.state.requirementsLoading, s.requirementUnit.state.requirementsLoaded = true, false, true
		s.requirementUnit.state.requirementsLoadError = "无法确认当前 Meegle 账户，请重试；本地需求已保留。"
		s.failRequirementListProgress(pending.progress, []string{s.requirementUnit.state.requirementsLoadError})
		return
	}
	if state.confirmed && state.state == application.RequirementIdentityLoggedOut {
		s.requirementUnit.state.requirementsRequested, s.requirementUnit.state.requirementsLoading = true, false
		s.requirementUnit.state.requirementsLoadError = "Meegle 已退出登录，请重新登录后读取需求。"
		return
	}
	if !state.confirmed {
		s.clearRequirementListView()
	}
	s.startRequirementListWithKnownIdentity(pending.sprintKey, pending.refresh)
}

// Account changes invalidate retained list and reader results, rather than
// entering the ordinary transient-query failure path which keeps old items.
func (s *app) applyRequirementListIdentityFailure(snapshot application.RequirementIdentitySnapshot, err error) {
	state := s.ensureRequirementIdentityJobs()
	if state.current != nil {
		state.current.cancel()
		state.current = nil
	}
	state.generation++
	state.pending = nil
	state.confirmed = snapshot.State == application.RequirementIdentityAuthenticated || snapshot.State == application.RequirementIdentityLoggedOut
	state.identity, state.state = snapshot.Identity, snapshot.State
	s.clearMeegleIdentityBoundViews()
	s.requirementUnit.state.requirementsLoadError = err.Error()
	s.requirementUnit.state.requirementsRequested = snapshot.State != application.RequirementIdentityAuthenticated
	s.requirementUnit.state.requirementsLoading = false
}

func (s *requirementController) closeRequirementIdentityJobs(ctx context.Context) error {
	state := s.state.requirementIdentityJobs
	if state == nil {
		return nil
	}
	state.mu.Lock()
	state.closed = true
	running := make([]*requirementIdentityJob, 0, len(state.running))
	for request := range state.running {
		request.cancel()
		running = append(running, request)
	}
	state.mu.Unlock()
	for _, request := range running {
		select {
		case <-request.done:
		case <-ctx.Done():
			return fmt.Errorf("等待需求身份读取清理：%w", ctx.Err())
		}
	}
	return nil
}
