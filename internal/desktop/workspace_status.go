package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

// Git health is a transient observation, never a task-manifest field. All
// state below is accessed on the window thread, including async completions.
type workspaceGitState struct {
	taskKey      string
	generation   uint64
	loading      bool
	cancel       context.CancelFunc
	current      *workspaceGitStatusRequest
	pending      bool
	afterLoad    bool
	queuedKey    string
	queuedScope  workspaceGitStatusScope
	scope        workspaceGitStatusScope
	health       map[string]domain.WorkspaceGitHealth
	observation  *workspaceGitObservation
	displayCache workspaceGitDisplayCache
	scopeCache   workspaceGitScopeCache
	failure      string
	flow         *workspaceGitFlowState
}

type workspaceGitStatusScope struct {
	home, taskRoot, executable, account string
}

type workspaceGitStatusRequest struct {
	generation uint64
	key        string
	scope      workspaceGitStatusScope
	task       task
	action     application.Action
}

func (s *app) workspaceGitStatusScope() workspaceGitStatusScope {
	state := s.ensureWorkspaceGitState()
	account := s.currentRequirementAccountIdentity()
	cache := &state.scopeCache
	if cache.valid && cache.config == s.configJSON && cache.home == s.snapshot.Home && cache.account == account {
		return cache.value
	}
	value := workspaceGitStatusScope{
		home: s.snapshot.Home, taskRoot: s.configString("taskRoot"), executable: s.configString("gitExecutablePath"),
		account: account,
	}
	*cache = workspaceGitScopeCache{valid: true, config: s.configJSON, home: s.snapshot.Home, account: account, value: value}
	return value
}

func workspaceHealthPathKey(path string) string {
	if absolute, err := filepath.Abs(path); err == nil {
		path = absolute
	}
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

func workspaceHealthTaskKey(task task) string {
	var key strings.Builder
	key.WriteString(task.id)
	key.WriteByte(0)
	key.WriteString(task.path)
	key.WriteByte(0)
	key.WriteString(task.createdAt)
	key.WriteByte(0)
	key.WriteString(task.groupID)
	for _, item := range task.workspaces {
		identity := workspaceGitDisplayKey(item)
		for _, value := range []string{workspaceHealthPathKey(item.Path), identity.workspace, identity.service, identity.module,
			identity.branch, identity.strategy, identity.repositoryID, identity.repository, identity.origin, identity.pushRemote} {
			key.WriteByte(0)
			key.WriteString(value)
		}
	}
	return key.String()
}

func (s *app) ensureWorkspaceGitState() *workspaceGitState {
	if s.workspaceGit == nil {
		s.workspaceGit = &workspaceGitState{health: make(map[string]domain.WorkspaceGitHealth)}
	}
	return s.workspaceGit
}

func (s *app) cancelWorkspaceGitStatus() {
	if state := s.workspaceGit; state != nil {
		if state.cancel != nil {
			state.cancel()
			state.cancel = nil
		}
		state.generation++
		state.loading = false
		state.taskKey = ""
		state.pending = false
		state.afterLoad = false
		state.queuedKey = ""
		state.queuedScope = workspaceGitStatusScope{}
	}
}

func (s *app) requestWorkspaceGitStatus(force bool) {
	if s.loading {
		if t := s.currentTask(); t.id != "" {
			s.bindWorkspaceGitObservation(t, s.workspaceGitStatusScope())
		}
		if force {
			s.queueWorkspaceGitStatusAfterLoad()
		}
		return
	}
	if s.backend == nil || s.win == nil {
		return
	}
	task := s.currentTask()
	if task.id == "" {
		s.cancelWorkspaceGitStatus()
		return
	}
	state := s.ensureWorkspaceGitState()
	scope := s.workspaceGitStatusScope()
	if state.afterLoad {
		queuedKey, queuedScope := state.queuedKey, state.queuedScope
		state.afterLoad = false
		state.queuedKey = ""
		state.queuedScope = workspaceGitStatusScope{}
		// A snapshot may have replaced or removed the row while the window was
		// loading. Only carry the user's forced refresh forward when the same
		// selected task and account/config scope are still current.
		if queuedKey == workspaceHealthTaskKey(task) && queuedScope == scope {
			force = true
		}
	}
	if state.scope != scope {
		s.cancelWorkspaceGitStatus()
		state.scope = scope
		state.health = make(map[string]domain.WorkspaceGitHealth)
	}
	key := workspaceHealthTaskKey(task)
	s.bindWorkspaceGitObservation(task, scope)
	if !force && state.taskKey == key {
		return
	}
	if state.current != nil {
		if state.current.key != key && state.cancel != nil {
			state.cancel()
		}
		// One active read plus one latest intent, even when the backend is slow
		// to acknowledge cancellation. Repeated row clicks never fan out reads.
		state.taskKey, state.pending = key, true
		state.loading = len(task.workspaces) > 0
		return
	}
	s.cancelWorkspaceGitStatus()
	state.taskKey = key
	if len(task.workspaces) == 0 {
		return
	}
	state.loading = true
	state.failure = ""
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	state.cancel = cancel
	action := application.Action{Kind: "workspace.status", TaskID: task.id}
	request := &workspaceGitStatusRequest{generation: state.generation, key: key, scope: scope, task: task, action: action}
	state.current = request
	win := s.win
	backend := s.backend
	go func() {
		defer cancel()
		err := s.acquireBackend(ctx)
		var result application.ActionResult
		if err == nil {
			if err = ctx.Err(); err == nil {
				result, err = backend.Dispatch(ctx, action)
			}
			s.releaseBackend()
		}
		if win.IsDestroyed() {
			return
		}
		win.Update(func() {
			s.finishWorkspaceGitStatus(state, request, result, err)
		})
	}()
}

func (s *app) finishWorkspaceGitStatus(state *workspaceGitState, request *workspaceGitStatusRequest, result application.ActionResult, err error) {
	if s.workspaceGit != state || state.current != request {
		return
	}
	state.current, state.cancel, state.loading = nil, nil, false
	if state.pending {
		state.pending = false
		s.requestWorkspaceGitStatus(true)
		return
	}
	if state.generation != request.generation || state.taskKey != request.key ||
		s.workspaceGitStatusScope() != request.scope || workspaceHealthTaskKey(s.currentTask()) != request.key {
		state.taskKey = ""
		return
	}
	if err != nil {
		s.failWorkspaceGitStatus(request.task, err)
		return
	}
	s.applyWorkspaceGitStatus(request.action, result)
}

func (s *app) applyWorkspaceGitStatus(action application.Action, result application.ActionResult) {
	if action.TaskID != s.currentTask().id {
		return
	}
	var statuses map[string]domain.WorkspaceGitHealth
	content, err := json.Marshal(result.Data)
	if err == nil {
		err = json.Unmarshal(content, &statuses)
	}
	if err != nil || statuses == nil {
		if err == nil {
			err = fmt.Errorf("返回数据为空")
		}
		s.failWorkspaceGitStatus(s.currentTask(), fmt.Errorf("读取 Git 状态格式失败：%w", err))
		return
	}
	s.commitWorkspaceGitStatuses(s.currentTask(), statuses)
}

func (s *app) queueWorkspaceGitStatusAfterLoad() {
	state := s.ensureWorkspaceGitState()
	task := s.currentTask()
	if task.id == "" {
		state.afterLoad = false
		state.queuedKey = ""
		state.queuedScope = workspaceGitStatusScope{}
		return
	}
	// Keep one latest intent. The request itself is built only after loading
	// settles, from the then-current selected task and scope.
	state.afterLoad = true
	state.queuedKey = workspaceHealthTaskKey(task)
	state.queuedScope = s.workspaceGitStatusScope()
}

func (s *app) workspaceGitStatusBusy() bool {
	state := s.workspaceGit
	return state != nil && (state.loading || state.pending || state.afterLoad)
}

func (s *app) failWorkspaceGitStatus(task task, err error) {
	message := err.Error()
	statuses := make(map[string]domain.WorkspaceGitHealth, len(task.workspaces))
	for _, item := range task.workspaces {
		expected := item.Branch
		statuses[item.Path] = domain.WorkspaceGitHealth{
			State: domain.WorkspaceGitFailed, Issue: domain.WorkspaceGitIssueInspectionFailed,
			PushState: domain.LocalPushFailed, Message: &message, ExpectedBranch: &expected,
			DirtyFiles: []domain.WorkspaceGitFileChange{},
		}
	}
	s.commitWorkspaceGitStatuses(task, statuses)
}

func (s *app) workspaceHealth(path string) (domain.WorkspaceGitHealth, bool) {
	state := s.workspaceGit
	if state == nil {
		return domain.WorkspaceGitHealth{}, false
	}
	if state.observation != nil && !s.workspaceGitObservationCurrent(state.observation) {
		return domain.WorkspaceGitHealth{}, false
	}
	if observation := state.observation; observation != nil {
		pathKey, matching := workspaceHealthPathKey(path), false
		for _, item := range s.currentTask().workspaces {
			if workspaceHealthPathKey(item.Path) == pathKey {
				identity, exists := observation.identities[pathKey]
				matching = exists && identity == workspaceGitDisplayKey(item)
				break
			}
		}
		if !matching {
			return domain.WorkspaceGitHealth{}, false
		}
	}
	health, ok := state.health[workspaceHealthPathKey(path)]
	if !ok && state.loading {
		return domain.WorkspaceGitHealth{State: domain.WorkspaceGitChecking}, true
	}
	return health, ok
}

func (s *app) workspaceGitCaptureReady() bool {
	s.requestWorkspaceGitStatus(false)
	return s.workspaceGit == nil || !s.workspaceGit.loading
}

func workspaceDedicatedResult(kind string) bool {
	switch kind {
	case "workspace.remove-preview", "workspace.remove", "workspace.history", "workspace.file-preview", "workspace.diff-files", "workspace.diff-preview",
		"workspace.commit", "workspace.commit-push", "workspace.push", "workspace.batch", "workspace.merge", "workspace.merge-batch", "workspace.command":
		return true
	}
	return false
}

func refreshesWorkspaceHealth(kind string) bool {
	switch kind {
	case "workspace.fetch", "workspace.commit", "workspace.commit-push", "workspace.push", "workspace.batch", "workspace.merge", "workspace.merge-batch", "workspace.repair", "workspace.remove", "workspace.add",
		"workspace.bootstrap-clear", "workspace.bootstrap-rerun", "task.create", "task.archive", "task.restore", "task.refresh":
		return true
	}
	return false
}
