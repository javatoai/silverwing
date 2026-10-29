package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"silverwing-go/internal/application"
)

func (s *app) ensureRequirementListJobs() *requirementListJobState {
	if s.requirementUnit.state.requirementListJobs == nil {
		s.requirementUnit.state.requirementListJobs = &requirementListJobState{
			running:   make(map[*requirementListJob]struct{}),
			snapshots: make(map[requirementListSnapshotKey]requirementListViewSnapshot),
		}
	} else if s.requirementUnit.state.requirementListJobs.snapshots == nil {
		s.requirementUnit.state.requirementListJobs.snapshots = make(map[requirementListSnapshotKey]requirementListViewSnapshot)
	}
	return s.requirementUnit.state.requirementListJobs
}

func (s *app) saveRequirementListSnapshot(identity requirementListIdentity, sprintKey string) {
	if !s.requirementUnit.state.requirementsLoaded && len(s.requirementUnit.state.requirements) == 0 {
		return
	}
	state := s.ensureRequirementListJobs()
	key := requirementListSnapshotKey{identity: identity, sprint: sprintKey}
	estimate := state.snapshots[key].estimate
	if s.hasCompleteRequirementsEstimate() {
		estimate = requirementsEstimateTotal(s.requirementUnit.state.requirements)
	}
	state.snapshots[key] = requirementListViewSnapshot{
		fetchedAt: s.requirementUnit.state.requirementsFetchedAt,
		items:     append([]application.Requirement(nil), s.requirementUnit.state.requirements...),
		selected:  s.requirementUnit.state.selectedRequirementKey,
		sprints:   append([]requirementSprint(nil), s.requirementUnit.state.requirementsSprints...),
		failures:  append([]string(nil), s.requirementUnit.state.requirementsFailures...),
		completed: s.requirementUnit.state.requirementsCompletedQueries,
		loadError: s.requirementUnit.state.requirementsLoadError,
		loaded:    s.requirementUnit.state.requirementsLoaded,
		estimate:  estimate,
	}
}

func (s *app) activateRequirementSprint(sprintKey string) {
	if sprintKey == "" {
		sprintKey = s.requirementUnit.state.requirementsSelectedSprintKey
	}
	if sprintKey == s.requirementUnit.state.requirementsSelectedSprintKey {
		return
	}
	s.requirementUnit.state.requirementListProgress = nil
	state := s.ensureRequirementListJobs()
	identity := s.currentRequirementListIdentity()
	if state.hasIdentity && state.identity == identity {
		s.saveRequirementListSnapshot(identity, s.requirementUnit.state.requirementsSelectedSprintKey)
	}
	if cached, ok := state.snapshots[requirementListSnapshotKey{identity: identity, sprint: sprintKey}]; ok {
		s.requirementUnit.state.requirementsFetchedAt = cached.fetchedAt
		s.requirementUnit.state.requirements = append([]application.Requirement(nil), cached.items...)
		s.requirementUnit.state.requirementsSprints = append([]requirementSprint(nil), cached.sprints...)
		s.requirementUnit.state.requirementsFailures = append([]string(nil), cached.failures...)
		s.requirementUnit.state.requirementsCompletedQueries = cached.completed
		s.requirementUnit.state.requirementsLoadError = cached.loadError
		s.requirementUnit.state.requirementsLoaded = cached.loaded
		s.restoreRequirementListSelection(cached.selected)
	} else {
		s.requirementUnit.state.requirementsFetchedAt = 0
		s.requirementUnit.state.requirements = nil
		s.clearRequirementListSelection()
		s.requirementUnit.state.requirementsLoaded = false
		s.requirementUnit.state.requirementsFailures = nil
		s.requirementUnit.state.requirementsCompletedQueries = 0
		s.requirementUnit.state.requirementsLoadError = ""
	}
	s.requirementUnit.state.requirementsSelectedSprintKey = sprintKey
	s.requirementUnit.state.requirementsFocusedDetail = false
	s.lastResult = ""
}

func (s *app) retireRequirementListRequest(state *requirementListJobState) {
	if state == nil || state.current == nil {
		return
	}
	state.current.cancel()
	state.current = nil
}

func (s *app) startRequirementListJob(sprintKey string, refresh bool) {
	if s.backend == nil || s.win == nil || s.win.IsDestroyed() {
		s.requirementUnit.state.requirementsLoading = false
		s.requirementUnit.state.requirementsLoadError = "需求列表后端或窗口不可用，请重试。"
		s.failRequirementListProgress(s.requirementUnit.state.requirementListProgress, []string{s.requirementUnit.state.requirementsLoadError})
		return
	}
	state := s.ensureRequirementListJobs()
	state.mu.Lock()
	closed := state.closed
	state.mu.Unlock()
	if closed {
		s.requirementUnit.state.requirementsLoading = false
		s.requirementUnit.state.requirementsLoadError = "需求列表读取已关闭。"
		s.failRequirementListProgress(s.requirementUnit.state.requirementListProgress, []string{s.requirementUnit.state.requirementsLoadError})
		return
	}
	identity := s.currentRequirementListIdentity()
	if !state.hasIdentity {
		state.identity, state.hasIdentity = identity, true
	} else if state.identity != identity {
		s.resetRequirementListForIdentity(state, identity)
	}

	previousSprint := s.requirementUnit.state.requirementsSelectedSprintKey
	if sprintKey == "" {
		sprintKey = previousSprint
	}
	sameSprint := sprintKey == previousSprint
	if current := state.current; current != nil && current.identity == identity && current.sprintKey == sprintKey && !refresh {
		return
	}
	items := append([]application.Requirement(nil), s.requirementUnit.state.requirements...)
	selectedKey := s.requirementUnit.state.selectedRequirementKey
	s.retireRequirementListRequest(state)
	s.requirementUnit.cancelRequirementDocumentPreparation()
	state.generation++
	ctx, cancel := context.WithCancel(context.Background())
	payload := map[string]any{}
	if state := s.requirementUnit.state.requirementIdentityJobs; state != nil && state.confirmed && state.identity != "" {
		payload["expectedIdentity"] = state.identity
	}
	if sprintKey != "" {
		payload["sprintKey"] = sprintKey
	}
	kind := "requirements.list"
	if refresh {
		kind = "requirements.refresh"
	}
	request := &requirementListJob{
		generation: state.generation, identity: identity,
		action: application.Action{Kind: kind, Payload: payload},
		ctx:    ctx, cancel: cancel, done: make(chan struct{}),
		items: items, selectedKey: selectedKey, sprintKey: sprintKey, sameSprint: sameSprint,
	}
	if identityState := s.requirementUnit.state.requirementIdentityJobs; identityState != nil && identityState.hasContext {
		request.identityContext, request.hasIdentityContext = identityState.context, true
	}
	state.current = request
	s.bindRequirementListProgress(request)
	state.mu.Lock()
	state.running[request] = struct{}{}
	state.mu.Unlock()

	if !sameSprint {
		s.requirementUnit.state.requirements = nil
		s.clearRequirementListSelection()
		s.lastResult = ""
		s.requirementUnit.state.requirementsFocusedDetail = false
	}
	s.requirementUnit.state.requirementsSelectedSprintKey = sprintKey
	s.requirementUnit.state.requirementsRequested = true
	s.requirementUnit.state.requirementsLoading = true
	s.requirementUnit.state.requirementsLoadError = ""
	s.requirementUnit.state.requirementsFailures = nil
	s.requirementUnit.state.requirementsCompletedQueries = 0
	backend, win := s.backend, s.win
	win.Invalidate()
	go func() {
		result, err := executeRequirementListJob(backend, request)
		if win.IsDestroyed() {
			request.cancel()
			s.completeRequirementListWorker(state, request)
			return
		}
		updated := make(chan struct{})
		win.Update(func() {
			defer close(updated)
			s.finishRequirementListJob(request, result, err)
		})
		select {
		case <-updated:
		case <-request.ctx.Done():
		}
		request.cancel()
		s.completeRequirementListWorker(state, request)
	}()
}

func executeRequirementListJob(backend application.Backend, request *requirementListJob) (application.ActionResult, error) {
	if backend == nil || request == nil {
		return application.ActionResult{}, errors.New("需求列表读取缺少后端或请求。")
	}
	if err := request.ctx.Err(); err != nil {
		return application.ActionResult{}, err
	}
	result, err := executeRequirementOperation(request.ctx, backend, request.action, request.onProgress)
	if request.ctx.Err() != nil {
		err = errors.Join(request.ctx.Err(), err)
	}
	return result, err
}

func (s *app) requirementListJobRelevant(request *requirementListJob) bool {
	state := s.requirementUnit.state.requirementListJobs
	if state == nil || request == nil {
		return false
	}
	state.mu.Lock()
	closed := state.closed
	state.mu.Unlock()
	if closed || state.current != request || state.generation != request.generation ||
		state.identity != request.identity || s.currentRequirementListIdentity() != request.identity {
		return false
	}
	return request.ctx.Err() == nil
}

func (s *app) finishRequirementListJob(request *requirementListJob, result application.ActionResult, err error) {
	if !s.requirementListJobRelevant(request) {
		return
	}
	// Source verifies the profile metadata even on a cache hit. An external CLI
	// may change it while this worker runs without a focus event reaching us.
	if request.hasIdentityContext {
		next := s.currentRequirementIdentityContext()
		if next != request.identityContext {
			s.resetRequirementIdentityContext(s.ensureRequirementIdentityJobs(), next)
			return
		}
	}
	state := s.requirementUnit.state.requirementListJobs
	state.current = nil
	if err != nil {
		var changed *application.RequirementIdentityChangedError
		if errors.As(err, &changed) {
			s.applyRequirementListIdentityFailure(changed.Snapshot, err)
			return
		}
		if errors.Is(err, context.Canceled) {
			s.requirementUnit.state.requirementsLoading = false
			s.requirementUnit.state.requirementsLoaded = false
			return
		}
		s.applyRequirementListFailure(request, err)
		s.finishRequirementListProgress(request)
		return
	}
	p := request.progress
	progressCurrent := s.requirementListProgressCurrent(p) && p.request == request
	if progressCurrent {
		p.steps["apply"] = application.RequirementListProgress{Phase: "apply", Status: "running", Message: "正在更新列表…"}
	}
	s.applyRequirementListResult(request, result)
	if progressCurrent && s.requirementUnit.state.requirementListProgress == p {
		p.viewSprintKey = s.requirementUnit.state.requirementsSelectedSprintKey
	}
	s.finishRequirementListProgress(request)
}

func (s *app) applyRequirementListFailure(request *requirementListJob, err error) {
	if request.sameSprint {
		s.requirementUnit.state.requirements = append([]application.Requirement(nil), request.items...)
		s.restoreRequirementListSelection(request.selectedKey)
	} else {
		s.requirementUnit.state.requirements = nil
		s.clearRequirementListSelection()
	}
	s.requirementUnit.state.requirementsLoading, s.requirementUnit.state.requirementsLoaded = false, true
	s.requirementUnit.state.requirementsLoadError = "读取需求列表失败"
	if len(s.requirementUnit.state.requirements) > 0 {
		s.requirementUnit.state.requirementsLoadError += "；已保留上次结果"
	}
	s.requirementUnit.state.requirementsLoadError += "：" + strings.TrimSpace(err.Error())
	s.requirementUnit.state.requirementsFailures = []string{strings.TrimSpace(err.Error())}
	s.requirementUnit.state.requirementsCompletedQueries = 0
	s.requirementUnit.state.requirementsSelectedSprintKey = request.sprintKey
	s.saveRequirementListSnapshot(request.identity, request.sprintKey)
}

func (s *app) applyRequirementListResult(request *requirementListJob, result application.ActionResult) {
	data, err := application.ResultData[application.RequirementList](result)
	if err != nil {
		s.applyRequirementListFailure(request, fmt.Errorf("读取需求列表格式失败：%w", err))
		return
	}
	items, failures, completed := data.Items, data.Failures, data.CompletedQueries
	sprints := append([]requirementSprint(nil), s.requirementUnit.state.requirementsSprints...)
	hasSprintCatalog := data.Sprints != nil
	if hasSprintCatalog {
		sprints = make([]requirementSprint, 0, len(data.Sprints))
		for _, sprint := range data.Sprints {
			sprints = append(sprints, requirementSprint(sprint))
		}
	}
	resolvedSprintKey := request.sprintKey
	if hasSprintCatalog {
		resolvedSprintKey = ""
		if data.SelectedSprintKey != nil {
			resolvedSprintKey = *data.SelectedSprintKey
		}
	}
	retain := request.sameSprint && resolvedSprintKey == request.sprintKey
	retainedItems := []application.Requirement(nil)
	if retain {
		retainedItems = append(retainedItems, request.items...)
	}
	selectedKey := request.selectedKey
	if !retain {
		selectedKey = ""
	}

	s.requirementUnit.state.requirementsSprints = sprints
	s.requirementUnit.state.requirementsSelectedSprintKey = resolvedSprintKey
	s.requirementUnit.state.requirementsLoading, s.requirementUnit.state.requirementsLoaded = false, true
	s.requirementUnit.state.requirementsFailures = failures
	s.requirementUnit.state.requirementsCompletedQueries = completed
	s.requirementUnit.state.requirementsLoadError = ""
	if completed == 0 && len(failures) > 0 {
		s.requirementUnit.state.requirements = retainedItems
		s.restoreRequirementListSelection(selectedKey)
		s.requirementUnit.state.requirementsLoadError = "读取需求列表失败"
		if len(retainedItems) > 0 {
			s.requirementUnit.state.requirementsLoadError += "；已保留上次结果"
		}
		s.requirementUnit.state.requirementsLoadError += "。" + failures[0]
		s.saveRequirementListSnapshot(request.identity, resolvedSprintKey)
		return
	}
	if len(failures) > 0 && len(retainedItems) > 0 {
		s.requirementUnit.state.requirements = retainedItems
	} else {
		s.requirementUnit.state.requirements = items
		s.requirementUnit.state.requirementsFetchedAt = data.FetchedAt
	}
	s.overlayRefreshedRequirements(s.requirementUnit.state.requirements, resolvedSprintKey, s.requirementUnit.state.requirementsFetchedAt)
	s.restoreRequirementListSelection(selectedKey)
	s.startRequirementNamingPreparation(items)
	// A list refresh should only fill missing document/image cache entries; the
	// explicit detail refresh remains responsible for forcing body/comments.
	s.startRequirementDocumentPreparation(items, false, s.requirementUnit.state.selectedRequirementKey)
	s.saveRequirementListSnapshot(request.identity, resolvedSprintKey)
}

func (s *app) restoreRequirementListSelection(previousKey string) {
	previous := s.requirementUnit.state.selectedRequirementKey
	if previousKey != "" {
		if selected, ok := s.selectedRequirementByKey(previousKey); ok {
			s.requirementUnit.state.selectedRequirement, s.requirementUnit.state.selectedRequirementKey = selected, previousKey
		} else if len(s.requirementUnit.state.requirements) > 0 {
			s.requirementUnit.state.selectedRequirement, s.requirementUnit.state.selectedRequirementKey = 0, requirementKey(s.requirementUnit.state.requirements[0])
		} else {
			s.requirementUnit.state.selectedRequirement, s.requirementUnit.state.selectedRequirementKey = 0, ""
		}
	} else if len(s.requirementUnit.state.requirements) > 0 {
		s.requirementUnit.state.selectedRequirement, s.requirementUnit.state.selectedRequirementKey = 0, requirementKey(s.requirementUnit.state.requirements[0])
	} else {
		s.requirementUnit.state.selectedRequirement, s.requirementUnit.state.selectedRequirementKey = 0, ""
	}
	if previous != s.requirementUnit.state.selectedRequirementKey {
		if s.page == "requirements" {
			s.dismissMarkdownBitmapViewer()
		}
		s.lastResult = ""
	}
}

func (s *app) clearRequirementListSelection() {
	if s.page == "requirements" && s.requirementUnit.state.selectedRequirementKey != "" {
		s.dismissMarkdownBitmapViewer()
	}
	s.requirementUnit.state.selectedRequirement, s.requirementUnit.state.selectedRequirementKey = 0, ""
}

func (s *app) completeRequirementListWorker(state *requirementListJobState, request *requirementListJob) {
	state.mu.Lock()
	delete(state.running, request)
	close(request.done)
	state.mu.Unlock()
}

func (s *requirementController) closeRequirementListJobs(ctx context.Context) error {
	state := s.state.requirementListJobs
	if state == nil {
		return nil
	}
	state.mu.Lock()
	state.closed = true
	running := make([]*requirementListJob, 0, len(state.running))
	for request := range state.running {
		request.cancel()
		running = append(running, request)
	}
	state.mu.Unlock()
	for _, request := range running {
		select {
		case <-request.done:
		case <-ctx.Done():
			return fmt.Errorf("等待需求列表读取清理：%w", ctx.Err())
		}
	}
	return nil
}
