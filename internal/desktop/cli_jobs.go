package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/process"
)

// CLI status and authentication polling have their own UI requests. A slow
// authorization process must not occupy the task/Git operation coordinator.
// The state map is owned by the UI thread; workers only hold immutable requests.
type cliJobState struct {
	next     uint64
	requests map[string]*cliJobRequest
	running  map[*cliJobRequest]struct{}

	cleanupMu  sync.Mutex
	cleanupErr error
}

type cliJobRequest struct {
	id                             uint64
	key, tool, path, home, session string
	page, section, config          string
	action                         application.Action
	ctx                            context.Context
	cancel                         context.CancelFunc
	// UI-owned intent, independent of the worker's deferred context cleanup.
	userCancelled bool
	done          chan struct{}
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
	backend, win, state := s.backend, s.win, s.cliJobs
	win.Invalidate()
	go func() {
		result, snapshot, err := executeCLIJob(backend, request)
		state.recordCleanupFailure(err)
		close(request.done)
		if win.IsDestroyed() {
			return
		}
		win.Update(func() { s.finishCLIJob(request, result, snapshot, err) })
	}()
	return true
}

func (s *app) cliJobTool(action application.Action) string {
	if action.Kind == "meegle.projects" {
		return "meegle"
	}
	tool := strings.ToLower(strings.TrimSpace(payloadString(action.Payload, "tool")))
	if tool != "" {
		if desktopCLIJobSupported(tool) {
			return tool
		}
		return ""
	}
	if session := payloadString(action.Payload, "sessionId"); session != "" {
		for name, current := range s.settingsCLISessions {
			if mapString(current, "sessionId") == session && desktopCLIJobSupported(name) {
				return name
			}
		}
	}
	return ""
}

func desktopCLIJobSupported(tool string) bool {
	switch tool {
	case "meegle", "lark", "codex", "git":
		return true
	default:
		return false
	}
}

func (s *app) cliConfiguredPath(tool string) string {
	key := map[string]string{"meegle": "meegleExecutablePath", "lark": "larkExecutablePath", "codex": "codexExecutablePath", "git": "gitExecutablePath"}[tool]
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
	if s.cliJobs == nil {
		s.cliJobs = &cliJobState{requests: make(map[string]*cliJobRequest), running: make(map[*cliJobRequest]struct{})}
	}
	key := "auth:" + tool
	if action.Kind == "cli.status" {
		key = "status:" + tool
	} else if action.Kind == "cli.auth.session" {
		key = "session:" + tool
	} else if action.Kind == "meegle.projects" {
		key = action.Kind
	}
	state := s.cliJobs
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
	state.next++
	ctx, cancel := context.WithCancel(context.Background())
	request := &cliJobRequest{id: state.next, key: key, tool: tool, path: s.cliConfiguredPath(tool), home: s.snapshot.Home,
		session: payloadString(action.Payload, "sessionId"), page: s.page, section: s.settingsPage, config: s.configJSON,
		action: action, ctx: ctx, cancel: cancel, done: make(chan struct{})}
	if action.Kind == "meegle.projects" {
		request.meegleContext = s.currentRequirementIdentityContext()
		if identity := s.requirementIdentityJobs; identity != nil && identity.confirmed && identity.context == request.meegleContext && identity.state == application.RequirementIdentityAuthenticated {
			request.meegleIdentity = identity.identity
		} else if catalog := s.meegleProjectCatalog; catalog != nil && catalog.context == request.meegleContext {
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
		s.stopCLIAuthPoll(tool)
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
	state.running[request] = struct{}{}
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
			if err == nil {
				err = fmt.Errorf("已请求取消登录，但进程清理尚未完成：%w", cleanup.Err())
			}
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
	if request == nil || s.cliJobs == nil || s.cliJobs.requests[request.key] != request {
		return false
	}
	if s.snapshot.Home != request.home || s.cliConfiguredPath(request.tool) != request.path {
		return false
	}
	if request.action.Kind == "meegle.projects" && request.meegleContext != s.currentRequirementIdentityContext() {
		return false
	}
	if request.session != "" && mapString(s.settingsCLISessions[request.tool], "sessionId") != request.session {
		return false
	}
	return true
}

func (s *app) finishCLIJob(request *cliJobRequest, result application.ActionResult, snapshot application.Snapshot, err error) {
	if s.cliJobs != nil {
		s.cliJobs.recordCleanupFailure(err)
	}
	relevant := s.cliJobRelevant(request)
	if s.cliJobs != nil {
		delete(s.cliJobs.running, request)
	}
	if s.cliJobs != nil && s.cliJobs.requests[request.key] == request {
		delete(s.cliJobs.requests, request.key)
	}
	if !relevant {
		if request.action.Kind == "meegle.projects" && s.meegleCatalogActionRelevant(request.action) {
			s.syncMeegleProjectCatalogContext(s.meegleProjectCatalog)
			s.meegleProjectCatalog.loading = false
			s.meegleProjectCatalog.error = "账号配置已变化，请刷新项目目录。"
		}
		return
	}
	currentSection := s.page == request.page && s.settingsPage == request.section
	if request.userCancelled && !process.IsCleanupFailure(err) {
		if currentSection {
			s.settingsOutput = "登录操作已取消。"
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
			s.settingsOutput = operation + "失败：" + err.Error()
			s.lastError = s.settingsOutput
		}
		if request.action.Kind == "cli.auth.poll" {
			if cleanupFailure {
				s.stopCLIAuthPoll(request.tool)
			} else if !errors.Is(err, context.Canceled) {
				s.scheduleCLIAuthPoll(request.tool, s.settingsCLISessions[request.tool])
			}
		}
		return
	}
	previousOutput := s.settingsOutput
	if request.action.Kind == "meegle.projects" {
		if s.meegleCatalogActionRelevant(request.action) {
			s.meegleProjectCatalog.context = request.meegleContext
			s.meegleProjectCatalog.verifiedIdentity = request.meegleIdentity
		}
		s.applyMeegleProjectCatalogResult(request.action, result)
	} else {
		s.applySettingsActionResult(request.action, result)
	}
	if !currentSection {
		s.settingsOutput = previousOutput
	}
	if request.action.Kind == "cli.status" {
		s.applySettingsCLIDiscoverySnapshot(request.tool, request.config, snapshot)
	}
}

func (s *app) invalidateCLIJobs(tool string) {
	if s.cliJobs == nil {
		return
	}
	for key, request := range s.cliJobs.requests {
		if request.tool == tool {
			request.cancel()
			delete(s.cliJobs.requests, key)
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
	for tool, key := range map[string]string{"meegle": "meegleExecutablePath", "lark": "larkExecutablePath", "codex": "codexExecutablePath", "git": "gitExecutablePath"} {
		if s.snapshot.Home == snapshot.Home && s.cliConfiguredPath(tool) == strings.TrimSpace(mapString(config, key)) {
			continue
		}
		s.invalidateCLIJobs(tool)
		s.stopCLIAuthPoll(tool)
		if tool == "meegle" {
			s.clearMeegleSessionViews()
		}
		delete(s.settingsCLIStatus, tool)
		delete(s.settingsCLISessions, tool)
	}
}

func (state *cliJobState) recordCleanupFailure(err error) {
	if state == nil || !process.IsCleanupFailure(err) {
		return
	}
	state.cleanupMu.Lock()
	if state.cleanupErr == nil {
		state.cleanupErr = err
	}
	state.cleanupMu.Unlock()
}

func (state *cliJobState) cleanupFailure() error {
	state.cleanupMu.Lock()
	defer state.cleanupMu.Unlock()
	return state.cleanupErr
}

func (s *app) closeCLIJobs(ctx context.Context) error {
	state := s.cliJobs
	if state == nil {
		return nil
	}
	requests := state.running
	state.requests = make(map[string]*cliJobRequest)
	for request := range requests {
		request.cancel()
	}
	for request := range requests {
		select {
		case <-request.done:
		case <-ctx.Done():
			return errors.Join(ctx.Err(), state.cleanupFailure())
		}
	}
	state.running = make(map[*cliJobRequest]struct{})
	return state.cleanupFailure()
}
