package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"

	"github.com/egoist/mygo/ui"
)

const workspaceCommandMaxLogLines = 20_000

type workspaceCommandStatus string

const (
	workspaceCommandRunning   workspaceCommandStatus = "RUNNING"
	workspaceCommandSucceeded workspaceCommandStatus = "SUCCEEDED"
	workspaceCommandFailed    workspaceCommandStatus = "FAILED"
	workspaceCommandCancelled workspaceCommandStatus = "CANCELLED"
)

type workspaceCommandOutputLine struct {
	Stream string
	Text   string
}

// workspaceCommandUIState binds command output to the exact task, workspace,
// command and invocation that produced it. The callback token is sent through
// Action.Payload so stale output cannot leak into a later selection.
type workspaceCommandUIState struct {
	Token                string
	Home                 string
	TaskID               string
	TaskPath             string
	TaskCreatedAt        string
	TaskGroupID          string
	Workspace            application.Workspace
	WorkspaceFingerprint string
	Command              domain.WorkspaceCommandConfig
	Status               workspaceCommandStatus
	StartedAt            time.Time
	Duration             time.Duration
	ExitCode             int
	HasExitCode          bool
	Error                string
	Lines                []workspaceCommandOutputLine
	LineStart            int
	Truncated            bool
	StreamObserved       bool
	StopRequested        bool
	Open                 bool
	Scroll               ui.ScrollState
}

func (s *app) renderWorkspaceCommandActions(c *ui.Context, p palette, t task, w application.Workspace) {
	commands := s.workspaceCommandsFor(t, w)
	if len(commands) == 0 {
		return
	}
	iconGroup(c, p, func() {
		for _, command := range commands {
			name := strings.TrimSpace(command.Name)
			if name == "" {
				name = command.ID
			}
			tooltip := name + " · " + workspaceCommandDisplay(command)
			button := iconButtonDisabled(
				c,
				"workspace-command-"+w.ServiceID+"-"+w.ModuleID+"-"+command.ID,
				workspaceCommandIcon(command.IconKey),
				tooltip,
				p,
				s.workspaceCommandBusy() || !workspaceCommandHealthy(w),
			)
			if button.Clicked() && !s.workspaceCommandBusy() && workspaceCommandHealthy(w) {
				s.beginWorkspaceCommand(t, w, command)
			}
		}
	})
}

func (s *app) workspaceCommandsFor(t task, w application.Workspace) []domain.WorkspaceCommandConfig {
	if !workspaceCommandHealthy(w) {
		return nil
	}
	var config domain.AppConfig
	if strings.TrimSpace(s.configJSON) == "" || json.Unmarshal([]byte(s.configJSON), &config) != nil {
		return nil
	}
	for _, group := range config.Groups {
		if group.ID != t.groupID {
			continue
		}
		for _, service := range group.Services {
			if service.ID != w.ServiceID {
				continue
			}
			for _, module := range service.Modules {
				if module.ID != w.ModuleID {
					continue
				}
				commands := make([]domain.WorkspaceCommandConfig, 0, len(module.CustomCommands))
				for _, command := range module.CustomCommands {
					if command.Enabled {
						commands = append(commands, command)
					}
				}
				return commands
			}
		}
	}
	return nil
}

func workspaceCommandHealthy(w application.Workspace) bool {
	return strings.EqualFold(w.State, string(domain.WorkspaceReady)) ||
		strings.EqualFold(w.State, string(domain.WorkspaceReadyWithWarnings))
}

func (s *app) beginWorkspaceCommand(t task, w application.Workspace, command domain.WorkspaceCommandConfig) {
	if s.backend == nil {
		s.lastError = "桌面后端不可用，无法执行快捷命令"
		return
	}
	if s.workspaceCommandBusy() || !workspaceCommandHealthy(w) || !command.Enabled || strings.TrimSpace(command.ID) == "" {
		return
	}
	configured := false
	for _, candidate := range s.workspaceCommandsFor(t, w) {
		if candidate.ID == command.ID && candidate.Enabled {
			command = candidate
			configured = true
			break
		}
	}
	if !configured {
		return
	}

	token := newDesktopEditorID("workspace-command")
	fingerprint := workspaceCommandFingerprint(t, w)
	s.workspaceCommand = &workspaceCommandUIState{
		Token:                token,
		Home:                 s.snapshot.Home,
		TaskID:               t.id,
		TaskPath:             filepath.Clean(t.path),
		TaskCreatedAt:        t.createdAt,
		TaskGroupID:          t.groupID,
		Workspace:            w,
		WorkspaceFingerprint: fingerprint,
		Command:              command,
		Status:               workspaceCommandRunning,
		StartedAt:            time.Now(),
		Lines:                []workspaceCommandOutputLine{},
		Open:                 true,
	}
	s.act("workspace.command", t.id, w.ServiceID, w.ModuleID, w.Path, "", map[string]any{
		"commandId":            command.ID,
		"commandToken":         token,
		"taskPath":             filepath.Clean(t.path),
		"taskCreatedAt":        t.createdAt,
		"taskGroupId":          t.groupID,
		"workspaceId":          w.ID,
		"workspacePath":        filepath.Clean(w.Path),
		"workspaceBranch":      w.Branch,
		"workspaceFingerprint": fingerprint,
	})
}

func workspaceCommandFingerprint(t task, w application.Workspace) string {
	value := struct {
		TaskID    string
		TaskPath  string
		CreatedAt string
		GroupID   string
		Workspace application.Workspace
	}{
		TaskID: t.id, TaskPath: filepath.Clean(t.path), CreatedAt: t.createdAt,
		GroupID: t.groupID, Workspace: w,
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func (s *app) workspaceCommandActionRelevant(action application.Action) bool {
	state := s.workspaceCommand
	if state == nil || state.Status != workspaceCommandRunning || !workspaceCommandActionMatchesState(action, state) {
		return false
	}
	if !s.workspaceCommandIdentityCurrent(state) {
		s.invalidateWorkspaceCommand(state)
		return false
	}
	return true
}

// Matches only captured command identity. Ordinary actions separately require
// RUNNING; the cleanup completion path may also own a stopped CANCELLED state.
func workspaceCommandActionMatchesState(action application.Action, state *workspaceCommandUIState) bool {
	if state == nil || action.Kind != "workspace.command" || payloadString(action.Payload, "commandToken") != state.Token ||
		payloadString(action.Payload, "commandId") != state.Command.ID ||
		action.TaskID != state.TaskID || action.ServiceID != state.Workspace.ServiceID || action.ModuleID != state.Workspace.ModuleID ||
		!sameWorkspaceCommandPath(action.Path, state.Workspace.Path) ||
		!sameWorkspaceCommandPath(payloadString(action.Payload, "taskPath"), state.TaskPath) ||
		payloadString(action.Payload, "taskCreatedAt") != state.TaskCreatedAt ||
		payloadString(action.Payload, "taskGroupId") != state.TaskGroupID ||
		payloadString(action.Payload, "workspaceId") != state.Workspace.ID ||
		!sameWorkspaceCommandPath(payloadString(action.Payload, "workspacePath"), state.Workspace.Path) ||
		payloadString(action.Payload, "workspaceBranch") != state.Workspace.Branch ||
		payloadString(action.Payload, "workspaceFingerprint") != state.WorkspaceFingerprint {
		return false
	}

	return true
}

func (s *app) workspaceCommandIdentityCurrent(state *workspaceCommandUIState) bool {
	if state == nil {
		return false
	}
	if s.snapshot.Home != state.Home {
		return false
	}
	current := s.currentTask()
	if current.id != state.TaskID || current.createdAt != state.TaskCreatedAt || current.groupID != state.TaskGroupID ||
		!sameWorkspaceCommandPath(current.path, state.TaskPath) {
		return false
	}
	for _, workspace := range current.workspaces {
		if workspace.ID == state.Workspace.ID && workspace.ServiceID == state.Workspace.ServiceID &&
			workspace.ModuleID == state.Workspace.ModuleID && sameWorkspaceCommandPath(workspace.Path, state.Workspace.Path) &&
			workspaceCommandFingerprint(current, workspace) == state.WorkspaceFingerprint {
			return true
		}
	}
	return false
}

func (s *app) invalidateWorkspaceCommand(state *workspaceCommandUIState) {
	if state == nil || s.workspaceCommand != state {
		return
	}
	if state.Status == workspaceCommandRunning {
		state.StopRequested = true
		state.Status = workspaceCommandCancelled
		state.Duration = time.Since(state.StartedAt)
		s.cancelWorkspaceCommandJob(state.Token)
	}
	s.workspaceCommand = nil
}

func sameWorkspaceCommandPath(left, right string) bool {
	left, right = filepath.Clean(strings.TrimSpace(left)), filepath.Clean(strings.TrimSpace(right))
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func (s *app) workspaceCommandStream(action application.Action) func(process.StreamEvent) {
	win := s.win
	action = cloneWorkspaceCommandAction(action)
	return func(event process.StreamEvent) {
		if win == nil || win.IsDestroyed() {
			return
		}
		win.Update(func() {
			if !s.workspaceCommandActionRelevant(action) || s.workspaceCommand == nil {
				return
			}
			state := s.workspaceCommand
			if state.Status != workspaceCommandRunning {
				return
			}
			if state.Scroll.Y >= state.Scroll.MaxY {
				state.Scroll.Y = math.MaxFloat32
			}
			appendWorkspaceCommandEvent(state, event.Stream, event.Text)
		})
	}
}

func cloneWorkspaceCommandAction(action application.Action) application.Action {
	payload := make(map[string]any, len(action.Payload))
	for key, value := range action.Payload {
		payload[key] = value
	}
	action.Payload = payload
	return action
}

func appendWorkspaceCommandLine(state *workspaceCommandUIState, line workspaceCommandOutputLine) {
	if len(state.Lines) < workspaceCommandMaxLogLines {
		state.Lines = append(state.Lines, line)
		return
	}
	state.Lines[state.LineStart] = line
	state.LineStart = (state.LineStart + 1) % len(state.Lines)
	state.Truncated = true
}

func appendWorkspaceCommandEvent(state *workspaceCommandUIState, stream, line string) {
	if state == nil {
		return
	}
	stream = workspaceCommandStreamName(stream)
	state.StreamObserved = true
	appendWorkspaceCommandLine(state, workspaceCommandOutputLine{Stream: stream, Text: line})
}

func appendWorkspaceCommandText(state *workspaceCommandUIState, stream, text string) {
	if state == nil || text == "" {
		return
	}
	stream = workspaceCommandStreamName(stream)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	for _, line := range lines {
		appendWorkspaceCommandLine(state, workspaceCommandOutputLine{Stream: stream, Text: strings.TrimSuffix(line, "\r")})
	}
}

func workspaceCommandStreamName(stream string) string {
	if strings.EqualFold(strings.TrimSpace(stream), "STDERR") {
		return "STDERR"
	}
	return "STDOUT"
}

func (s *app) applyWorkspaceCommandResult(action application.Action, result application.ActionResult) {
	if !s.workspaceCommandActionRelevant(action) || s.workspaceCommand == nil {
		return
	}
	state := s.workspaceCommand
	commandResult, err := workspaceCommandResult(result.Data)
	if err != nil {
		state.Status = workspaceCommandFailed
		state.Error = "读取命令结果失败：" + err.Error()
		state.Duration = time.Since(state.StartedAt)
		state.StopRequested = false
		return
	}
	if !state.StreamObserved {
		appendWorkspaceCommandText(state, "STDOUT", commandResult.Stdout)
		appendWorkspaceCommandText(state, "STDERR", commandResult.Stderr)
	}
	state.StopRequested = false
	state.ExitCode, state.HasExitCode = commandResult.ExitCode, true
	state.Duration = time.Since(state.StartedAt)
	if commandResult.ExitCode == 0 {
		state.Status = workspaceCommandSucceeded
		state.Error = ""
	} else {
		state.Status = workspaceCommandFailed
		state.Error = firstWorkspaceCommandLine(commandResult.Stderr)
		if state.Error == "" {
			state.Error = fmt.Sprintf("命令退出码：%d", commandResult.ExitCode)
		}
	}
}

func workspaceCommandResult(data any) (domain.CommandResult, error) {
	if value, ok := data.(domain.CommandResult); ok {
		return value, nil
	}
	if value, ok := data.(*domain.CommandResult); ok && value != nil {
		return *value, nil
	}
	if data == nil {
		return domain.CommandResult{}, errors.New("后端未返回命令退出码和输出")
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return domain.CommandResult{}, err
	}
	var result domain.CommandResult
	if err := json.Unmarshal(encoded, &result); err != nil {
		return domain.CommandResult{}, err
	}
	return result, nil
}

func firstWorkspaceCommandLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) != "" {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

func (s *app) applyWorkspaceCommandFailure(action application.Action, err error) {
	if !s.workspaceCommandActionRelevant(action) || s.workspaceCommand == nil {
		return
	}
	finishWorkspaceCommandFailure(s.workspaceCommand, err)
}

func finishWorkspaceCommandFailure(state *workspaceCommandUIState, err error) {
	if state == nil {
		return
	}
	state.Duration = time.Since(state.StartedAt)
	state.StopRequested = false
	if !process.IsCleanupFailure(err) && errors.Is(err, context.Canceled) {
		state.Status = workspaceCommandCancelled
		state.Error = ""
		return
	}
	state.Status = workspaceCommandFailed
	if err != nil {
		state.Error = err.Error()
	} else {
		state.Error = "快捷命令执行失败"
	}
}

func (s *app) requestWorkspaceCommandStop() {
	state := s.workspaceCommand
	if state == nil || state.Status != workspaceCommandRunning || state.StopRequested {
		return
	}
	state.StopRequested = true
	state.Status = workspaceCommandCancelled
	state.Duration = time.Since(state.StartedAt)
	s.cancelWorkspaceCommandJob(state.Token)
	if s.win != nil {
		s.win.Invalidate()
	}
}

func (s *app) clearWorkspaceCommandSelection() {
	state := s.workspaceCommand
	if state != nil && state.Status == workspaceCommandRunning {
		state.StopRequested = true
		state.Status = workspaceCommandCancelled
		state.Duration = time.Since(state.StartedAt)
	}
	if state != nil {
		s.cancelWorkspaceCommandJob(state.Token)
	}
	s.workspaceCommand = nil
	if s.win != nil {
		s.win.Invalidate()
	}
}

func (s *app) renderWorkspaceCommandDialog(c *ui.Context, p palette) {
	state := s.workspaceCommand
	if state == nil || !state.Open {
		return
	}
	if state.Status == workspaceCommandRunning && !s.workspaceCommandIdentityCurrent(state) {
		s.invalidateWorkspaceCommand(state)
		return
	}
	open := state.Open
	running := state.Status == workspaceCommandRunning
	if running && state.Scroll.Y >= state.Scroll.MaxY {
		state.Scroll.Y = math.MaxFloat32
	}
	ui.DialogBase(c.Key("workspace-command-dialog"), &open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.34))
		w, h := c.Size()
		panel.Width(max(float32(360), min(float32(820), w*0.78))).MaxHeight(max(float32(320), min(float32(700), h*0.82))).Padding(18).Gap(10).Background(p.surface).Border(1, p.line).Radius(20)
		panel.Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				icon(c, workspaceCommandIcon(state.Command.IconKey), 18, p.blue)
				ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
					label(c, "执行快捷命令", 17, p.text).FontWeight(600)
					workspaceName, _ := s.workspaceNames(state.TaskGroupID, state.Workspace)
					label(c, workspaceName+" · "+state.Command.Name, 11, p.muted).SingleLine().Tooltip(state.Workspace.Path)
				})
				status, color := workspaceCommandStatusLabel(state), workspaceCommandStatusColor(state.Status, p, s.dark)
				label(c, status, 11, color)
			})
			ui.Divider(c)
			label(c, workspaceCommandDisplay(state.Command), 11, p.text).Font("Consolas, monospace").Wrap().Selectable()
			label(c, "工作目录："+filepath.Join(state.Workspace.Path, filepath.FromSlash(firstNonEmpty(state.Command.WorkingDirectory, "."))), 10, p.muted).Wrap().Selectable()

			if state.Scroll.Y >= state.Scroll.MaxY {
				state.Scroll.Y = math.MaxFloat32
			}
			ui.Scroll(c).TrackScroll(&state.Scroll).Grow(1).MinHeight(150).Padding(9).Gap(3).Background(p.soft).Border(1, p.line).Radius(9).Children(func() {
				if len(state.Lines) == 0 {
					label(c, workspaceCommandEmptyLogMessage(running), 11, p.muted).Padding(4)
					return
				}
				for index := range state.Lines {
					row := state.Lines[(state.LineStart+index)%len(state.Lines)]
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Start).Children(func() {
						color := p.muted
						if strings.EqualFold(row.Stream, "STDERR") {
							color = workspaceCommandErrorColor(s.dark)
						}
						lineText := row.Text
						if lineText == "" {
							lineText = " "
						}
						label(c, lineText, 11, color).Grow(1).Font("Consolas, monospace").LineHeight(1.4).Wrap().Selectable()
					})
				}
			})

			if state.Truncated {
				label(c, fmt.Sprintf("日志过长，仅保留最近 %s 行。", formatWorkspaceCommandCount(workspaceCommandMaxLogLines)), 10, p.amber).Wrap()
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				summary := "耗时：执行中"
				if !running {
					summary = "耗时：" + formatWorkspaceCommandDuration(state.Duration)
				}
				if state.HasExitCode {
					summary += fmt.Sprintf(" · 退出码：%d", state.ExitCode)
				}
				label(c, summary, 10, p.muted).Grow(1)
				copyButton := ui.ButtonBase(c.Key("workspace-command-copy")).Height(32).Padding(0, 11).Radius(8).Border(1, p.line).Background(p.surface).Label("复制输出").Disabled(len(state.Lines) == 0)
				copyButton.Children(func() { label(c, "复制输出", 11, p.text) })
				if copyButton.Clicked() && len(state.Lines) > 0 {
					s.copyTextToClipboard(c.Services(), workspaceCommandOutputCopyText(state.Lines, state.LineStart), "命令输出已复制")
				}
				if running {
					stop := ui.ButtonBase(c.Key("workspace-command-stop")).Height(32).Padding(0, 11).Radius(8).Border(1, p.line).Background(p.surface).Label("停止执行").Disabled(state.StopRequested)
					stop.Children(func() {
						label(c, map[bool]string{true: "正在停止…", false: "停止执行"}[state.StopRequested], 11, map[bool]ui.Color{true: p.muted, false: p.text}[state.StopRequested])
					})
					if stop.Clicked() {
						s.requestWorkspaceCommandStop()
					}
				} else if action(c, "workspace-command-close", "close", "关闭", p, true) {
					open = false
				}
			})
			if state.Error != "" {
				label(c, "结果："+state.Error, 10, workspaceCommandErrorColor(s.dark)).Wrap().Selectable()
			}
		})
	})
	state.Open = running || open
	if !state.Open && s.workspaceCommand == state {
		s.workspaceCommand = nil
	}
}

func workspaceCommandStatusLabel(state *workspaceCommandUIState) string {
	if state == nil {
		return ""
	}
	switch state.Status {
	case workspaceCommandRunning:
		if state.StopRequested {
			return "正在停止"
		}
		return "执行中"
	case workspaceCommandSucceeded:
		return "执行成功"
	case workspaceCommandCancelled:
		return "已取消"
	default:
		return "执行失败"
	}
}

func workspaceCommandStatusColor(status workspaceCommandStatus, p palette, dark bool) ui.Color {
	switch status {
	case workspaceCommandSucceeded:
		return p.green
	case workspaceCommandFailed:
		return workspaceCommandErrorColor(dark)
	case workspaceCommandCancelled:
		return p.muted
	default:
		return p.blue
	}
}

func workspaceCommandErrorColor(dark bool) ui.Color {
	if dark {
		return ui.Hex("#F2B8B5")
	}
	return ui.Hex("#B3261E")
}

func workspaceCommandIcon(key string) string {
	switch key {
	case "play", "maven-deploy":
		return "launch"
	case "terminal":
		return "terminal"
	case "package", "maven-install":
		return "file"
	case "upload":
		return "cloud"
	case "refresh", "maven-clean":
		return "refresh"
	case "code":
		return "code"
	case "database":
		return "repo"
	case "settings":
		return "settings"
	case "build", "maven":
		return "tools"
	default:
		return "launch"
	}
}

func workspaceCommandDisplay(command domain.WorkspaceCommandConfig) string {
	parts := append([]string{command.Executable}, command.Arguments...)
	for i, part := range parts {
		if part == "" || strings.ContainsAny(part, " \t\r\n\"'") {
			parts[i] = `"` + strings.ReplaceAll(strings.ReplaceAll(part, `\`, `\\`), `"`, `\"`) + `"`
		}
	}
	return strings.Join(parts, " ")
}

func workspaceCommandOutputCopyText(lines []workspaceCommandOutputLine, start int) string {
	values := make([]string, len(lines))
	for index := range lines {
		line := lines[(start+index)%len(lines)]
		values[index] = line.Text
	}
	return strings.Join(values, "\n")
}

func workspaceCommandEmptyLogMessage(running bool) string {
	if running {
		return "正在等待命令输出…"
	}
	return "命令没有输出。"
}

func formatWorkspaceCommandDuration(duration time.Duration) string {
	if duration < time.Second {
		return fmt.Sprintf("%dms", duration.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", duration.Seconds())
}

func formatWorkspaceCommandCount(count int) string {
	if count >= 1000 {
		return fmt.Sprintf("%d,%03d", count/1000, count%1000)
	}
	return fmt.Sprint(count)
}
