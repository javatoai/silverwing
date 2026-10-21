package desktop

import (
	"slices"

	"silverwing-go/internal/domain"
)

const workspaceGitDisplayCacheLimit = 32

// Presentation only: never restored into workspaceGitState.health or persisted.
// Requests, snapshot reconciliation and their completions own these mutations.
type workspaceGitDisplayCache struct {
	scope   workspaceGitStatusScope
	entries map[string]*workspaceGitObservation
	recent  []string // oldest to most recently visited
}

func (cache *workspaceGitDisplayCache) prepare(scope workspaceGitStatusScope) {
	if cache.entries == nil || cache.scope != scope {
		*cache = workspaceGitDisplayCache{scope: scope, entries: make(map[string]*workspaceGitObservation)}
	}
}

func (cache *workspaceGitDisplayCache) forget(key string) {
	delete(cache.entries, key)
	if index := slices.Index(cache.recent, key); index >= 0 {
		cache.recent = slices.Delete(cache.recent, index, index+1)
	}
}

func (cache *workspaceGitDisplayCache) bind(t task, key string, scope workspaceGitStatusScope) *workspaceGitObservation {
	cache.prepare(scope)
	observation := cache.entries[key]
	if observation == nil {
		observation = &workspaceGitObservation{key: key, id: t.id, path: t.path, created: t.createdAt, group: t.groupID, scope: scope,
			identities: make(map[string]workspaceGitDisplayIdentity, len(t.workspaces)), display: make(map[string]domain.WorkspaceGitHealth)}
		for _, w := range t.workspaces {
			observation.identities[workspaceHealthPathKey(w.Path)] = workspaceGitDisplayKey(w)
		}
		cache.entries[key] = observation
	}
	if index := slices.Index(cache.recent, key); index >= 0 {
		cache.recent = slices.Delete(cache.recent, index, index+1)
	}
	cache.recent = append(cache.recent, key)
	for len(cache.recent) > workspaceGitDisplayCacheLimit {
		cache.forget(cache.recent[0])
	}
	return observation
}

func (cache *workspaceGitDisplayCache) reconcile(tasks []task, scope workspaceGitStatusScope) {
	cache.prepare(scope)
	valid := make(map[string]struct{}, len(tasks))
	for _, t := range tasks {
		valid[workspaceHealthTaskKey(t)] = struct{}{}
	}
	for key := range cache.entries {
		if _, exists := valid[key]; !exists {
			cache.forget(key)
		}
	}
}

func (s *app) reconcileWorkspaceGitDisplayCache() bool {
	state := s.workspaceGit
	if state == nil {
		return false
	}
	scope := s.workspaceGitStatusScope()
	state.displayCache.reconcile(s.tasks, scope)
	observation := state.observation
	if observation == nil || observation.scope == scope && state.displayCache.entries[workspaceHealthTaskKey(s.currentTask())] == observation {
		return false
	}
	s.cancelWorkspaceGitStatus()
	state.observation, state.failure = nil, ""
	state.health = make(map[string]domain.WorkspaceGitHealth)
	return true
}

func (s *app) invalidateWorkspaceGitDisplayCache() {
	if state := s.workspaceGit; state != nil {
		s.cancelWorkspaceGitStatus()
		state.displayCache = workspaceGitDisplayCache{}
		state.observation, state.failure = nil, ""
		state.health = make(map[string]domain.WorkspaceGitHealth)
	}
}

func (s *app) refreshWorkspaceGitDisplayContext() {
	if s.reconcileWorkspaceGitDisplayCache() {
		s.requestWorkspaceGitStatus(true)
	}
}
