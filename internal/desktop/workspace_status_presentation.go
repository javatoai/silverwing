package desktop

import (
	"fmt"
	"maps"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

type workspaceGitScopeCache struct {
	valid                 bool
	config, home, account string
	value                 workspaceGitStatusScope
}

type workspaceGitDisplayIdentity struct {
	workspace, service, module, branch, strategy string
	repositoryID, repository, origin, pushRemote string
}

func workspaceGitDisplayKey(w application.Workspace) workspaceGitDisplayIdentity {
	identity := workspaceGitDisplayIdentity{workspace: w.ID, service: w.ServiceID, module: w.ModuleID, branch: w.Branch,
		strategy: w.Strategy, repositoryID: w.Record.RepositoryID, repository: w.Record.RepositoryPath,
		pushRemote: firstNonEmpty(w.PushRemote, w.Record.PushRemote)}
	if w.Record.OriginURL != nil {
		identity.origin = *w.Record.OriginURL
	}
	return identity
}

// Only presentation retains a previous successful read on inspection failure.
// The authoritative health map still receives the failure for operation guards.
type workspaceGitObservation struct {
	key, id, path, created, group string
	scope                         workspaceGitStatusScope
	identities                    map[string]workspaceGitDisplayIdentity
	display                       map[string]domain.WorkspaceGitHealth
}

func (s *app) bindWorkspaceGitObservation(t task, scope workspaceGitStatusScope) *workspaceGitObservation {
	state := s.ensureWorkspaceGitState()
	key := workspaceHealthTaskKey(t)
	if old := state.observation; old != nil && old.key == key && old.scope == scope {
		return old
	}
	state.health = make(map[string]domain.WorkspaceGitHealth)
	state.failure = ""
	state.displayCache.reconcile(s.taskUnit.state.tasks, scope)
	observation := state.displayCache.bind(t, key, scope)
	state.observation = observation
	return observation
}

func (s *app) workspaceGitObservationCurrent(observation *workspaceGitObservation) bool {
	t := s.taskUnit.currentTask()
	return observation != nil && observation.id == t.id && observation.path == t.path && observation.created == t.createdAt &&
		observation.group == t.groupID && observation.scope == s.workspaceGitStatusScope()
}

func (s *app) workspaceDisplayHealth(w application.Workspace) (domain.WorkspaceGitHealth, bool) {
	state := s.workspaceUnit.state.workspaceGit
	if state == nil {
		return domain.WorkspaceGitHealth{}, false
	}
	if observation := state.observation; observation != nil {
		path := workspaceHealthPathKey(w.Path)
		identity, exists := observation.identities[path]
		if !s.workspaceGitObservationCurrent(observation) || !exists || identity != workspaceGitDisplayKey(w) {
			return domain.WorkspaceGitHealth{}, false
		}
		health, ok := observation.display[path]
		return health, ok
	}
	// Unbound, in-memory observations can be supplied before a request starts.
	// Every backend read binds its identity before it can publish a result.
	health, ok := s.workspaceHealth(w.Path)
	return health, ok && health.State != domain.WorkspaceGitChecking && health.Issue != domain.WorkspaceGitIssueInspectionFailed
}

func (s *app) commitWorkspaceGitStatuses(t task, statuses map[string]domain.WorkspaceGitHealth) {
	if workspaceHealthTaskKey(t) != workspaceHealthTaskKey(s.taskUnit.currentTask()) {
		return
	}
	state := s.ensureWorkspaceGitState()
	observation := s.bindWorkspaceGitObservation(t, s.workspaceGitStatusScope())
	normalized := make(map[string]domain.WorkspaceGitHealth, len(statuses))
	for path, health := range statuses {
		normalized[workspaceHealthPathKey(path)] = health
	}
	current := make(map[string]domain.WorkspaceGitHealth, len(t.workspaces))
	display := maps.Clone(observation.display)
	failed, reason := 0, ""
	for _, w := range t.workspaces {
		path := workspaceHealthPathKey(w.Path)
		health, exists := normalized[path]
		if !exists || health.State == "" || health.State == domain.WorkspaceGitChecking {
			message, expected := "未返回有效的工作区 Git 状态", w.Branch
			health = domain.WorkspaceGitHealth{State: domain.WorkspaceGitFailed, Issue: domain.WorkspaceGitIssueInspectionFailed,
				PushState: domain.LocalPushFailed, Message: &message, ExpectedBranch: &expected}
		}
		current[path] = health
		if health.Issue == domain.WorkspaceGitIssueInspectionFailed {
			failed++
			if reason == "" && health.Message != nil {
				reason = strings.TrimSpace(*health.Message)
			}
			continue
		}
		display[path] = workspaceGitDisplaySummary(health)
	}
	// One window-thread publication updates all cards together.
	state.health, observation.display, state.failure = current, display, ""
	if failed > 0 {
		state.failure = fmt.Sprintf("刷新失败，%d 个工作区未更新；当前显示上次结果。", failed)
		if len(display) == 0 {
			state.failure = "刷新失败，Git 状态暂不可用。"
		}
		if reason != "" {
			state.failure += "\n" + reason
		}
	}
}

func workspaceGitDisplaySummary(health domain.WorkspaceGitHealth) domain.WorkspaceGitHealth {
	health.DirtyFiles = nil
	for _, pointer := range []**string{&health.Message, &health.ActualBranch, &health.ExpectedBranch} {
		if *pointer != nil {
			value := **pointer
			*pointer = &value
		}
	}
	return health
}
