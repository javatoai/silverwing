package desktop

import (
	"fmt"

	"silverwing-go/internal/application"

	"github.com/egoist/mygo/ui"
)

// This session belongs to a manual list refresh, including its identity probe.
// Hiding its dialog has no effect on the worker's cancellation handle.
type requirementListProgressState struct {
	open, running              bool
	sprintKey, sprintTitle     string
	viewSprintKey              string // A current result may clear the selection without changing the retry target.
	identity                   requirementListIdentity
	profile                    requirementIdentityContext
	account                    string
	request                    *requirementListJob
	steps                      map[string]application.RequirementListProgress
	lastSequence               uint64
	failures                   []string
	message, notificationError string
	scroll                     ui.ScrollState
}

func (s *app) requirementListProgressCurrent(p *requirementListProgressState) bool {
	if p == nil || s.requirementListProgress != p || p.viewSprintKey != s.requirementsSelectedSprintKey {
		return false
	}
	now := s.currentRequirementListIdentity()
	if p.identity.home != now.home || p.identity.executable != now.executable ||
		p.identity.projects != now.projects || p.identity.defaultProject != now.defaultProject {
		return false
	}
	if identity := s.requirementIdentityJobs; identity != nil {
		if identity.hasContext && identity.context != p.profile {
			return false
		}
		if p.account != "" && (!identity.confirmed || identity.identity != p.account) {
			return false
		}
	}
	return p.request == nil || p.identity == now
}

func (s *app) observeRequirementListProgress() {
	p := s.requirementListProgress
	if p == nil {
		return
	}
	if !s.requirementListProgressCurrent(p) {
		s.requirementListProgress = nil
		return
	}
	if s.page != "requirements" {
		p.open = false
	}
}

func (s *app) newRequirementListProgress(sprintKey string) *requirementListProgressState {
	p := &requirementListProgressState{
		open: true, running: true, sprintKey: sprintKey, viewSprintKey: sprintKey,
		sprintTitle: s.requirementSprintLabel(s.selectedRequirementSprint()),
		identity:    s.currentRequirementListIdentity(), profile: s.currentRequirementIdentityContext(),
		steps: map[string]application.RequirementListProgress{
			"account": {Phase: "account", Status: "running", Message: "正在核对 Meegle 账号…"},
		},
	}
	if identity := s.requirementIdentityJobs; identity != nil && identity.confirmed {
		p.account = identity.identity
	}
	s.requirementListProgress = p
	return p
}

func (s *app) refreshRequirementListWithProgress(sprintKey string) {
	s.observeRequirementListProgress()
	if sprintKey != s.requirementsSelectedSprintKey || len(s.readConfigArray("meegleProjects")) == 0 {
		return
	}
	// The failure panel also retries the initial account/Sprint catalogue read,
	// before there is a selected Sprint for a manual progress session.
	if sprintKey == "" {
		if p := s.requirementListProgress; s.requirementListProgressCurrent(p) && p.sprintKey != "" && !p.running && len(p.failures) > 0 {
			s.retryRequirementListProgress(p)
		} else if !s.requirementsLoading {
			s.requestRequirements("", true)
		}
		return
	}
	if p := s.requirementListProgress; p != nil && p.running && p.sprintKey == sprintKey {
		p.open = true
		return
	}
	if s.requirementsLoading {
		return
	}
	s.newRequirementListProgress(sprintKey)
	s.requestRequirements(sprintKey, true)
}

func (s *app) retryRequirementListProgress(p *requirementListProgressState) {
	if !s.requirementListProgressCurrent(p) || p.running || s.requirementsLoading || len(p.failures) == 0 {
		return
	}
	// Catalogue application keeps its existing selection policy. Only an explicit
	// retry of this still-current session restores its original request target.
	// It must never fall back to another Sprint from a partial catalogue.
	if p.sprintKey != s.requirementsSelectedSprintKey {
		s.activateRequirementSprint(p.sprintKey)
	}
	s.refreshRequirementListWithProgress(p.sprintKey)
}

func (s *app) bindRequirementListProgress(request *requirementListJob) {
	p := s.requirementListProgress
	if !s.requirementListProgressCurrent(p) || !p.running || request.action.Kind != "requirements.refresh" || p.sprintKey != request.sprintKey {
		return
	}
	p.request, p.identity = request, request.identity
	if identity := s.requirementIdentityJobs; identity != nil && identity.confirmed {
		p.account = identity.identity
	}
	request.progress = p
	win := s.win
	request.onProgress = func(event application.RequirementListProgress) {
		if win == nil || win.IsDestroyed() || request.ctx.Err() != nil {
			return
		}
		win.Update(func() { s.applyRequirementListProgress(request, event) })
	}
}

func (s *app) applyRequirementListProgress(request *requirementListJob, event application.RequirementListProgress) {
	p := request.progress
	if !s.requirementListJobRelevant(request) || !s.requirementListProgressCurrent(p) ||
		p.request != request || !p.running || event.Sequence <= p.lastSequence {
		return
	}
	if s.currentRequirementIdentityContext() != p.profile {
		s.requirementListProgress = nil
		return
	}
	p.lastSequence = event.Sequence
	key := event.Phase
	if key != "account" && key != "sprints" && key != "items" && key != "estimates" {
		return
	}
	if event.Phase == "items" && event.WorkItemType != "" {
		p.steps["items:"+event.WorkItemType] = event
		parent := event
		parent.Status = "running"
		parent.WorkItemType = ""
		p.steps[key] = parent
		return
	}
	p.steps[key] = event
}

func (s *app) failRequirementListProgress(p *requirementListProgressState, failures []string) {
	if !s.requirementListProgressCurrent(p) || !p.running {
		return
	}
	p.running, p.failures = false, append([]string(nil), failures...)
	p.notificationError = "需求刷新未完成，请查看失败说明。"
	for key, event := range p.steps {
		if event.Status == "running" {
			event.Status = "failed"
			p.steps[key] = event
		}
	}
}

func (s *app) finishRequirementListProgress(request *requirementListJob) {
	p := request.progress
	if !s.requirementListProgressCurrent(p) || p.request != request || !p.running {
		return
	}
	failures := append([]string(nil), s.requirementsFailures...)
	if s.requirementsLoadError != "" {
		failures = []string{s.requirementsLoadError}
	} else {
		p.steps["apply"] = application.RequirementListProgress{Phase: "apply", Status: "done"}
	}
	if estimates := p.steps["estimates"]; estimates.Status == "failed" {
		failures = append(failures, firstNonEmpty(estimates.Message, "个人估分读取未完成，请重试。"))
	}
	if len(failures) > 0 {
		s.failRequirementListProgress(p, failures)
		return
	}
	p.running, p.open = false, false
	p.message = fmt.Sprintf("需求刷新完成，共 %d 条工作项", len(s.requirements))
	if len(s.requirements) == 0 {
		p.message = "刷新完成，暂无工作项"
	}
}

func (s *app) renderRequirementListProgressNotification(c *ui.Context) {
	message, failure := "", ""
	if p := s.requirementListProgress; s.requirementListProgressCurrent(p) {
		s.operationNotifications.present(c, "requirement-list-result", &p.message, &p.notificationError)
		return
	}
	s.operationNotifications.present(c, "requirement-list-result", &message, &failure)
}
