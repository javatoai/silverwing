package desktop

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"silverwing-go/internal/application"
)

func (s *app) ensureRequirementListJobs() *requirementListJobState {
	if s.requirementListJobs == nil {
		s.requirementListJobs = &requirementListJobState{
			running:   make(map[*requirementListJob]struct{}),
			snapshots: make(map[requirementListSnapshotKey]requirementListViewSnapshot),
		}
	} else if s.requirementListJobs.snapshots == nil {
		s.requirementListJobs.snapshots = make(map[requirementListSnapshotKey]requirementListViewSnapshot)
	}
	return s.requirementListJobs
}

func (s *app) saveRequirementListSnapshot(identity requirementListIdentity, sprintKey string) {
	if !s.requirementsLoaded && len(s.requirements) == 0 {
		return
	}
	state := s.ensureRequirementListJobs()
	key := requirementListSnapshotKey{identity: identity, sprint: sprintKey}
	estimate := state.snapshots[key].estimate
	if s.hasCompleteRequirementsEstimate() {
		estimate = requirementsEstimateTotal(s.requirements)
	}
	state.snapshots[key] = requirementListViewSnapshot{
		fetchedAt: s.requirementsFetchedAt,
		items:     append([]application.Requirement(nil), s.requirements...),
		selected:  s.selectedRequirementKey,
		sprints:   append([]requirementSprint(nil), s.requirementsSprints...),
		failures:  append([]string(nil), s.requirementsFailures...),
		completed: s.requirementsCompletedQueries,
		loadError: s.requirementsLoadError,
		loaded:    s.requirementsLoaded,
		estimate:  estimate,
	}
}

func (s *app) activateRequirementSprint(sprintKey string) {
	if sprintKey == "" {
		sprintKey = s.requirementsSelectedSprintKey
	}
	if sprintKey == s.requirementsSelectedSprintKey {
		return
	}
	s.requirementListProgress = nil
	state := s.ensureRequirementListJobs()
	identity := s.currentRequirementListIdentity()
	if state.hasIdentity && state.identity == identity {
		s.saveRequirementListSnapshot(identity, s.requirementsSelectedSprintKey)
	}
	if cached, ok := state.snapshots[requirementListSnapshotKey{identity: identity, sprint: sprintKey}]; ok {
		s.requirementsFetchedAt = cached.fetchedAt
		s.requirements = append([]application.Requirement(nil), cached.items...)
		s.requirementsSprints = append([]requirementSprint(nil), cached.sprints...)
		s.requirementsFailures = append([]string(nil), cached.failures...)
		s.requirementsCompletedQueries = cached.completed
		s.requirementsLoadError = cached.loadError
		s.requirementsLoaded = cached.loaded
		s.restoreRequirementListSelection(cached.selected)
	} else {
		s.requirementsFetchedAt = 0
		s.requirements = nil
		s.clearRequirementListSelection()
		s.requirementsLoaded = false
		s.requirementsFailures = nil
		s.requirementsCompletedQueries = 0
		s.requirementsLoadError = ""
	}
	s.requirementsSelectedSprintKey = sprintKey
	s.requirementsFocusedDetail = false
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
		s.requirementsLoading = false
		s.requirementsLoadError = "需求列表后端或窗口不可用，请重试。"
		s.failRequirementListProgress(s.requirementListProgress, []string{s.requirementsLoadError})
		return
	}
	state := s.ensureRequirementListJobs()
	state.mu.Lock()
	closed := state.closed
	state.mu.Unlock()
	if closed {
		s.requirementsLoading = false
		s.requirementsLoadError = "需求列表读取已关闭。"
		s.failRequirementListProgress(s.requirementListProgress, []string{s.requirementsLoadError})
		return
	}
	identity := s.currentRequirementListIdentity()
	if !state.hasIdentity {
		state.identity, state.hasIdentity = identity, true
	} else if state.identity != identity {
		s.resetRequirementListForIdentity(state, identity)
	}

	previousSprint := s.requirementsSelectedSprintKey
	if sprintKey == "" {
		sprintKey = previousSprint
	}
	sameSprint := sprintKey == previousSprint
	if current := state.current; current != nil && current.identity == identity && current.sprintKey == sprintKey && !refresh {
		return
	}
	items := append([]application.Requirement(nil), s.requirements...)
	selectedKey := s.selectedRequirementKey
	s.retireRequirementListRequest(state)
	s.cancelRequirementDocumentPreparation()
	state.generation++
	ctx, cancel := context.WithCancel(context.Background())
	payload := map[string]any{}
	if state := s.requirementIdentityJobs; state != nil && state.confirmed && state.identity != "" {
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
	if identityState := s.requirementIdentityJobs; identityState != nil && identityState.hasContext {
		request.identityContext, request.hasIdentityContext = identityState.context, true
	}
	state.current = request
	s.bindRequirementListProgress(request)
	state.mu.Lock()
	state.running[request] = struct{}{}
	state.mu.Unlock()

	if !sameSprint {
		s.requirements = nil
		s.clearRequirementListSelection()
		s.lastResult = ""
		s.requirementsFocusedDetail = false
	}
	s.requirementsSelectedSprintKey = sprintKey
	s.requirementsRequested = true
	s.requirementsLoading = true
	s.requirementsLoadError = ""
	s.requirementsFailures = nil
	s.requirementsCompletedQueries = 0
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
	var result application.ActionResult
	var err error
	if progressBackend, ok := backend.(application.RequirementListProgressBackend); ok && request.onProgress != nil {
		result, err = progressBackend.DispatchRequirementList(request.ctx, request.action, request.onProgress)
	} else {
		result, err = backend.Dispatch(request.ctx, request.action)
	}
	if request.ctx.Err() != nil {
		err = errors.Join(request.ctx.Err(), err)
	}
	return result, err
}

func (s *app) requirementListJobRelevant(request *requirementListJob) bool {
	state := s.requirementListJobs
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
	state := s.requirementListJobs
	state.current = nil
	if err != nil {
		var changed *application.RequirementIdentityChangedError
		if errors.As(err, &changed) {
			s.applyRequirementListIdentityFailure(changed.Snapshot, err)
			return
		}
		if errors.Is(err, context.Canceled) {
			s.requirementsLoading = false
			s.requirementsLoaded = false
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
	if progressCurrent && s.requirementListProgress == p {
		p.viewSprintKey = s.requirementsSelectedSprintKey
	}
	s.finishRequirementListProgress(request)
}

func (s *app) applyRequirementListFailure(request *requirementListJob, err error) {
	if request.sameSprint {
		s.requirements = append([]application.Requirement(nil), request.items...)
		s.restoreRequirementListSelection(request.selectedKey)
	} else {
		s.requirements = nil
		s.clearRequirementListSelection()
	}
	s.requirementsLoading, s.requirementsLoaded = false, true
	s.requirementsLoadError = "读取需求列表失败"
	if len(s.requirements) > 0 {
		s.requirementsLoadError += "；已保留上次结果"
	}
	s.requirementsLoadError += "：" + strings.TrimSpace(err.Error())
	s.requirementsFailures = []string{strings.TrimSpace(err.Error())}
	s.requirementsCompletedQueries = 0
	s.requirementsSelectedSprintKey = request.sprintKey
	s.saveRequirementListSnapshot(request.identity, request.sprintKey)
}

func (s *app) applyRequirementListResult(request *requirementListJob, result application.ActionResult) {
	data := mapData(result.Data)
	items, err := decodeData[application.Requirement](data["items"])
	if err != nil {
		s.applyRequirementListFailure(request, fmt.Errorf("读取需求列表格式失败：%w", err))
		return
	}
	failures := mapsString(data["failures"])
	completed := int(dataInt64(data, "completedQueries"))

	sprints := append([]requirementSprint(nil), s.requirementsSprints...)
	sprintsPayload, hasSprintCatalog := data["sprints"]
	if hasSprintCatalog && sprintsPayload != nil {
		decoded, decodeErr := decodeData[requirementSprint](data["sprints"])
		if decodeErr != nil {
			s.applyRequirementListFailure(request, fmt.Errorf("读取 Sprint 列表格式失败：%w", decodeErr))
			return
		}
		sprints = decoded
	}
	resolvedSprintKey := request.sprintKey
	if hasSprintCatalog && sprintsPayload != nil {
		resolvedSprintKey = dataString(data, "selectedSprintKey")
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

	s.requirementsSprints = sprints
	s.requirementsSelectedSprintKey = resolvedSprintKey
	s.requirementsLoading, s.requirementsLoaded = false, true
	s.requirementsFailures = failures
	s.requirementsCompletedQueries = completed
	s.requirementsLoadError = ""
	if completed == 0 && len(failures) > 0 {
		s.requirements = retainedItems
		s.restoreRequirementListSelection(selectedKey)
		s.requirementsLoadError = "读取需求列表失败"
		if len(retainedItems) > 0 {
			s.requirementsLoadError += "；已保留上次结果"
		}
		s.requirementsLoadError += "。" + failures[0]
		s.saveRequirementListSnapshot(request.identity, resolvedSprintKey)
		return
	}
	if len(failures) > 0 && len(retainedItems) > 0 {
		s.requirements = retainedItems
	} else {
		s.requirements = items
		s.requirementsFetchedAt = dataInt64(data, "fetchedAt")
	}
	s.overlayRefreshedRequirements(s.requirements, resolvedSprintKey, s.requirementsFetchedAt)
	s.restoreRequirementListSelection(selectedKey)
	s.startRequirementNamingPreparation(items)
	// A list refresh should only fill missing document/image cache entries; the
	// explicit detail refresh remains responsible for forcing body/comments.
	s.startRequirementDocumentPreparation(items, false, s.selectedRequirementKey)
	s.saveRequirementListSnapshot(request.identity, resolvedSprintKey)
}

func (s *app) restoreRequirementListSelection(previousKey string) {
	previous := s.selectedRequirementKey
	if previousKey != "" {
		if selected, ok := s.selectedRequirementByKey(previousKey); ok {
			s.selectedRequirement, s.selectedRequirementKey = selected, previousKey
		} else if len(s.requirements) > 0 {
			s.selectedRequirement, s.selectedRequirementKey = 0, requirementKey(s.requirements[0])
		} else {
			s.selectedRequirement, s.selectedRequirementKey = 0, ""
		}
	} else if len(s.requirements) > 0 {
		s.selectedRequirement, s.selectedRequirementKey = 0, requirementKey(s.requirements[0])
	} else {
		s.selectedRequirement, s.selectedRequirementKey = 0, ""
	}
	if previous != s.selectedRequirementKey {
		if s.page == "requirements" {
			s.dismissMarkdownBitmapViewer()
		}
		s.lastResult = ""
	}
}

func (s *app) clearRequirementListSelection() {
	if s.page == "requirements" && s.selectedRequirementKey != "" {
		s.dismissMarkdownBitmapViewer()
	}
	s.selectedRequirement, s.selectedRequirementKey = 0, ""
}

func (s *app) completeRequirementListWorker(state *requirementListJobState, request *requirementListJob) {
	state.mu.Lock()
	delete(state.running, request)
	close(request.done)
	state.mu.Unlock()
}

func (s *app) closeRequirementListJobs(ctx context.Context) error {
	state := s.requirementListJobs
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
