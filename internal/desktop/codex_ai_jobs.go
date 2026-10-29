package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

type codexAIRequest struct {
	key              string
	cancelRequested  bool
	generation       uint64
	identityRevision uint64
	snapshotRevision uint64
	identity         codexAIIdentity
	action           application.Action
	ctx              context.Context
	cancel           context.CancelFunc
	done             chan struct{}
}

func (s *app) requestCodexAISnapshot() {
	state := s.toolsUnit.state.codexAI
	if state == nil || !state.Open {
		return
	}
	state.snapshotLoading = true
	state.snapshotError = ""
	s.startCodexAIRequest("snapshot", application.Action{
		Kind: "codex.ai/snapshot", TaskID: state.identity.taskID,
		Payload: map[string]any{"cwd": state.identity.cwd, "includeSkills": state.identity.cwd != "" && state.tab == codexAITabSkills},
	})
}

func (s *app) requestCodexAIThreads() {
	state := s.toolsUnit.state.codexAI
	if state == nil || !state.Open || state.identity.cwd == "" || state.bindLoading {
		return
	}
	state.threadsLoading = true
	state.threadsError = ""
	state.bindError = ""
	s.startCodexAIRequest("threads", application.Action{
		Kind: "codex.ai/threads/list", TaskID: state.identity.taskID,
		Payload: map[string]any{"cwd": state.identity.cwd},
	})
}

func (s *app) requestCodexAIMCPSnapshot() {
	state := s.toolsUnit.state.codexAI
	if state == nil || !state.Open || !s.codexAIIdentityCurrent(state) || state.mcpStatusLoading || codexAIMCPBusy(state) {
		return
	}
	state.mcpStatusLoading = true
	state.mcpError, state.mcpCheckError = "", ""
	s.startCodexAIRequest("mcp-status", application.Action{
		Kind: "codex.extensions/mcp/status", TaskID: state.identity.taskID,
		Payload: map[string]any{"cwd": state.identity.cwd},
	})
}

func (s *app) requestCodexAIMCPCheck() {
	state := s.toolsUnit.state.codexAI
	if state == nil || !state.Open || !s.codexAIIdentityCurrent(state) || state.mcpSnapshot == nil || state.mcpCheckLoading || state.mcpStatusLoading || codexAIMCPBusy(state) {
		return
	}
	state.mcpCheckLoading = true
	state.mcpCheckError = ""
	s.startCodexAIRequest("mcp-check", application.Action{
		Kind: "codex.extensions/mcp/check", TaskID: state.identity.taskID,
		Payload: map[string]any{"cwd": state.identity.cwd},
	})
}

func (s *app) requestCodexAINewSession() {
	state := s.toolsUnit.state.codexAI
	if state == nil || !state.Open || !s.codexAIIdentityCurrent(state) || state.newSessionLoading {
		return
	}
	state.newSessionLoading = true
	state.newSessionError, state.newSessionMessage = "", ""
	s.startCodexAIRequest("new-session", codexAINewSessionAction(state.identity))
}

func codexAINewSessionAction(identity codexAIIdentity) application.Action {
	return application.Action{Kind: "codex.run", TaskID: identity.taskID, Path: identity.taskPath,
		Payload: map[string]any{"workspacePath": identity.taskPath, "forceNew": true}}
}

func (s *app) bindCodexAISelectedThread() {
	state := s.toolsUnit.state.codexAI
	if state == nil || !s.codexAIIdentityCurrent(state) || state.bindLoading {
		return
	}
	thread, ok := codexAISelectedThread(state)
	if !ok {
		state.bindError = "请选择当前任务目录中的一个 Codex 会话。"
		return
	}
	state.bindLoading = true
	state.bindError, state.bindMessage = "", ""
	threadID := thread.ID
	s.startCodexAIRequest("bind", codexAIThreadBindingAction(state.identity, &threadID))
}

func (s *app) unbindCodexAIThread() {
	state := s.toolsUnit.state.codexAI
	if state == nil || !s.codexAIIdentityCurrent(state) || state.bindLoading || state.boundThreadID == "" {
		return
	}
	state.bindLoading = true
	state.bindError, state.bindMessage = "", ""
	// The explicit null is required by the backend to distinguish unbind from
	// an accidentally omitted threadId.
	s.startCodexAIRequest("bind", codexAIThreadBindingAction(state.identity, nil))
}

func codexAIThreadBindingAction(identity codexAIIdentity, threadID *string) application.Action {
	var value any
	if threadID != nil {
		value = *threadID
	}
	return application.Action{
		Kind: "codex.ai/threads/bind", TaskID: identity.taskID,
		Payload: map[string]any{"cwd": identity.cwd, "threadId": value},
	}
}

func codexAISelectedThread(state *codexAIState) (codexAIThread, bool) {
	if state == nil || state.selectedThread == "" {
		return codexAIThread{}, false
	}
	for _, thread := range state.threads {
		if thread.ID == state.selectedThread && normalizeIdentityPath(thread.CWD) == normalizeIdentityPath(state.identity.cwd) {
			return thread, true
		}
	}
	return codexAIThread{}, false
}

func (s *app) readCodexAIFile(path string) {
	state := s.toolsUnit.state.codexAI
	if state == nil || !s.codexAIIdentityCurrent(state) {
		return
	}
	path = filepath.Clean(strings.TrimSpace(path))
	if !codexAIFileAllowed(state.snapshot, path) {
		state.fileError = "只能读取当前 Codex 快照列出的配置或说明文件。"
		return
	}
	if codexAIPathKey(state.filePath) != codexAIPathKey(path) {
		state.resetFilePreview()
		state.fileContent = ""
	}
	state.filePath = path
	state.fileLoading = true
	state.fileError = ""
	state.fileRevision = state.snapshotRevision
	s.startCodexAIRequest("file-read", application.Action{
		Kind: "codex.ai/file/read", TaskID: state.identity.taskID, Path: path,
		Payload: map[string]any{"path": path},
	})
}

func (s *app) openCodexAIFileExternally(path string) {
	state := s.toolsUnit.state.codexAI
	if state == nil || !s.codexAIIdentityCurrent(state) {
		return
	}
	path = filepath.Clean(strings.TrimSpace(path))
	if !codexAIFileAllowed(state.snapshot, path) {
		state.externalError = "只能打开当前 Codex 快照列出的文件。"
		return
	}
	state.externalPath = path
	state.externalError = ""
	state.externalMessage = ""
	s.startCodexAIRequest("external-open", application.Action{Kind: "system.open", Path: path})
}

func codexAIFileAllowed(snapshot *codexAISnapshot, path string) bool {
	if snapshot == nil || strings.TrimSpace(path) == "" {
		return false
	}
	target := codexAIPathKey(path)
	for _, file := range snapshot.Files {
		if file.Exists && codexAIPathKey(file.File) == target {
			return true
		}
	}
	for _, skill := range snapshot.Skills {
		if strings.TrimSpace(skill.Path) != "" && codexAIPathKey(skill.Path) == target {
			return true
		}
	}
	for _, layer := range snapshot.Layers {
		if strings.TrimSpace(layer.File) != "" && codexAIPathKey(layer.File) == target {
			return true
		}
	}
	return false
}

func (s *app) startCodexAIRequest(key string, action application.Action) {
	state := s.toolsUnit.state.codexAI
	if state == nil || !s.codexAIIdentityCurrent(state) {
		return
	}
	if key == "mcp-mutate" || key == "mcp-auth" {
		s.cancelCodexAIRequest(state, "mcp-status")
		s.cancelCodexAIRequest(state, "mcp-check")
	}
	if s.backend == nil || s.win == nil || s.win.IsDestroyed() {
		s.setCodexAIRequestFailure(key, errors.New("Codex 后端或窗口不可用。"))
		return
	}
	if previous := state.requests[key]; previous != nil {
		previous.cancel()
	}
	state.generations[key]++
	ctx, cancel := context.WithCancel(context.Background())
	request := &codexAIRequest{
		key:              key,
		generation:       state.generations[key],
		identityRevision: state.identityRevision,
		snapshotRevision: state.snapshotRevision,
		identity:         state.identity,
		action:           cloneCodexAIAction(action),
		ctx:              ctx, cancel: cancel, done: make(chan struct{}),
	}
	state.requests[key] = request
	state.mu.Lock()
	if state.closed {
		state.mu.Unlock()
		cancel()
		delete(state.requests, key)
		s.setCodexAIRequestFailure(key, errors.New("Codex 配置面板已关闭。"))
		return
	}
	if state.running == nil {
		state.running = make(map[*codexAIRequest]struct{})
	}
	state.running[request] = struct{}{}
	state.mu.Unlock()

	backend, win := s.backend, s.win
	win.Invalidate()
	go func() {
		result, err := executeCodexAIRequest(backend, request)
		state.recordCleanupFailure(err)
		if !win.IsDestroyed() {
			win.Update(func() { s.finishCodexAIRequest(request, result, err) })
		}
		cancel()
		state.mu.Lock()
		delete(state.running, request)
		close(request.done)
		state.mu.Unlock()
	}()
}

func executeCodexAIRequest(backend application.Backend, request *codexAIRequest) (application.ActionResult, error) {
	if backend == nil || request == nil {
		return application.ActionResult{}, errors.New("Codex 请求缺少后端或请求状态")
	}
	if err := request.ctx.Err(); err != nil {
		return application.ActionResult{}, err
	}
	if request.key == "new-session" {
		dispatched, dispatchErr := backend.Dispatch(request.ctx, request.action)
		if process.IsCleanupFailure(dispatchErr) || errors.Is(dispatchErr, context.Canceled) || errors.Is(dispatchErr, context.DeadlineExceeded) {
			return application.ActionResult{}, dispatchErr
		}
		snapshot, snapshotErr := backend.Snapshot(request.ctx)
		if process.IsCleanupFailure(snapshotErr) || errors.Is(snapshotErr, context.Canceled) || errors.Is(snapshotErr, context.DeadlineExceeded) {
			return application.ActionResult{}, snapshotErr
		}
		return application.ActionResult{Data: codexAINewSessionOutcome{
			Dispatch: dispatched, DispatchError: errorString(dispatchErr), Snapshot: snapshot,
			SnapshotError: errorString(snapshotErr),
		}}, nil
	}
	return backend.Dispatch(request.ctx, request.action)
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func (s *app) cancelCodexAIRequest(state *codexAIState, key string) {
	if state == nil {
		return
	}
	if request := state.requests[key]; request != nil {
		request.cancel()
		delete(state.requests, key)
		state.generations[key]++
	}
	switch key {
	case "snapshot":
		state.snapshotLoading = false
	case "threads":
		state.threadsLoading = false
	case "bind":
		state.bindLoading = false
	case "file-read":
		state.fileLoading = false
	case "mcp-status":
		state.mcpStatusLoading = false
	case "mcp-check":
		state.mcpCheckLoading = false
	case "mcp-mutate":
		state.mcpMutationLoading = false
		if state.mcpEditor != nil {
			state.mcpEditor.Saving = false
		}
	case "mcp-auth":
		state.mcpAuthLoading = false
	case "new-session":
		state.newSessionLoading = false
	}
}

func cloneCodexAIAction(action application.Action) application.Action {
	if action.Payload == nil {
		return action
	}
	clone := make(map[string]any, len(action.Payload))
	for key, value := range action.Payload {
		clone[key] = value
	}
	action.Payload = clone
	return action
}

func (s *app) codexAIRequestRelevant(request *codexAIRequest) bool {
	state := s.toolsUnit.state.codexAI
	if state == nil || request == nil || state.requests[request.key] != request ||
		state.generations[request.key] != request.generation || state.identityRevision != request.identityRevision ||
		state.identity.key() != request.identity.key() || !s.codexAIIdentityCurrent(state) {
		return false
	}
	if request.key == "file-read" && (state.fileRevision != request.snapshotRevision ||
		state.snapshotRevision != request.snapshotRevision || codexAIPathKey(state.filePath) != codexAIPathKey(request.action.Path) ||
		!codexAIFileAllowed(state.snapshot, request.action.Path)) {
		return false
	}
	if strings.HasPrefix(request.key, "mcp-") && payloadString(request.action.Payload, "cwd") != request.identity.cwd {
		return false
	}
	if request.key == "new-session" && (payloadString(request.action.Payload, "workspacePath") != request.identity.taskPath ||
		request.action.Payload["forceNew"] != true || request.action.TaskID != request.identity.taskID) {
		return false
	}
	return true
}

func (s *app) finishCodexAIRequest(request *codexAIRequest, result application.ActionResult, err error) {
	if s.toolsUnit.state.codexAI != nil {
		s.toolsUnit.state.codexAI.recordCleanupFailure(err)
	}
	if !s.codexAIRequestRelevant(request) {
		return
	}
	state := s.toolsUnit.state.codexAI
	delete(state.requests, request.key)
	cleanupFailure := process.IsCleanupFailure(err)
	if request.cancelRequested && !cleanupFailure {
		if request.key == "mcp-mutate" {
			s.resetCodexAIMCPSwitch(request.action)
		}
		s.setCodexAIRequestFailure(request.key, errors.New("操作已取消，可刷新确认当前状态后重试"))
		return
	}
	if err != nil {
		if cleanupFailure || !errors.Is(err, context.Canceled) {
			if request.key == "mcp-mutate" {
				s.resetCodexAIMCPSwitch(request.action)
			}
			s.setCodexAIRequestFailure(request.key, err)
		}
		return
	}
	switch request.key {
	case "snapshot":
		var snapshot codexAISnapshot
		if err := decodeCodexAIData(result.Data, &snapshot); err != nil {
			s.setCodexAIRequestFailure(request.key, fmt.Errorf("Codex 配置响应无效：%w", err))
			return
		}
		if snapshot.CWD == nil || normalizeIdentityPath(*snapshot.CWD) != normalizeIdentityPath(request.identity.cwd) {
			s.setCodexAIRequestFailure(request.key, errors.New("Codex 配置响应目录与当前任务不一致。"))
			return
		}
		previousFile := state.filePath
		wasFileLoading := state.fileLoading
		if pending := state.requests["file-read"]; pending != nil {
			pending.cancel()
			delete(state.requests, "file-read")
			state.generations["file-read"]++
		}
		state.snapshot = &snapshot
		state.snapshotLoading, state.snapshotLoaded, state.snapshotError = false, true, ""
		state.skillsLoaded = codexAIPayloadBool(request.action.Payload, "includeSkills")
		state.snapshotRevision++
		if previousFile != "" {
			if !codexAIFileAllowed(state.snapshot, previousFile) {
				state.filePath, state.fileContent, state.fileError = "", "", ""
				state.fileLoading = false
				state.resetFilePreview()
			} else {
				state.fileRevision = state.snapshotRevision
				state.fileLoading = false
				if wasFileLoading {
					s.readCodexAIFile(previousFile)
				}
			}
		}
	case "threads":
		var response codexAIThreadsResult
		if err := decodeCodexAIData(result.Data, &response); err != nil {
			s.setCodexAIRequestFailure(request.key, fmt.Errorf("Codex 会话响应无效：%w", err))
			return
		}
		threads := make([]codexAIThread, 0, len(response.Threads))
		for _, thread := range response.Threads {
			if thread.ID != "" && normalizeIdentityPath(thread.CWD) == normalizeIdentityPath(request.identity.cwd) {
				threads = append(threads, thread)
			}
		}
		state.threads = threads
		state.threadsLoading, state.threadsLoaded, state.threadsError = false, true, ""
		state.boundThreadID = ""
		if response.BoundThreadID != nil {
			state.boundThreadID = strings.TrimSpace(*response.BoundThreadID)
		}
		state.selectedThread = ""
		for _, thread := range threads {
			if thread.ID == state.boundThreadID {
				state.selectedThread = thread.ID
				break
			}
		}
	case "bind":
		var response struct {
			CWD      string  `json:"cwd"`
			ThreadID *string `json:"threadId"`
		}
		if err := decodeCodexAIData(result.Data, &response); err != nil {
			s.setCodexAIRequestFailure(request.key, fmt.Errorf("Codex 会话关联响应无效：%w", err))
			return
		}
		if normalizeIdentityPath(response.CWD) != normalizeIdentityPath(request.identity.cwd) {
			s.setCodexAIRequestFailure(request.key, errors.New("Codex 会话关联响应目录与当前任务不一致。"))
			return
		}
		state.boundThreadID = ""
		if response.ThreadID != nil {
			state.boundThreadID = strings.TrimSpace(*response.ThreadID)
		}
		state.selectedThread = state.boundThreadID
		state.bindLoading, state.bindError = false, ""
		if state.boundThreadID == "" {
			state.bindMessage = "已解除当前任务的 Codex 会话关联。"
		} else {
			state.bindMessage = "已关联当前目录中的 Codex 会话。"
		}
	case "file-read":
		content, ok := result.Data.(string)
		if !ok {
			if result.Text != "" {
				content, ok = result.Text, true
			}
		}
		if !ok {
			s.setCodexAIRequestFailure(request.key, errors.New("Codex 文件读取响应不是文本。"))
			return
		}
		state.fileContent, state.fileLoading, state.fileError = content, false, ""
	case "external-open":
		state.externalError = ""
		state.externalMessage = "已交由系统默认应用打开：" + filepath.Base(state.externalPath)
	case "mcp-status":
		var snapshot codexAISnapshot
		if err := decodeCodexAIData(result.Data, &snapshot); err != nil {
			s.setCodexAIRequestFailure(request.key, fmt.Errorf("MCP 配置响应无效：%w", err))
			return
		}
		if snapshot.CWD == nil || normalizeIdentityPath(*snapshot.CWD) != normalizeIdentityPath(request.identity.cwd) {
			s.setCodexAIRequestFailure(request.key, errors.New("MCP 配置响应目录与当前任务不一致。"))
			return
		}
		state.mcpSnapshot = &snapshot
		state.mcpStatusLoading, state.mcpError = false, ""
		state.mcpConnections = nil
		state.mcpSwitches = nil
	case "mcp-check":
		var response struct {
			CWD         string                          `json:"cwd"`
			Connections map[string]codexAIMCPConnection `json:"connections"`
		}
		if err := decodeCodexAIData(result.Data, &response); err != nil {
			s.setCodexAIRequestFailure(request.key, fmt.Errorf("MCP 连接检测响应无效：%w", err))
			return
		}
		if normalizeIdentityPath(response.CWD) != normalizeIdentityPath(request.identity.cwd) {
			s.setCodexAIRequestFailure(request.key, errors.New("MCP 连接检测响应目录与当前任务不一致。"))
			return
		}
		state.mcpConnections = response.Connections
		state.mcpCheckLoading, state.mcpCheckError = false, ""
	case "mcp-mutate":
		var response codexAIMCPMutationResult
		if err := decodeCodexAIData(result.Data, &response); err != nil {
			s.setCodexAIRequestFailure(request.key, fmt.Errorf("MCP 保存响应无效：%w", err))
			return
		}
		if !response.Saved {
			s.setCodexAIRequestFailure(request.key, errors.New("MCP 后端未确认配置已保存。"))
			return
		}
		if response.Snapshot != nil {
			if response.Snapshot.CWD == nil || normalizeIdentityPath(*response.Snapshot.CWD) != normalizeIdentityPath(request.identity.cwd) {
				s.setCodexAIRequestFailure(request.key, errors.New("MCP 保存响应目录与当前任务不一致。"))
				return
			}
			state.mcpSnapshot = response.Snapshot
			state.mcpConnections = nil
			state.mcpSwitches = nil
		}
		state.mcpMutationLoading, state.mcpError = false, ""
		state.mcpEditor, state.mcpDelete = nil, nil
		state.mcpMessage = "MCP 配置已保存；新建或重启 Codex 会话后生效。"
		if response.RefreshError != "" || response.Snapshot == nil {
			state.mcpMessage = "配置已保存，正在重新读取 MCP 状态。"
			s.requestCodexAIMCPSnapshot()
		}
	case "mcp-auth":
		state.mcpAuthLoading, state.mcpError = false, ""
		if result.Message != "" {
			state.mcpMessage = result.Message
		} else if codexAIPayloadBool(request.action.Payload, "login") {
			state.mcpMessage = "MCP 授权操作已完成。"
		} else {
			state.mcpMessage = "MCP 退出授权操作已完成。"
		}
		state.mcpConnections = nil
		s.requestCodexAIMCPSnapshot()
	case "new-session":
		outcome, ok := result.Data.(codexAINewSessionOutcome)
		if !ok {
			s.setCodexAIRequestFailure(request.key, errors.New("Codex 新会话响应格式无效。"))
			return
		}
		state.newSessionLoading = false
		if outcome.SnapshotError == "" {
			s.refreshCodexAITaskWorkspaceLaunches(request.identity, outcome.Snapshot)
		} else {
			state.newSessionError = "新会话操作已返回，但刷新任务状态失败：" + outcome.SnapshotError
			if manifest, isManifest := outcome.Dispatch.Data.(domain.TaskManifest); isManifest {
				s.refreshCodexAITaskLaunchesFromManifest(request.identity, manifest)
			}
		}
		if outcome.DispatchError != "" {
			state.newSessionError = codexAIErrorMessage(errors.New(outcome.DispatchError))
			if outcome.SnapshotError != "" {
				state.newSessionError += "；刷新任务状态失败：" + outcome.SnapshotError
			}
		} else {
			state.newSessionMessage = outcome.Dispatch.Message
			if state.newSessionMessage == "" {
				state.newSessionMessage = "Codex 新会话操作已完成。"
			}
		}
		failed := false
		for _, current := range s.taskUnit.state.tasks {
			if current.id != request.identity.taskID || normalizeIdentityPath(current.path) != normalizeIdentityPath(request.identity.taskPath) {
				continue
			}
			for _, launch := range current.workspaceToolLaunches {
				if launch.ToolID == "codex" && string(launch.Status) == "FAILED" {
					failed = true
					state.newSessionError = firstNonEmptyCodexAI(launch.Message, outcome.Dispatch.Message, "Codex 未能新建会话。")
					state.newSessionMessage = ""
				}
			}
			break
		}
		if !failed && outcome.DispatchError == "" && outcome.SnapshotError == "" && s.win != nil && !s.win.IsDestroyed() {
			s.requestCodexAIThreads()
		}
	}
}

func (s *app) refreshCodexAITaskLaunchesFromManifest(identity codexAIIdentity, manifest domain.TaskManifest) {
	state := s.toolsUnit.state.codexAI
	if state == nil || !s.codexAIIdentityCurrent(state) || state.identity.key() != identity.key() {
		return
	}
	for index := range s.taskUnit.state.tasks {
		if s.taskUnit.state.tasks[index].id == identity.taskID && s.taskUnit.state.tasks[index].createdAt == identity.taskCreatedAt &&
			normalizeIdentityPath(s.taskUnit.state.tasks[index].path) == normalizeIdentityPath(identity.taskPath) {
			s.taskUnit.state.tasks[index].workspaceToolLaunches = append(s.taskUnit.state.tasks[index].workspaceToolLaunches[:0], manifest.WorkspaceToolLaunches...)
			return
		}
	}
}

func (s *app) resetCodexAIMCPSwitch(action application.Action) {
	state := s.toolsUnit.state.codexAI
	if state == nil || state.mcpSnapshot == nil || action.Kind != "codex.extensions/mcp/set" {
		return
	}
	name, file := payloadString(action.Payload, "name"), payloadString(action.Payload, "file")
	for _, server := range state.mcpSnapshot.Servers {
		if server.Name == name && codexAIPathKey(server.File) == codexAIPathKey(file) {
			if value := state.mcpSwitches[codexAIMCPServerKey(server)]; value != nil {
				*value = codexAIMCPEnabled(server)
			}
			return
		}
	}
}

func (s *app) refreshCodexAITaskWorkspaceLaunches(identity codexAIIdentity, snapshot application.Snapshot) {
	if s.toolsUnit.state.codexAI == nil || !s.codexAIIdentityCurrent(s.toolsUnit.state.codexAI) || s.toolsUnit.state.codexAI.identity.key() != identity.key() ||
		normalizeIdentityPath(snapshot.Home) != normalizeIdentityPath(identity.home) {
		return
	}
	for _, refreshed := range snapshot.Tasks {
		if refreshed.ID != identity.taskID || refreshed.CreatedAt != identity.taskCreatedAt ||
			normalizeIdentityPath(refreshed.Path) != normalizeIdentityPath(identity.taskPath) {
			continue
		}
		for index := range s.taskUnit.state.tasks {
			if s.taskUnit.state.tasks[index].id == identity.taskID && s.taskUnit.state.tasks[index].createdAt == identity.taskCreatedAt &&
				normalizeIdentityPath(s.taskUnit.state.tasks[index].path) == normalizeIdentityPath(identity.taskPath) {
				s.taskUnit.state.tasks[index].workspaceToolLaunches = append(s.taskUnit.state.tasks[index].workspaceToolLaunches[:0], refreshed.WorkspaceToolLaunches...)
				return
			}
		}
	}
}

func decodeCodexAIData(data any, target any) error {
	if data == nil {
		return errors.New("响应数据为空")
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(encoded, target); err != nil {
		return err
	}
	return nil
}

func (s *app) setCodexAIRequestFailure(key string, err error) {
	state := s.toolsUnit.state.codexAI
	if state == nil {
		return
	}
	message := codexAIErrorMessage(err)
	switch key {
	case "snapshot":
		state.snapshotLoading, state.snapshotError = false, message
	case "threads":
		state.threadsLoading, state.threadsError = false, message
	case "bind":
		state.bindLoading, state.bindError = false, message
	case "file-read":
		state.fileLoading, state.fileError = false, message
	case "external-open":
		state.externalError = message
	case "mcp-status":
		state.mcpStatusLoading, state.mcpError = false, message
	case "mcp-check":
		state.mcpCheckLoading, state.mcpCheckError = false, message
	case "mcp-mutate":
		state.mcpMutationLoading, state.mcpError = false, message
		if state.mcpEditor != nil {
			state.mcpEditor.Saving = false
			state.mcpEditor.Error = message
		}
	case "mcp-auth":
		state.mcpAuthLoading, state.mcpError = false, message
	case "new-session":
		state.newSessionLoading, state.newSessionError = false, message
	}
}

func (state *codexAIState) recordCleanupFailure(err error) {
	if state == nil || !process.IsCleanupFailure(err) {
		return
	}
	state.mu.Lock()
	if state.cleanupErr == nil {
		state.cleanupErr = err
	}
	state.mu.Unlock()
}

func (state *codexAIState) cleanupFailure() error {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.cleanupErr
}

func (s *app) closeCodexAIJobs(ctx context.Context) error {
	state := s.toolsUnit.state.codexAI
	if state == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	state.mu.Lock()
	state.closed = true
	state.Open = false
	requests := make([]*codexAIRequest, 0, len(state.running))
	for request := range state.running {
		requests = append(requests, request)
	}
	state.mu.Unlock()
	closeMarkdownPreviewState(state.fileMarkdown)
	for _, request := range requests {
		request.cancel()
	}
	for _, request := range requests {
		select {
		case <-request.done:
		case <-ctx.Done():
			return errors.Join(ctx.Err(), state.cleanupFailure())
		}
	}
	return state.cleanupFailure()
}
