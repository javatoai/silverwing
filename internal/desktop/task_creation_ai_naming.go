package desktop

import (
	"context"
	"errors"
	"strings"

	"silverwing-go/internal/application"
)

// taskCreationAINamingState owns an AI naming request independently from the
// desktop's global action and cancellation state.
type taskCreationAINamingState struct {
	generation uint64
	request    *taskCreationAINamingRequest
	scope      taskCreationAINamingScope
	hasScope   bool
	closed     bool
}

type taskCreationAINamingScope struct {
	token, home, link, config string
	identity                  requirementListIdentity
	identityContext           requirementIdentityContext
}

type taskCreationAINamingRequest struct {
	form       *taskCreationState
	token      string
	home       string
	link       string
	config     string
	identity   requirementListIdentity
	profile    requirementIdentityContext
	account    string
	accountSet bool
	generation uint64
	ctx        context.Context
	cancel     context.CancelFunc
	action     application.Action
}

// ensureTaskCreationAINamingScope retires a request when its form, link,
// configuration, home directory, or Meegle identity changes. Name and branch
// edits intentionally do not change this scope: ApplyAINaming preserves them.
func (s *app) ensureTaskCreationAINamingScope() *taskCreationAINamingState {
	if s == nil || s.taskUnit.state.taskCreation == nil {
		return nil
	}
	form := s.taskUnit.state.taskCreation
	state := form.aiNaming
	if state == nil {
		state = &taskCreationAINamingState{}
		form.aiNaming = state
	}
	if state.closed {
		return state
	}
	next := s.taskCreationAINamingScope(form)
	if !state.hasScope {
		state.scope, state.hasScope = next, true
		return state
	}
	if state.scope != next {
		s.taskUnit.cancelTaskCreationAINamingRequest(form, state)
		state.scope = next
	}
	return state
}

func (s *app) taskCreationAINamingScope(form *taskCreationState) taskCreationAINamingScope {
	return taskCreationAINamingScope{
		token: form.token, home: s.snapshot.Home, link: form.draft.RequirementLink,
		config:   taskCreationRequirementsConfigFingerprint(s.snapshot.ConfigJSON, form.config),
		identity: s.currentRequirementListIdentity(), identityContext: s.currentRequirementIdentityContext(),
	}
}

func (s *app) requestTaskCreationAINamingJob(force bool) {
	form := s.taskUnit.state.taskCreation
	if form == nil || !form.aiEligible || !form.config.AIRequirementNamingEnabled || strings.TrimSpace(form.draft.RequirementLink) == "" {
		return
	}
	state := s.ensureTaskCreationAINamingScope()
	if state.closed {
		return
	}
	if identity := s.requirementUnit.state.requirementIdentityJobs; identity != nil && identity.hasContext && identity.context != state.scope.identityContext {
		s.resetRequirementIdentityContext(identity, state.scope.identityContext)
		state = s.ensureTaskCreationAINamingScope()
	}

	scope := state.scope
	link := scope.link
	action := application.Action{Kind: "requirements.ai-name", Payload: map[string]any{
		"requirementLink": link, "requestLink": link, "taskCreateToken": form.token, "force": force,
	}}
	if s.backend == nil {
		s.taskUnit.cancelTaskCreationAINamingRequest(form, state)
		s.taskCreationActionError(action, errors.New("AI 命名服务不可用。"))
		return
	}
	account, accountSet, identityErr := taskCreationExpectedRequirementIdentity(s)
	if identityErr != nil {
		s.taskUnit.cancelTaskCreationAINamingRequest(form, state)
		s.taskCreationActionError(action, identityErr)
		return
	}
	if cached, ok := s.backend.(application.RequirementNamingCacheBackend); ok && accountSet && !force {
		suggestion, found, err := cached.CachedRequirementAINaming(context.Background(), link, account)
		if s.taskUnit.state.taskCreation != form || state.closed || scope != s.taskCreationAINamingScope(form) {
			s.taskUnit.cancelTaskCreationAINamingRequest(form, state)
			return
		}
		if err != nil || found {
			s.taskUnit.cancelTaskCreationAINamingRequest(form, state)
			if err != nil {
				s.taskCreationAINamingError(action, err)
			} else {
				s.taskCreationActionResult(action, application.ActionResult{Data: suggestion})
			}
			return
		}
	}
	if !force && state.request != nil {
		return
	}
	if s.win == nil || s.win.IsDestroyed() {
		return
	}

	s.taskUnit.cancelTaskCreationAINamingRequest(form, state)
	ctx, cancel := context.WithCancel(context.Background())
	request := &taskCreationAINamingRequest{
		form: form, token: scope.token, home: scope.home, link: link, config: scope.config,
		identity: scope.identity, profile: scope.identityContext,
		account: account, accountSet: accountSet, generation: state.generation,
		ctx: ctx, cancel: cancel, action: action,
	}
	state.request = request
	form.aiPending = true
	form.aiError = ""
	backend, win := s.backend, s.win
	go func() {
		defer cancel()
		result, err := runTaskCreationAINamingRequest(backend, request)
		if ctx.Err() != nil || win.IsDestroyed() {
			return
		}
		updated := make(chan struct{})
		win.Update(func() {
			defer close(updated)
			s.finishTaskCreationAINaming(request, result, err)
		})
		select {
		case <-updated:
		case <-ctx.Done():
		}
	}()
}

// A valid suggestion is returned even when its folder is occupied. Directory
// validation belongs to the form and must never cause another AI request.
func runTaskCreationAINamingRequest(backend application.Backend, request *taskCreationAINamingRequest) (application.ActionResult, error) {
	ctx := request.ctx
	_, cacheAware := backend.(application.RequirementNamingCacheBackend)
	account, confirmed := request.account, request.accountSet
	if !cacheAware || !confirmed {
		var err error
		account, confirmed, err = taskCreationRequirementIdentityBeforeRead(ctx, backend, account, confirmed)
		if err != nil {
			return application.ActionResult{}, err
		}
	}
	action := request.action
	action.Payload = taskCreationActionPayload(action.Payload, request.token)
	if confirmed {
		action.Payload["expectedIdentity"] = account
	}
	result, err := backend.Dispatch(ctx, action)
	if err == nil && confirmed && !cacheAware {
		err = taskCreationRequirementIdentityAfterRead(ctx, backend, account)
	}
	if ctx.Err() != nil {
		return application.ActionResult{}, errors.Join(ctx.Err(), err)
	}
	return result, err
}

func (s *app) finishTaskCreationAINaming(request *taskCreationAINamingRequest, result application.ActionResult, requestErr error) {
	if !s.taskCreationAINamingRequestRelevant(request) {
		if s != nil && request != nil && s.taskUnit.state.taskCreation == request.form && request.form.aiNaming != nil && request.form.aiNaming.request == request {
			s.taskUnit.cancelTaskCreationAINamingRequest(request.form, request.form.aiNaming)
		}
		return
	}
	state := request.form.aiNaming
	state.request = nil
	if requestErr != nil {
		s.taskCreationAINamingError(request.action, requestErr)
		return
	}
	s.taskCreationActionResult(request.action, result)
}

func (s *app) taskCreationAINamingError(action application.Action, err error) {
	var changed *application.RequirementIdentityChangedError
	if errors.As(err, &changed) {
		s.applyRequirementListIdentityFailure(changed.Snapshot, err)
	}
	s.taskCreationActionError(action, err)
}

func (s *app) taskCreationAINamingRequestRelevant(request *taskCreationAINamingRequest) bool {
	if s == nil || request == nil || request.ctx == nil || request.ctx.Err() != nil || s.win == nil || s.win.IsDestroyed() {
		return false
	}
	form := s.taskUnit.state.taskCreation
	if form == nil || form != request.form || form.aiNaming == nil || form.aiNaming.closed ||
		form.token != request.token || s.snapshot.Home != request.home || form.draft.RequirementLink != request.link ||
		taskCreationRequirementsConfigFingerprint(s.snapshot.ConfigJSON, form.config) != request.config ||
		s.currentRequirementListIdentity() != request.identity || s.currentRequirementIdentityContext() != request.profile {
		return false
	}
	state := form.aiNaming
	return state.request == request && state.generation == request.generation
}

func (s *taskController) cancelTaskCreationAINamingRequest(form *taskCreationState, state *taskCreationAINamingState) {
	if state == nil {
		if form != nil {
			form.aiPending = false
		}
		return
	}
	state.generation++
	if state.request != nil {
		state.request.cancel()
		state.request = nil
	}
	if form != nil {
		form.aiPending = false
	}
}

func (s *taskController) cancelTaskCreationAINaming() {
	if s == nil || s.state.taskCreation == nil {
		return
	}
	form := s.state.taskCreation
	s.cancelTaskCreationAINamingRequest(form, form.aiNaming)
}

func (s *taskController) closeTaskCreationAINaming() {
	if s == nil || s.state.taskCreation == nil {
		return
	}
	form := s.state.taskCreation
	state := form.aiNaming
	if state != nil {
		state.closed = true
	}
	s.cancelTaskCreationAINamingRequest(form, state)
	form.pendingPreflight = false
}
