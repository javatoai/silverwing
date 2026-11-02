package desktop

import (
	"context"
	"errors"
	"strings"

	"silverwing-go/internal/application"
)

func (s *app) requirementListWindow(state *requirementListJobState) requirementListWindow {
	if s.win != nil {
		return s.win
	}
	return state.window
}

func (s *app) hasRequirementSprintCatalog() bool {
	state := s.requirementUnit.state.requirementListJobs
	return state != nil && state.hasIdentity && state.identity == s.currentRequirementListIdentity() && state.catalogFetchedAt > 0
}

func (s *app) requirementSprintCatalog() []requirementSprint {
	if s.hasRequirementSprintCatalog() {
		return s.requirementUnit.state.requirementListJobs.catalog
	}
	return s.requirementUnit.state.requirementsSprints
}

func requirementSprintCatalogContains(sprints []requirementSprint, key string) bool {
	if key == "" {
		return false
	}
	for _, sprint := range sprints {
		if sprint.key() == key {
			return true
		}
	}
	return false
}

// An expired choice may move only to an unambiguous active Sprint. Upcoming
// Sprints and multiple active Sprints remain an explicit user choice.
func (s *app) resolveRequirementSprintCatalogSelection(sprints []requirementSprint, previous string) string {
	if requirementSprintCatalogContains(sprints, previous) {
		return previous
	}
	active, preferred := "", ""
	activeCount, preferredCount := 0, 0
	defaultProject := strings.TrimSpace(s.configString("meegleDefaultSprintProjectKey"))
	for _, sprint := range sprints {
		if strings.TrimSpace(sprint.Status) != "进行中" {
			continue
		}
		active, activeCount = sprint.key(), activeCount+1
		if defaultProject != "" && sprint.ProjectKey == defaultProject {
			preferred, preferredCount = sprint.key(), preferredCount+1
		}
	}
	if activeCount == 1 {
		return active
	}
	if preferredCount == 1 {
		return preferred
	}
	return ""
}

func (s *app) retireRequirementSprintCatalogRequest(state *requirementListJobState) {
	state.catalogGeneration++
	if state.catalogCurrent != nil {
		state.catalogCurrent.cancel()
		state.catalogCurrent = nil
	}
}

func (s *app) resetRequirementSprintCatalog(state *requirementListJobState) {
	s.retireRequirementSprintCatalogRequest(state)
	state.catalog, state.catalogFetchedAt, state.catalogAttempted = nil, 0, false
}

// Cached item lists can carry a newer persisted catalogue, but cannot overwrite
// an equally dated live one. Live reads also retire a pending startup worker,
// even when the following work-item queries failed.
func (s *app) acceptRequirementSprintCatalog(sprints []requirementSprint, fetchedAt int64, live bool) {
	if fetchedAt <= 0 {
		return
	}
	state := s.ensureRequirementListJobs()
	if live {
		s.retireRequirementSprintCatalogRequest(state)
		state.catalogAttempted = true
	}
	if fetchedAt < state.catalogFetchedAt || (!live && fetchedAt == state.catalogFetchedAt) {
		return
	}
	state.catalog, state.catalogFetchedAt = append([]requirementSprint(nil), sprints...), fetchedAt
}

// The first cache hit in a confirmed account session gets one quiet directory
// refresh. The worker shares the list lifecycle so identity reset and shutdown
// cancel/join it alongside item queries.
func (s *app) startRequirementSprintCatalogRefresh() {
	identityState := s.requirementUnit.state.requirementIdentityJobs
	if identityState == nil || !identityState.confirmed || identityState.state != application.RequirementIdentityAuthenticated || identityState.identity == "" || s.backend == nil {
		return
	}
	state := s.ensureRequirementListJobs()
	if !state.hasIdentity || state.identity != s.currentRequirementListIdentity() || state.catalogAttempted {
		return
	}
	window := s.requirementListWindow(state)
	if window == nil || window.IsDestroyed() {
		return
	}
	state.mu.Lock()
	closed := state.closed
	state.mu.Unlock()
	if closed {
		return
	}
	state.catalogAttempted = true
	state.catalogGeneration++
	ctx, cancel := context.WithCancel(context.Background())
	request := &requirementListJob{
		generation: state.catalogGeneration, identity: state.identity,
		action: application.Action{Kind: "requirements.sprints-refresh", Payload: map[string]any{"expectedIdentity": identityState.identity}},
		ctx:    ctx, cancel: cancel, done: make(chan struct{}),
		identityContext: identityState.context, hasIdentityContext: identityState.hasContext,
	}
	state.catalogCurrent = request
	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		cancel()
		state.catalogCurrent = nil
		return
	}
	state.running[request] = struct{}{}
	state.mu.Unlock()
	backend := s.backend
	go func() {
		result, err := executeRequirementListJob(backend, request)
		if !window.IsDestroyed() {
			updated := make(chan struct{})
			window.Update(func() {
				defer close(updated)
				s.finishRequirementSprintCatalogJob(request, result, err)
			})
			select {
			case <-updated:
			case <-ctx.Done():
			}
		}
		cancel()
		s.completeRequirementListWorker(state, request)
	}()
}

func (s *app) requirementSprintCatalogJobRelevant(request *requirementListJob) bool {
	state := s.requirementUnit.state.requirementListJobs
	if state == nil || request == nil {
		return false
	}
	state.mu.Lock()
	closed := state.closed
	state.mu.Unlock()
	return !closed && state.catalogCurrent == request && state.catalogGeneration == request.generation && state.hasIdentity &&
		state.identity == request.identity && s.currentRequirementListIdentity() == request.identity && request.ctx.Err() == nil
}

func (s *app) finishRequirementSprintCatalogJob(request *requirementListJob, result application.ActionResult, err error) {
	if !s.requirementSprintCatalogJobRelevant(request) {
		return
	}
	if request.hasIdentityContext {
		next := s.currentRequirementIdentityContext()
		if next != request.identityContext {
			s.resetRequirementIdentityContext(s.ensureRequirementIdentityJobs(), next)
			return
		}
	}
	state := s.requirementUnit.state.requirementListJobs
	state.catalogCurrent = nil
	if err != nil {
		var changed *application.RequirementIdentityChangedError
		if errors.As(err, &changed) {
			s.applyRequirementListIdentityFailure(changed.Snapshot, err)
		}
		return
	}
	data := mapData(result.Data)
	fetchedAt := dataInt64(data, "fetchedAt")
	payload, present := data["sprints"]
	if !present || fetchedAt <= 0 || len(mapsString(data["failures"])) > 0 || fetchedAt < state.catalogFetchedAt {
		return
	}
	sprints, decodeErr := decodeData[requirementSprint](payload)
	if decodeErr != nil {
		return
	}
	s.acceptRequirementSprintCatalog(sprints, fetchedAt, true)
	s.reconcileRequirementSprintCatalog()
}

// Applying a complete directory preserves valid manual choices and their exact
// items. If the choice expired, clear its items before requesting the next
// Sprint through the ordinary identity/cache-aware list path.
func (s *app) reconcileRequirementSprintCatalog() {
	if !s.hasRequirementSprintCatalog() {
		return
	}
	sprints := s.requirementSprintCatalog()
	s.requirementUnit.state.requirementsSprints = append([]requirementSprint(nil), sprints...)
	previous := s.requirementUnit.state.requirementsSelectedSprintKey
	if requirementSprintCatalogContains(sprints, previous) {
		return
	}
	next := s.resolveRequirementSprintCatalogSelection(sprints, previous)
	state := s.ensureRequirementListJobs()
	s.retireRequirementListRequest(state)
	state.generation++
	s.requirementUnit.cancelRequirementDocumentPreparation()
	s.requirementUnit.state.requirements, s.requirementUnit.state.requirementsFetchedAt = nil, 0
	s.clearRequirementListSelection()
	s.requirementUnit.state.requirementsSelectedSprintKey = ""
	s.requirementUnit.state.requirementsLoading, s.requirementUnit.state.requirementsLoaded = false, true
	s.requirementUnit.state.requirementsLoadError, s.requirementUnit.state.requirementsFailures, s.requirementUnit.state.requirementsCompletedQueries = "", nil, 0
	s.requirementUnit.state.requirementsFocusedDetail = false
	if next != "" {
		s.requestRequirements(next, false)
	}
}
