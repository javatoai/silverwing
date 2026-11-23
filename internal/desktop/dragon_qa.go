package desktop

import (
	"context"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

type dragonQAScope struct {
	task           application.DragonQATask
	home, taskRoot string
}

type dragonQAServiceChoice struct {
	service  application.DragonQAService
	selected bool
}

type dragonQAViewState struct {
	scope        dragonQAScope
	open, loaded bool
	result       application.DragonQAState
	choices      []dragonQAServiceChoice
	remark       string
	syncComment  bool
	err          string
}

type dragonQAJob uint8

const (
	dragonQALoad dragonQAJob = iota + 1
	dragonQASubmit
	dragonQACheck
	dragonQAComment
)

type dragonQARunningJob struct {
	kind        dragonQAJob
	receipt     *application.DragonQAState
	optionalCtx context.Context
}

type dragonQAWindow interface {
	Update(func())
	IsDestroyed() bool
}

func (s *app) dragonQAAvailable(t task) bool {
	_, supported := s.backend.(application.DragonQABackend)
	return supported && application.OptionalCLIFeatureDragonQA.Available(s.integrationReady) && t.id != "" && !t.archived && s.page == "tasks" && s.tab == 0 && !s.workspaceReviewPageVisible()
}

func (s *app) dragonQAScopeFor(t task) dragonQAScope {
	return dragonQAScope{
		task: application.DragonQATask{TaskID: t.id, TaskPath: t.path, TaskCreatedAt: t.createdAt},
		home: s.snapshot.Home, taskRoot: s.configString("taskRoot"),
	}
}

func (s *app) dragonQACurrent(state *dragonQAViewState) bool {
	t := s.taskUnit.currentTask()
	return state != nil && state.open && s.taskUnit.state.dragonQA == state &&
		s.dragonQAAvailable(t) && state.scope == s.dragonQAScopeFor(t)
}

func (s *app) openDragonQA(t task) {
	current := s.taskUnit.currentTask()
	scope := s.dragonQAScopeFor(t)
	if !s.dragonQAAvailable(current) || scope != s.dragonQAScopeFor(current) {
		return
	}
	if s.dragonQACurrent(s.taskUnit.state.dragonQA) {
		return
	}
	state := &dragonQAViewState{scope: scope, open: true, syncComment: true}
	s.taskUnit.state.dragonQA = state
	if running := s.taskUnit.state.dragonQAJobs[state.scope]; running != nil {
		if running.receipt != nil && running.optionalCtx.Err() == nil {
			state.result, state.loaded = *running.receipt, true
		}
		return
	}
	s.requestDragonQAJob(state, dragonQALoad, "")
}

func (state *dragonQAViewState) canEdit() bool {
	return state.loaded && (state.result.Phase == "" || state.result.Phase == application.DragonQAFailed)
}

func (state *dragonQAViewState) canCheckApproval() bool {
	return state.loaded && (state.result.Phase == application.DragonQAUnknown || state.result.Phase == application.DragonQASubmitting)
}

func (state *dragonQAViewState) canRetryComment() bool {
	if !state.loaded || state.result.Phase != application.DragonQAApproved {
		return false
	}
	switch state.result.CommentStatus {
	case application.DragonQACommentPending, application.DragonQACommentFailed, application.DragonQACommentUnknown:
		return true
	default:
		return false
	}
}

func (state *dragonQAViewState) selectedServices() []application.DragonQAService {
	var services []application.DragonQAService
	for _, choice := range state.choices {
		if choice.selected {
			services = append(services, choice.service)
		}
	}
	return services
}

func (state *dragonQAViewState) canSubmit() bool {
	return state.canEdit() && domain.ParseFeishuWorkItemLink(state.result.RequirementURL) != nil && len(state.selectedServices()) != 0
}

func (state *dragonQAViewState) setServices(services []application.DragonQAService) {
	previous := make(map[application.DragonQAService]bool, len(state.choices))
	for _, choice := range state.choices {
		previous[choice.service] = choice.selected
	}
	seen := make(map[string]bool, len(services))
	state.choices = make([]dragonQAServiceChoice, 0, len(services))
	for _, service := range services {
		if service.ID == "" || seen[service.ID] {
			continue
		}
		seen[service.ID] = true
		selected, known := previous[service]
		state.choices = append(state.choices, dragonQAServiceChoice{service: service, selected: !known || selected})
	}
}

func (s *app) requestDragonQAJob(state *dragonQAViewState, job dragonQAJob, approvalID string) {
	if s.win != nil {
		s.startDragonQAJob(s.win, state, job, approvalID)
	}
}

func (s *app) startDragonQAJob(window dragonQAWindow, state *dragonQAViewState, job dragonQAJob, approvalID string) {
	if !s.dragonQACurrent(state) || s.taskUnit.state.dragonQAJobs[state.scope] != nil {
		return
	}
	backend := s.backend.(application.DragonQABackend)
	scope := state.scope
	var run func(context.Context) (application.DragonQAState, error)
	switch job {
	case dragonQALoad:
		run = func(ctx context.Context) (application.DragonQAState, error) {
			return backend.DragonQAStatus(ctx, scope.task)
		}
	case dragonQASubmit:
		if !state.canSubmit() {
			return
		}
		request := application.DragonQASubmitRequest{
			DragonQATask: scope.task, RequirementURL: state.result.RequirementURL,
			Services: state.selectedServices(), Remark: state.remark, SyncComment: state.syncComment,
		}
		run = func(ctx context.Context) (application.DragonQAState, error) {
			return backend.SubmitDragonQA(ctx, request)
		}
	case dragonQACheck:
		if !state.canCheckApproval() {
			return
		}
		run = func(ctx context.Context) (application.DragonQAState, error) {
			return backend.RecheckDragonQA(ctx, scope.task, approvalID)
		}
	case dragonQAComment:
		if !state.canRetryComment() {
			return
		}
		run = func(ctx context.Context) (application.DragonQAState, error) {
			return backend.RetryDragonQAComment(ctx, scope.task)
		}
	default:
		return
	}
	if s.taskUnit.state.dragonQAJobs == nil {
		s.taskUnit.state.dragonQAJobs = make(map[dragonQAScope]*dragonQARunningJob)
	}
	optionalCtx := s.integrationContext("dragon")
	running := &dragonQARunningJob{kind: job, optionalCtx: optionalCtx}
	if state.result.Phase == application.DragonQAApproved {
		receipt := state.result
		running.receipt = &receipt
	}
	s.taskUnit.state.dragonQAJobs[scope] = running
	state.err = ""
	ctx := context.Background()
	if job == dragonQALoad {
		ctx = optionalCtx
	}
	observeCtx, stopObservation := context.WithCancel(optionalCtx)
	if job == dragonQASubmit || job == dragonQACheck {
		go s.observeDragonQAReceipt(observeCtx, window, backend, scope, running)
	}
	go func() {
		// The backend owns the durable record. Closing the dialog or changing
		// tasks releases the presentation while the operation finishes.
		result, err := run(ctx)
		stopObservation()
		if !window.IsDestroyed() {
			window.Update(func() { s.finishDragonQAJob(scope, running, result, err) })
		}
	}()
}

func (s *app) observeDragonQAReceipt(ctx context.Context, window dragonQAWindow, backend application.DragonQABackend, scope dragonQAScope, running *dragonQARunningJob) {
	ticker := time.NewTicker(750 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ctx.Err() != nil || window.IsDestroyed() {
				return
			}
			// The approval is already saved while Meego synchronization runs.
			// This reads its local record so the link is available immediately.
			result, err := backend.DragonQAStatus(ctx, scope.task)
			if err == nil && result.TaskID == scope.task.TaskID && result.Phase == application.DragonQAApproved {
				window.Update(func() { s.showDragonQAReceipt(scope, running, result) })
				return
			}
		}
	}
}

func (s *app) showDragonQAReceipt(scope dragonQAScope, running *dragonQARunningJob, result application.DragonQAState) {
	if s.taskUnit.state.dragonQAJobs[scope] != running || running.optionalCtx.Err() != nil {
		return
	}
	running.receipt = &result
	state := s.taskUnit.state.dragonQA
	if s.dragonQACurrent(state) && state.scope == scope {
		state.result, state.loaded = result, true
	}
}

func (s *app) finishDragonQAJob(scope dragonQAScope, running *dragonQARunningJob, result application.DragonQAState, err error) {
	if s.taskUnit.state.dragonQAJobs[scope] != running {
		return
	}
	delete(s.taskUnit.state.dragonQAJobs, scope)
	state := s.taskUnit.state.dragonQA
	if !s.dragonQACurrent(state) || state.scope != scope {
		return
	}
	if running.optionalCtx.Err() != nil {
		s.requestDragonQAJob(state, dragonQALoad, "")
		return
	}
	if err != nil {
		state.err = err.Error()
	}
	if running.receipt != nil && (result.TaskID != scope.task.TaskID || result.Phase != application.DragonQAApproved) {
		result = *running.receipt
	}
	if result.TaskID != scope.task.TaskID || err != nil && result.Phase == "" {
		if err == nil {
			state.err = "返回的提测状态与当前任务不匹配。"
		}
		if running.kind == dragonQALoad || running.kind == dragonQASubmit {
			state.loaded = false
		}
		return
	}
	state.result, state.loaded = result, true
	if state.canEdit() {
		state.setServices(result.Services)
	}
}
