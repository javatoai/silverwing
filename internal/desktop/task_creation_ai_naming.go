package desktop

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

const taskCreationAINamingMaxAttempts = 3

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
	taskRoot   string
	attempt    int
	generation uint64
	ctx        context.Context
	cancel     context.CancelFunc
	action     application.Action
}

// ensureTaskCreationAINamingScope retires a request when its form, link,
// configuration, home directory, or Meegle identity changes. Name and branch
// edits intentionally do not change this scope: ApplyAINaming preserves them.
func (s *app) ensureTaskCreationAINamingScope() *taskCreationAINamingState {
	if s == nil || s.taskCreation == nil {
		return nil
	}
	form := s.taskCreation
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
		s.cancelTaskCreationAINamingRequest(form, state)
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
	form := s.taskCreation
	if form == nil {
		return
	}
	state := s.ensureTaskCreationAINamingScope()
	if state == nil || state.closed || !form.aiEligible || !form.config.AIRequirementNamingEnabled || strings.TrimSpace(form.draft.RequirementLink) == "" {
		return
	}

	link := form.draft.RequirementLink
	action := application.Action{Kind: "requirements.ai-name", Payload: map[string]any{
		"requirementLink": link, "requestLink": link, "taskCreateToken": form.token, "force": force,
	}}
	s.cancelTaskCreationAINamingRequest(form, state)
	if s.backend == nil {
		s.taskCreationActionError(action, errors.New("AI 命名服务不可用。"))
		return
	}
	if s.win == nil || s.win.IsDestroyed() {
		return
	}
	account, accountSet, identityErr := taskCreationExpectedRequirementIdentity(s)
	if identityErr != nil {
		form.aiPending = true
		form.aiError = ""
		s.taskCreationActionError(action, identityErr)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	taskRoot := ""
	if form.config.TaskRoot != nil {
		taskRoot = *form.config.TaskRoot
	}
	request := &taskCreationAINamingRequest{
		form: form, token: form.token, home: s.snapshot.Home, link: link,
		config:   taskCreationRequirementsConfigFingerprint(s.snapshot.ConfigJSON, form.config),
		identity: s.currentRequirementListIdentity(), profile: s.currentRequirementIdentityContext(),
		account: account, accountSet: accountSet, generation: state.generation,
		taskRoot: taskRoot, attempt: 1,
		ctx: ctx, cancel: cancel, action: action,
	}
	state.request = request
	form.aiPending = true
	form.aiError = ""
	backend, win := s.backend, s.win
	go func() {
		// Each retry returns to the window thread before dispatch. Form replacement
		// and identity retirement must stop retries as well as reject their results.
		result, err := runTaskCreationAINamingRequest(backend, request, func(attempt int) bool {
			if ctx.Err() != nil || win.IsDestroyed() {
				cancel()
				return false
			}
			checked := make(chan bool, 1)
			win.Update(func() {
				current := s.taskCreationAINamingRequestRelevant(request)
				if current {
					request.attempt = attempt
				}
				checked <- current
			})
			select {
			case current := <-checked:
				if !current {
					cancel()
				}
				return current
			case <-ctx.Done():
				return false
			}
		})
		if ctx.Err() != nil {
			err = errors.Join(ctx.Err(), err)
		}
		if win.IsDestroyed() {
			cancel()
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
		cancel()
	}()
}

// The window confirms ownership between attempts; all remote reads and local
// existence checks stay on the request worker. Only occupied suggestions retry.
func runTaskCreationAINamingRequest(backend application.Backend, request *taskCreationAINamingRequest, current func(int) bool) (application.ActionResult, error) {
	ctx := request.ctx
	account, confirmed := request.account, request.accountSet
	force, _ := request.action.Payload["force"].(bool)
	forbidden := []string{}
	for attempt := 1; attempt <= taskCreationAINamingMaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return application.ActionResult{}, err
		}
		if attempt > 1 && !current(attempt) {
			return application.ActionResult{}, context.Canceled
		}
		nextAccount, nextConfirmed, err := taskCreationRequirementIdentityBeforeRead(ctx, backend, account, confirmed)
		if err != nil {
			return application.ActionResult{}, err
		}
		account, confirmed = nextAccount, nextConfirmed
		if err := ctx.Err(); err != nil {
			return application.ActionResult{}, err
		}
		action := request.action
		action.Payload = taskCreationActionPayload(request.action.Payload, request.token)
		action.Payload["force"] = force || attempt > 1
		// Every service call owns its history snapshot, including the empty first
		// one. Later collisions cannot mutate an earlier request's payload.
		action.Payload["forbiddenFolderNames"] = append([]string{}, forbidden...)
		if confirmed {
			action.Payload["expectedIdentity"] = account
		}
		result, err := backend.Dispatch(ctx, action)
		if err == nil && confirmed {
			err = taskCreationRequirementIdentityAfterRead(ctx, backend, account)
		}
		if ctx.Err() != nil {
			return application.ActionResult{}, errors.Join(ctx.Err(), err)
		}
		if err != nil {
			return application.ActionResult{}, err
		}
		suggestion, decodeErr := decodeTaskCreationResult[domain.RequirementAiNamingSuggestion](result.Data)
		if decodeErr != nil {
			// Preserve the form's existing format-error and validation messages.
			return result, nil
		}
		validated, validationErr := domain.ValidateRequirementAiNamingSuggestion(suggestion)
		if validationErr != nil {
			return result, nil
		}
		occupied := taskCreationAINamingFolderExists(request.taskRoot, validated.FolderName)
		if err := ctx.Err(); err != nil {
			return application.ActionResult{}, err
		}
		if !occupied {
			return result, nil
		}
		if !slices.Contains(forbidden, validated.FolderName) {
			forbidden = append(forbidden, validated.FolderName)
		}
	}
	return application.ActionResult{}, errors.New("AI 生成的文件夹名已存在，请手工修改或重新生成")
}

func taskCreationAINamingFolderExists(root, folder string) bool {
	if strings.TrimSpace(root) == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(root, folder))
	return err == nil
}

func (s *app) finishTaskCreationAINaming(request *taskCreationAINamingRequest, result application.ActionResult, requestErr error) {
	if !s.taskCreationAINamingRequestRelevant(request) {
		if s != nil && request != nil && s.taskCreation == request.form && request.form.aiNaming != nil && request.form.aiNaming.request == request {
			s.cancelTaskCreationAINamingRequest(request.form, request.form.aiNaming)
		}
		return
	}
	state := request.form.aiNaming
	state.request = nil
	if requestErr != nil {
		var changed *application.RequirementIdentityChangedError
		if errors.As(requestErr, &changed) {
			s.applyRequirementListIdentityFailure(changed.Snapshot, requestErr)
		}
		s.taskCreationActionError(request.action, requestErr)
		return
	}
	s.taskCreationActionResult(request.action, result)
}

func (s *app) taskCreationAINamingRequestRelevant(request *taskCreationAINamingRequest) bool {
	if s == nil || request == nil || request.ctx == nil || request.ctx.Err() != nil || s.win == nil || s.win.IsDestroyed() {
		return false
	}
	form := s.taskCreation
	if form == nil || form != request.form || form.aiNaming == nil || form.aiNaming.closed ||
		form.token != request.token || s.snapshot.Home != request.home || form.draft.RequirementLink != request.link ||
		taskCreationRequirementsConfigFingerprint(s.snapshot.ConfigJSON, form.config) != request.config ||
		s.currentRequirementListIdentity() != request.identity || s.currentRequirementIdentityContext() != request.profile {
		return false
	}
	state := form.aiNaming
	return state.request == request && state.generation == request.generation
}

func (s *app) cancelTaskCreationAINamingRequest(form *taskCreationState, state *taskCreationAINamingState) {
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

func (s *app) cancelTaskCreationAINaming() {
	if s == nil || s.taskCreation == nil {
		return
	}
	form := s.taskCreation
	s.cancelTaskCreationAINamingRequest(form, form.aiNaming)
}

func (s *app) closeTaskCreationAINaming() {
	if s == nil || s.taskCreation == nil {
		return
	}
	form := s.taskCreation
	state := form.aiNaming
	if state != nil {
		state.closed = true
	}
	s.cancelTaskCreationAINamingRequest(form, state)
	form.pendingPreflight = false
}
