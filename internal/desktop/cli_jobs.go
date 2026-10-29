package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/asyncjob"
	"silverwing-go/internal/process"
)

// CLI status and authentication polling have their own UI requests. A slow
// authorization process must not occupy the task/Git operation coordinator.
// The state map is owned by the UI thread; workers only hold immutable requests.
type cliJobState struct {
	next          uint64
	requests      map[string]*cliJobRequest
	lifecycle     asyncjob.Group
	authMutations []*cliJobRequest // UI-owned close-blocking business operations
}

type cliJobRequest struct {
	id                             uint64
	key, tool, path, home, session string
	page, section, config          string
	action                         application.Action
	owner                          *cliJobState
	lease                          *asyncjob.Lease
	ctx                            context.Context
	cancel                         context.CancelFunc
	// UI-owned intent, independent of the worker's deferred context cleanup.
	userCancelled bool
	done          <-chan struct{}
	retiring      *cliJobRequest
	// Written once by executeCLIJob before done closes; read after <-done.
	completionErr  error
	meegleContext  requirementIdentityContext
	meegleIdentity string
}

func independentCLIAction(kind string) bool {
	return kind == "cli.status" || kind == "cli.auth.start" || kind == "cli.auth.logout" || kind == "cli.auth.session" || kind == "cli.auth.poll" || kind == "cli.auth.cancel" || kind == "meegle.projects"
}

func (s *app) dispatchIndependentCLI(action application.Action) bool {
	if !independentCLIAction(action.Kind) {
		return false
	}
	if s.backend == nil || s.win == nil || s.win.IsDestroyed() {
		s.lastError = "CLI 状态后端或窗口不可用。"
		return true
	}
	request, err := s.beginCLIJob(action)
	if err != nil {
		s.lastError = err.Error()
		return true
	}
	if request == nil {
		return true
	}
	backend, win := s.backend, s.win
	win.Invalidate()
	go func() {
		result, snapshot, err := executeCLIJob(backend, request)
		request.complete(err)
		if win.IsDestroyed() {
			return
		}
		request.owner.lifecycle.Post(func(callback func()) bool {
			if win.IsDestroyed() {
				return false
			}
			win.Update(callback)
			return true
		}, func() { s.finishCLIJob(request, result, snapshot, err) })
	}()
	return true
}

func (s *app) cliJobTool(action application.Action) string {
	if action.Kind == "meegle.projects" {
		return "meegle"
	}
	tool := strings.ToLower(strings.TrimSpace(payloadString(action.Payload, "tool")))
	if tool != "" {
		if !desktopCLIJobSupported(tool) {
			return ""
		}
		return tool
	}
	if session := payloadString(action.Payload, "sessionId"); session != "" {
		for name, current := range s.settingsUnit.state.settingsCLISessions {
			if mapString(current, "sessionId") == session && desktopCLIJobSupported(name) {
				return name
			}
		}
	}
	return ""
}

func (s *app) cliConfiguredPath(tool string) string {
	key := map[string]string{"meegle": "meegleExecutablePath", "codex": "codexExecutablePath", "git": "gitExecutablePath", "genbu": "genbuExecutablePath"}[tool]
	if key == "" {
		return ""
	}
	return s.configString(key)
}

func (s *app) beginCLIJob(action application.Action) (*cliJobRequest, error) {
	tool := s.cliJobTool(action)
	if tool == "" {
		return nil, fmt.Errorf("CLI 请求缺少工具或有效登录会话")
	}
	if s.toolsUnit.state.cliJobs == nil {
		s.toolsUnit.state.cliJobs = &cliJobState{requests: make(map[string]*cliJobRequest)}
	}
	key := "auth:" + tool
	if action.Kind == "cli.status" {
		key = "status:" + tool
	} else if action.Kind == "cli.auth.session" {
		key = "session:" + tool
	} else if action.Kind == "meegle.projects" {
		key = action.Kind
	}
	state := s.toolsUnit.state.cliJobs
	if state.lifecycle.Closed() {
		return nil, errors.New("CLI 请求已进入退出清理阶段")
	}
	if previous := state.requests["auth:"+tool]; previous != nil && previous.path == s.cliConfiguredPath(tool) && previous.home == s.snapshot.Home && (previous.action.Kind == "cli.auth.start" || previous.action.Kind == "cli.auth.logout") {
		if previous.action.Kind == action.Kind {
			return nil, nil
		}
		if action.Kind != "cli.status" && action.Kind != "meegle.projects" {
			return nil, fmt.Errorf("%s 登录操作正在执行，请等待完成或取消后重试", tool)
		}
	}
	if previous := state.requests[key]; previous != nil && previous.path == s.cliConfiguredPath(tool) && previous.home == s.snapshot.Home && previous.action.Kind == action.Kind && previous.session == payloadString(action.Payload, "sessionId") && (action.Kind == "cli.auth.poll" || action.Kind == "cli.auth.cancel") {
		return nil, nil
	}
	if action.Payload != nil {
		payload := make(map[string]any, len(action.Payload))
		for key, value := range action.Payload {
			payload[key] = value
		}
		action.Payload = payload
	}
	lease, err := state.lifecycle.Begin(context.Background(), asyncjob.Options{CancelOnClose: true})
	if err != nil {
		return nil, fmt.Errorf("CLI 请求已进入退出清理阶段：%w", err)
	}
	state.next++
	request := &cliJobRequest{id: state.next, key: key, tool: tool, path: s.cliConfiguredPath(tool), home: s.snapshot.Home,
		session: payloadString(action.Payload, "sessionId"), page: s.page, section: s.settingsUnit.state.settingsPage, config: s.settingsUnit.state.configJSON,
		action: action, owner: state, lease: lease, ctx: lease.Context(), cancel: lease.Cancel, done: lease.Done()}
	if action.Kind == "meegle.projects" {
		request.meegleContext = s.currentRequirementIdentityContext()
		if identity := s.requirementUnit.state.requirementIdentityJobs; identity != nil && identity.confirmed && identity.context == request.meegleContext && identity.state == application.RequirementIdentityAuthenticated {
			request.meegleIdentity = identity.identity
		} else if catalog := s.settingsUnit.state.meegleProjectCatalog; catalog != nil && catalog.context == request.meegleContext {
			request.meegleIdentity = catalog.verifiedIdentity
		}
	}
	if previous := state.requests[key]; previous != nil {
		if action.Kind == "cli.auth.cancel" || action.Kind == "cli.auth.logout" {
			// Let the backend atomically mark CANCELLED before terminating the
			// running poll. Otherwise the poll could first record FAILED.
			request.retiring = previous
		} else {
			previous.cancel()
		}
	}
	if action.Kind == "cli.auth.start" || action.Kind == "cli.auth.logout" {
		s.settingsUnit.stopCLIAuthPoll(tool)
		for pendingKey, pending := range state.requests {
			if pending.tool == tool && pendingKey != key {
				pending.cancel()
				delete(state.requests, pendingKey)
			}
		}
	}
	if action.Kind == "cli.auth.cancel" {
		if reader := state.requests["session:"+tool]; reader != nil {
			reader.cancel()
			delete(state.requests, "session:"+tool)
		}
	}
	state.requests[key] = request
	if action.Kind == "cli.auth.start" || action.Kind == "cli.auth.logout" {
		state.authMutationRunning()
		state.authMutations = append(state.authMutations, request)
	}
	return request, nil
}

func executeCLIJob(backend application.Backend, request *cliJobRequest) (result application.ActionResult, snapshot application.Snapshot, err error) {
	defer request.cancel()
	defer func() { request.completionErr = err }()
	if err := request.ctx.Err(); err != nil {
		return application.ActionResult{}, application.Snapshot{}, err
	}
	action := request.action
	if action.Kind == "meegle.projects" {
		if request.meegleIdentity == "" {
			identityBackend, ok := backend.(application.RequirementIdentityBackend)
			if !ok {
				return result, snapshot, errors.New("项目目录缺少账号身份核验服务")
			}
			identity, identityErr := identityBackend.RequirementIdentity(request.ctx)
			if identityErr != nil {
				return result, snapshot, identityErr
			}
			if identity.State != application.RequirementIdentityAuthenticated || strings.TrimSpace(identity.Identity) == "" {
				return result, snapshot, errors.New("请确认 Meegle 当前账号已登录后重试")
			}
			request.meegleIdentity = identity.Identity
		}
		payload := make(map[string]any, len(action.Payload)+1)
		for key, value := range action.Payload {
			payload[key] = value
		}
		payload["expectedIdentity"] = request.meegleIdentity
		action.Payload = payload
	}
	result, err = backend.Dispatch(request.ctx, action)
	if request.ctx.Err() != nil {
		// CLI adapters can wrap command errors as display text. Preserve the
		// context identity, including a backend that returns success after cancel.
		err = errors.Join(request.ctx.Err(), err)
	}
	if request.retiring != nil {
		request.retiring.cancel()
		cleanup, stop := context.WithTimeout(request.ctx, 10*time.Second)
		select {
		case <-request.retiring.done:
			if process.IsCleanupFailure(request.retiring.completionErr) {
				err = errors.Join(err, request.retiring.completionErr)
			}
		case <-cleanup.Done():
			err = errors.Join(err, fmt.Errorf("已请求取消登录，但进程清理尚未完成：%w", cleanup.Err()))
		}
		stop()
	}
	if err == nil && request.action.Kind == "cli.status" {
		// Automatic executable discovery may persist a path. Only the config
		// projection is used below; this read never replaces task/Git state.
		snapshot, _ = backend.Snapshot(request.ctx)
	}
	return result, snapshot, err
}

func (s *app) cliJobRelevant(request *cliJobRequest) bool {
	if request == nil || s.toolsUnit.state.cliJobs != request.owner || request.owner.lifecycle.Closed() || request.owner.requests[request.key] != request {
		return false
	}
	if s.snapshot.Home != request.home || s.cliConfiguredPath(request.tool) != request.path {
		return false
	}
	if request.action.Kind == "meegle.projects" && request.meegleContext != s.currentRequirementIdentityContext() {
		return false
	}
	if request.session != "" && mapString(s.settingsUnit.state.settingsCLISessions[request.tool], "sessionId") != request.session {
		return false
	}
	return true
}

func (s *app) finishCLIJob(request *cliJobRequest, result application.ActionResult, snapshot application.Snapshot, err error) {
	if request == nil {
		return
	}
	relevant := s.cliJobRelevant(request)
	request.owner.authMutationRunning()
	current := request.owner.requests[request.key] == request
	if current {
		delete(request.owner.requests, request.key)
	}
	if !relevant {
		if current && s.toolsUnit.state.cliJobs == request.owner && !request.owner.lifecycle.Closed() && request.action.Kind == "meegle.projects" && s.meegleCatalogActionRelevant(request.action) {
			s.syncMeegleProjectCatalogContext(s.settingsUnit.state.meegleProjectCatalog)
			s.settingsUnit.state.meegleProjectCatalog.loading = false
			s.settingsUnit.state.meegleProjectCatalog.error = "账号配置已变化，请刷新项目目录。"
		}
		return
	}
	currentSection := s.page == request.page && s.settingsUnit.state.settingsPage == request.section
	if request.userCancelled && !process.IsCleanupFailure(err) {
		if currentSection {
			s.settingsUnit.state.settingsOutput = "登录操作已取消。"
		}
		return
	}
	if err != nil {
		cleanupFailure := process.IsCleanupFailure(err)
		if s.applyMeegleProjectCatalogFailure(request.action, err) {
			return
		}
		if currentSection && (cleanupFailure || !errors.Is(err, context.Canceled)) {
			operation := "CLI 状态读取"
			if request.action.Kind == "cli.auth.start" {
				operation = "生成授权链接"
			} else if request.action.Kind == "cli.auth.logout" {
				operation = "退出登录"
			}
			s.settingsUnit.state.settingsOutput = operation + "失败：" + err.Error()
			s.lastError = s.settingsUnit.state.settingsOutput
		}
		if request.action.Kind == "cli.auth.poll" {
			if cleanupFailure {
				s.settingsUnit.stopCLIAuthPoll(request.tool)
			} else if !errors.Is(err, context.Canceled) {
				s.scheduleCLIAuthPoll(request.tool, s.settingsUnit.state.settingsCLISessions[request.tool])
			}
		}
		return
	}
	previousOutput := s.settingsUnit.state.settingsOutput
	if request.action.Kind == "meegle.projects" {
		if s.meegleCatalogActionRelevant(request.action) {
			s.settingsUnit.state.meegleProjectCatalog.context = request.meegleContext
			s.settingsUnit.state.meegleProjectCatalog.verifiedIdentity = request.meegleIdentity
		}
		s.applyMeegleProjectCatalogResult(request.action, result)
	} else {
		s.applySettingsActionResult(request.action, result)
	}
	if !currentSection {
		s.settingsUnit.state.settingsOutput = previousOutput
	}
	if request.action.Kind == "cli.status" {
		s.applySettingsCLIDiscoverySnapshot(request.tool, request.config, snapshot)
	}
}

func (s *app) invalidateCLIJobs(tool string) {
	if s.toolsUnit.state.cliJobs == nil {
		return
	}
	for key, request := range s.toolsUnit.state.cliJobs.requests {
		if request.tool == tool {
			request.cancel()
			delete(s.toolsUnit.state.cliJobs.requests, key)
		}
	}
}

// A cached login or version belongs to the configured command and application
// home that produced it. Changing either also retires its polling timer.
func (s *app) invalidateCLIConfiguration(snapshot application.Snapshot) {
	var config map[string]any
	if json.Unmarshal([]byte(snapshot.ConfigJSON), &config) != nil {
		return
	}
	for tool, key := range map[string]string{"meegle": "meegleExecutablePath", "codex": "codexExecutablePath", "git": "gitExecutablePath", "genbu": "genbuExecutablePath"} {
		if s.snapshot.Home == snapshot.Home && s.cliConfiguredPath(tool) == strings.TrimSpace(mapString(config, key)) {
			continue
		}
		s.invalidateCLIJobs(tool)
		s.settingsUnit.stopCLIAuthPoll(tool)
		if tool == "meegle" {
			s.clearMeegleSessionViews()
		}
		delete(s.settingsUnit.state.settingsCLIStatus, tool)
		delete(s.settingsUnit.state.settingsCLISessions, tool)
	}
}

func (request *cliJobRequest) complete(err error) {
	request.lease.Complete(asyncJobCleanupError(err))
}

// Ordinary action failures are consumed by their feature UI. Process cleanup
// uncertainty must also survive a destroyed window and be returned by shutdown.
func asyncJobCleanupError(err error) error {
	if process.IsCleanupFailure(err) {
		return err
	}
	return nil
}

// Only login initialization and logout block document/window closing. This
// business boundary is distinct from the lifecycle group that drains all jobs.
// The UI owns this slice; workers publish completion through their lease only.
func (state *cliJobState) authMutationRunning() bool {
	pending := state.authMutations[:0]
	for _, request := range state.authMutations {
		select {
		case <-request.done:
		default:
			pending = append(pending, request)
		}
	}
	clear(state.authMutations[len(pending):])
	state.authMutations = pending
	return len(pending) > 0
}

func (s *app) closeCLIJobs(ctx context.Context) error {
	state := s.toolsUnit.state.cliJobs
	if state == nil {
		return nil
	}
	state.lifecycle.Close()
	state.requests = make(map[string]*cliJobRequest)
	err := state.lifecycle.Wait(ctx)
	state.authMutationRunning()
	return err
}

func desktopCLIJobSupported(tool string) bool {
	switch tool {
	case "meegle", "codex", "git", "genbu":
		return true
	default:
		return false
	}
}
