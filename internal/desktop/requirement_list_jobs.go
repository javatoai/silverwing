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
	if s.requirementUnit.state.requirementListJobs.running == nil {
		s.requirementUnit.state.requirementListJobs.running = make(map[*requirementListJob]struct{})
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
	stamp := int64(0)
	if s.hasRequirementSprintCatalog() {
		stamp = state.catalogFetchedAt
	}
	state.snapshots[key] = requirementListViewSnapshot{
		fetchedAt:        s.requirementUnit.state.requirementsFetchedAt,
		items:            append([]application.Requirement(nil), s.requirementUnit.state.requirements...),
		selected:         s.requirementUnit.state.selectedRequirementKey,
		sprints:          append([]requirementSprint(nil), s.requirementSprintCatalog()...),
		sprintsFetchedAt: stamp,
		failures:         append([]string(nil), s.requirementUnit.state.requirementsFailures...),
		completed:        s.requirementUnit.state.requirementsCompletedQueries,
		loadError:        s.requirementUnit.state.requirementsLoadError,
		loaded:           s.requirementUnit.state.requirementsLoaded,
		estimate:         estimate,
	}
}

func (s *app) activateRequirementSprint(sprintKey string) {
	if sprintKey == "" {
		sprintKey = s.requirementUnit.state.requirementsSelectedSprintKey
	}
	if s.hasRequirementSprintCatalog() {
		s.requirementUnit.state.requirementsSprints = append([]requirementSprint(nil), s.requirementSprintCatalog()...)
		sprintKey = s.resolveRequirementSprintCatalogSelection(s.requirementUnit.state.requirementsSprints, sprintKey)
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
	if cached, ok := state.snapshots[requirementListSnapshotKey{identity: identity, sprint: sprintKey}]; ok && sprintKey != "" {
		s.requirementUnit.state.requirementsFetchedAt = cached.fetchedAt
		s.requirementUnit.state.requirements = append([]application.Requirement(nil), cached.items...)
		s.acceptRequirementSprintCatalog(cached.sprints, cached.sprintsFetchedAt, false)
		if s.hasRequirementSprintCatalog() {
			s.requirementUnit.state.requirementsSprints = append([]requirementSprint(nil), s.requirementSprintCatalog()...)
		} else {
			s.requirementUnit.state.requirementsSprints = append([]requirementSprint(nil), cached.sprints...)
		}
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
	state := s.ensureRequirementListJobs()
	win := s.requirementListWindow(state)
	if s.backend == nil || win == nil || win.IsDestroyed() {
		s.requirementUnit.state.requirementsLoading = false
		s.requirementUnit.state.requirementsLoadError = "需求列表后端或窗口不可用，请重试。"
		s.failRequirementListProgress(s.requirementUnit.state.requirementListProgress, []string{s.requirementUnit.state.requirementsLoadError})
		return
	}
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
	if refresh {
		s.retireRequirementSprintCatalogRequest(state)
		state.catalogAttempted = true
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
	if state.closed {
		state.mu.Unlock()
		request.cancel()
		state.current = nil
		s.requirementUnit.state.requirementsLoading = false
		return
	}
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
	backend := s.backend
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
	sprints := append([]requirementSprint(nil), s.requirementSprintCatalog()...)
	previousSprints := sprints
	var sprintsFetchedAt int64
	if data.SprintsFetchedAt != nil {
		sprintsFetchedAt = *data.SprintsFetchedAt
	}
	fromCache := data.FromCache != nil && *data.FromCache
	completeCatalog := sprintsFetchedAt > 0
	hasSprintCatalog := data.Sprints != nil || completeCatalog
	selectedSprintKey := ""
	if data.SelectedSprintKey != nil {
		selectedSprintKey = *data.SelectedSprintKey
	}
	if hasSprintCatalog {
		sprints = make([]requirementSprint, 0, len(data.Sprints))
		for _, sprint := range data.Sprints {
			sprints = append(sprints, requirementSprint(sprint))
		}
	}
	if completeCatalog {
		s.acceptRequirementSprintCatalog(sprints, sprintsFetchedAt, !fromCache)
	}
	if s.hasRequirementSprintCatalog() {
		sprints = append([]requirementSprint(nil), s.requirementSprintCatalog()...)
	}
	if fromCache {
		defer s.startRequirementSprintCatalogRefresh()
	}
	hasCatalogTimestamp := data.SprintsFetchedAt != nil
	hasCacheProvenance := data.FromCache != nil
	state := s.requirementUnit.state.requirementListJobs
	retainAvailableCatalog := !completeCatalog && len(failures) > 0 && len(previousSprints) > 0 && state != nil && state.hasIdentity &&
		state.identity == request.identity && s.currentRequirementListIdentity() == request.identity &&
		(s.hasRequirementSprintCatalog() || (hasCatalogTimestamp && hasCacheProvenance))
	if retainAvailableCatalog {
		// New DTOs explicitly mark incomplete directory reads. Before the first
		// complete read, retain usable same-scope cached choices without assigning
		// them an authoritative timestamp. Markerless legacy payloads keep their
		// existing selection/failure policy.
		sprints = previousSprints
	}
	resolvedSprintKey := request.sprintKey
	if hasSprintCatalog {
		resolvedSprintKey = selectedSprintKey
	}
	if completeCatalog {
		preferred := request.sprintKey
		if preferred == "" {
			preferred = resolvedSprintKey
		}
		next := s.resolveRequirementSprintCatalogSelection(sprints, preferred)
		if !requirementSprintCatalogContains(sprints, resolvedSprintKey) || next != resolvedSprintKey {
			// These items belong to the old/invalid selection. The target Sprint
			// must acquire its own items through the normal list path.
			s.requirementUnit.state.requirementsSprints = sprints
			s.requirementUnit.state.requirements, s.requirementUnit.state.requirementsFetchedAt = nil, 0
			s.clearRequirementListSelection()
			s.requirementUnit.state.requirementsSelectedSprintKey = ""
			s.requirementUnit.state.requirementsLoading, s.requirementUnit.state.requirementsLoaded = false, true
			s.requirementUnit.state.requirementsFailures, s.requirementUnit.state.requirementsCompletedQueries, s.requirementUnit.state.requirementsLoadError = nil, 0, ""
			s.requirementUnit.state.requirementsFocusedDetail = false
			if p := request.progress; p != nil && s.requirementUnit.state.requirementListProgress == p {
				p.viewSprintKey = ""
				s.finishRequirementListProgress(request)
			}
			if next != "" {
				s.requestRequirements(next, false)
			}
			return
		}
	} else if retainAvailableCatalog && request.sameSprint && request.sprintKey == s.requirementUnit.state.requirementsSelectedSprintKey &&
		requirementSprintCatalogContains(sprints, request.sprintKey) && selectedSprintKey != request.sprintKey {
		// An incomplete directory read cannot unbind the usable current Sprint,
		// including a cached view shown before the first complete directory read.
		// Foreign/unbound items cannot be relabelled as belonging to this Sprint.
		resolvedSprintKey, items, completed = request.sprintKey, nil, 0
	} else if s.hasRequirementSprintCatalog() && resolvedSprintKey != "" && !requirementSprintCatalogContains(sprints, resolvedSprintKey) {
		// A legacy/partial item cache cannot revive an expired Sprint, but its
		// existing failure/retry policy remains responsible for incomplete reads.
		resolvedSprintKey, items, completed = "", nil, 0
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
