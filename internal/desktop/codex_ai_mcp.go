package desktop

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

type codexAIMCPConnection struct {
	Label   string `json:"label"`
	Success bool   `json:"success"`
	Tools   int    `json:"tools"`
}

type codexAIMCPMutationResult struct {
	Saved        bool             `json:"saved"`
	Snapshot     *codexAISnapshot `json:"snapshot"`
	RefreshError string           `json:"refreshError"`
}

type codexAIMCPForm struct {
	Open              bool
	Editing           bool
	Global            bool
	ShowSecrets       bool
	Saving            bool
	Name              string
	OldName           string
	File              string
	Transport         string
	Endpoint          string
	Args              string
	Environment       string
	HTTPHeaders       string
	BearerTokenEnvVar string
	StartupTimeoutSec string
	ToolTimeoutSec    string
	Original          map[string]any
	Error             string
}

type codexAIMCPDelete struct {
	Open   bool
	Server codexAIMCPServer
}

type codexAINewSessionOutcome struct {
	Dispatch      application.ActionResult
	DispatchError string
	Snapshot      application.Snapshot
	SnapshotError string
}

func codexAIMCPBusy(state *codexAIState) bool {
	return state != nil && (state.mcpCheckLoading || state.mcpMutationLoading || state.mcpAuthLoading)
}

func (s *app) renderCodexAIMCPManagement(c *ui.Context, p palette, state *codexAIState) {
	settingsCard(c, p, "管理 MCP", "维护当前任务目录的 Codex MCP 配置。保存后，新建或重启 Codex 会话生效。", func() {
		ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
			if action(c, "codex-ai-mcp-back", "chevron-left", "返回", p, false) {
				state.mcpManaging = false
				s.cancelCodexAIRequest(state, "mcp-status")
				s.cancelCodexAIRequest(state, "mcp-check")
			}
			ui.Spacer(c)
			if codexAIAction(c, "codex-ai-mcp-refresh", "refresh", "刷新", p, false, state.mcpStatusLoading || codexAIMCPBusy(state)) {
				s.requestCodexAIMCPSnapshot()
			}
			if codexAIAction(c, "codex-ai-mcp-check", "launch", "检测连接", p, false, state.mcpSnapshot == nil || state.mcpStatusLoading || codexAIMCPBusy(state)) {
				s.requestCodexAIMCPCheck()
			}
			if codexAIAction(c, "codex-ai-mcp-add", "plus", "添加 MCP", p, true, state.mcpSnapshot == nil || codexAIMCPBusy(state)) {
				state.mcpEditor = newCodexAIMCPForm(state.mcpSnapshot, state.identity.cwd, nil)
			}
		})
		label(c, "任务目录："+state.identity.cwd, 9, p.muted).Wrap().Selectable()
		if state.mcpStatusLoading || state.mcpCheckLoading || state.mcpMutationLoading || state.mcpAuthLoading {
			ui.Progress(c, -1).FillWidth().Label("正在读取或更新 MCP 状态…")
		}
		if state.mcpError != "" {
			codexAIErrorPanel(c, p, state.mcpError)
		}
		if state.mcpCheckError != "" {
			codexAIErrorPanel(c, p, state.mcpCheckError)
		}
		if state.mcpMessage != "" {
			label(c, state.mcpMessage, 10, p.green).Wrap()
		}
		if state.mcpSnapshot == nil {
			if !state.mcpStatusLoading && state.mcpError == "" {
				settingsMuted(c, p, "等待读取当前任务的 MCP 配置。")
			}
			return
		}
		for _, warning := range state.mcpSnapshot.Warnings {
			label(c, warning, 10, p.amber).Wrap()
		}
		ui.Row(c).Gap(6).Children(func() {
			for _, scope := range []struct{ key, label string }{{"ALL", "全部"}, {"GLOBAL", "全局"}, {"TASK", "当前任务"}, {"PLUGIN", "插件提供"}, {"MANAGED", "受管理"}} {
				if scope.key != "ALL" && !codexAIHasMCPScope(state.mcpSnapshot, scope.key) {
					continue
				}
				selected, key, title := state.mcpScope == scope.key, scope.key, scope.label
				s.settingsChoice(c, p, "codex-ai-mcp-scope-"+key, title, selected, func() { state.mcpScope = key })
			}
		})
		servers := codexAISortedServers(state.mcpSnapshot.Servers, state.mcpScope)
		if len(servers) == 0 {
			settingsMuted(c, p, "当前任务没有匹配的 MCP 服务。添加本地命令或 HTTP 服务后即可使用。")
			return
		}
		for index, server := range servers {
			s.renderCodexAIMCPManagedServer(c, p, state, server, index)
		}
		if state.mcpConnections != nil {
			label(c, "最近连接检测", 11, p.text).FontWeight(600)
			if len(state.mcpConnections) == 0 {
				settingsMuted(c, p, "Codex 没有返回 MCP 连接状态。")
			}
			for name, connection := range state.mcpConnections {
				color := p.green
				if !connection.Success {
					color = p.danger
				}
				status := connection.Label
				if connection.Tools > 0 {
					status += fmt.Sprintf(" · %d 个工具", connection.Tools)
				}
				label(c, name+" · "+status, 10, color).Wrap()
			}
		}
	})
	if state.mcpEditor != nil {
		s.renderCodexAIMCPEditor(c, p, state, state.mcpEditor)
	}
	if state.mcpDelete != nil {
		s.renderCodexAIMCPDeleteDialog(c, p, state, state.mcpDelete)
	}
}

func (s *app) renderCodexAIMCPManagedServer(c *ui.Context, p palette, state *codexAIState, server codexAIMCPServer, index int) {
	name := firstNonEmpty(server.Name, "未命名 MCP")
	enabled := codexAIMCPEnabled(server)
	connection, hasConnection := state.mcpConnections[name]
	activeLabel, activeColor := "已启用", p.green
	if !enabled {
		activeLabel, activeColor = "已停用", p.muted
	} else if !server.Active {
		activeLabel, activeColor = "未生效", p.amber
	}
	key := codexAIMCPServerKey(server)
	ui.Column(c).FillWidth().Padding(9, 10).Gap(5).Background(p.soft).Radius(8).Children(func() {
		ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				label(c, name, 11, p.text).FontWeight(600)
				label(c, codexAIMCPScopeLabel(server.Scope)+" · "+firstNonEmpty(server.Version, "Codex MCP"), 9, p.muted)
			})
			if server.PluginID != "" || codexAIMCPEditable(server) {
				if state.mcpSwitches == nil {
					state.mcpSwitches = make(map[string]*bool)
				}
				switchValue := state.mcpSwitches[key]
				if switchValue == nil {
					value := enabled
					switchValue = &value
					state.mcpSwitches[key] = switchValue
				}
				ui.Switch(c.Key("codex-ai-mcp-enabled-"+key), switchValue).Label("启用").Disabled(codexAIMCPBusy(state))
				if *switchValue != enabled && !codexAIMCPBusy(state) {
					s.setCodexAIMCPEnabled(server, *switchValue)
				}
			} else {
				label(c, activeLabel, 9, activeColor)
			}
		})
		if detail := codexAIMCPEndpoint(server); detail != "" {
			label(c, detail, 10, p.muted).SingleLine().Tooltip(detail).Selectable()
		}
		if server.PluginID != "" {
			label(c, "插件："+server.PluginID, 9, p.muted).SingleLine()
		}
		if server.File != "" {
			label(c, server.File, 9, p.muted).Wrap().Selectable()
		}
		if !enabled {
			label(c, "已停用", 10, p.muted)
		} else if !server.Active {
			label(c, server.Note, 10, p.amber).Wrap()
		} else if server.PluginID == "" {
			status := "连接尚未检测"
			color := p.muted
			if state.mcpConnections != nil {
				status, color = "未就绪：服务未返回状态，请检查配置后重试", p.amber
				if hasConnection {
					status, color = connection.Label, p.green
					if !connection.Success {
						color = p.danger
					}
					if connection.Tools > 0 {
						status += fmt.Sprintf(" · %d 个工具", connection.Tools)
					}
				}
			}
			label(c, status, 9, color).Wrap()
		} else if state.mcpConnections != nil && hasConnection {
			color := p.green
			if !connection.Success {
				color = p.danger
			}
			label(c, connection.Label, 9, color).Wrap()
		}
		ui.Row(c).Gap(7).Disabled(codexAIMCPBusy(state)).Children(func() {
			if codexAIMCPEditable(server) {
				if action(c, fmt.Sprintf("codex-ai-mcp-edit-%d", index), "edit", "编辑", p, false) && !state.mcpMutationLoading {
					state.mcpEditor = newCodexAIMCPForm(state.mcpSnapshot, state.identity.cwd, &server)
				}
				if action(c, fmt.Sprintf("codex-ai-mcp-delete-%d", index), "delete", "删除", p, false) && !state.mcpMutationLoading {
					state.mcpDelete = &codexAIMCPDelete{Open: true, Server: server}
				}
			}
			_, hasHTTPURL := server.Raw["url"].(string)
			if server.PluginID == "" && hasHTTPURL && server.Active && enabled {
				if action(c, fmt.Sprintf("codex-ai-mcp-login-%d", index), "key", "认证", p, false) && !state.mcpAuthLoading && !state.mcpMutationLoading {
					s.authenticateCodexAIMCP(server, true)
				}
				if action(c, fmt.Sprintf("codex-ai-mcp-logout-%d", index), "close", "退出认证", p, false) && !state.mcpAuthLoading && !state.mcpMutationLoading {
					s.authenticateCodexAIMCP(server, false)
				}
			}
		})
	})
}

func codexAIMCPEnabled(server codexAIMCPServer) bool {
	if enabled, ok := server.Raw["enabled"].(bool); ok {
		return enabled
	}
	return true
}

func codexAIMCPEditable(server codexAIMCPServer) bool {
	scope := strings.ToUpper(server.Scope)
	return server.PluginID == "" && (scope == "GLOBAL" || scope == "TASK") && server.File != ""
}

func codexAIMCPServerKey(server codexAIMCPServer) string {
	return codexAIPathKey(server.File) + "\x00" + server.PluginID + "\x00" + server.Name
}

func (s *app) setCodexAIMCPEnabled(server codexAIMCPServer, enabled bool) {
	state := s.codexAI
	if state == nil || !s.codexAIIdentityCurrent(state) || state.mcpSnapshot == nil || codexAIMCPBusy(state) {
		return
	}
	state.mcpMutationLoading = true
	state.mcpError, state.mcpMessage = "", ""
	s.startCodexAIRequest("mcp-mutate", codexAIMCPSetAction(state.identity, server, enabled))
}

func (s *app) authenticateCodexAIMCP(server codexAIMCPServer, login bool) {
	state := s.codexAI
	if state == nil || !s.codexAIIdentityCurrent(state) || codexAIMCPBusy(state) {
		return
	}
	state.mcpAuthLoading = true
	state.mcpError, state.mcpMessage = "", ""
	s.startCodexAIRequest("mcp-auth", codexAIMCPAuthenticateAction(state.identity, server, login))
}

func codexAIMCPSetAction(identity codexAIIdentity, server codexAIMCPServer, enabled bool) application.Action {
	payload := map[string]any{"cwd": identity.cwd, "file": server.File, "name": server.Name, "enabled": enabled}
	if server.PluginID != "" {
		payload["pluginId"] = server.PluginID
	}
	return application.Action{Kind: "codex.extensions/mcp/set", TaskID: identity.taskID, Payload: payload}
}

func codexAIMCPRemoveAction(identity codexAIIdentity, server codexAIMCPServer) application.Action {
	return application.Action{Kind: "codex.extensions/mcp/remove", TaskID: identity.taskID,
		Payload: map[string]any{"cwd": identity.cwd, "file": server.File, "name": server.Name, "confirmed": true}}
}

func codexAIMCPAuthenticateAction(identity codexAIIdentity, server codexAIMCPServer, login bool) application.Action {
	return application.Action{Kind: "codex.ai/mcp/authenticate", TaskID: identity.taskID,
		Payload: map[string]any{"cwd": identity.cwd, "name": server.Name, "login": login}}
}

func newCodexAIMCPForm(snapshot *codexAISnapshot, cwd string, server *codexAIMCPServer) *codexAIMCPForm {
	form := &codexAIMCPForm{Open: true, Transport: "本地命令", StartupTimeoutSec: "10", ToolTimeoutSec: "60"}
	if server != nil {
		form.Editing = true
		form.Name, form.OldName, form.File = server.Name, server.Name, server.File
		form.Global = strings.EqualFold(server.Scope, "GLOBAL")
		form.Original = server.Raw
		form.Endpoint = mapString(server.Raw, "command")
		if rawURL := mapString(server.Raw, "url"); rawURL != "" {
			form.Transport, form.Endpoint = "HTTP 服务", rawURL
		}
		form.Args = strings.Join(stringArray(mustArray(server.Raw["args"])), "\n")
		form.Environment = codexAIMCPMapLines(server.Raw["env"])
		form.HTTPHeaders = codexAIMCPMapLines(server.Raw["http_headers"])
		form.BearerTokenEnvVar = mapString(server.Raw, "bearer_token_env_var")
		form.StartupTimeoutSec = mcpConfigString(server.Raw, "startup_timeout_sec", "10")
		form.ToolTimeoutSec = mcpConfigString(server.Raw, "tool_timeout_sec", "60")
		return form
	}
	if cwd == "" {
		form.Global = true
		if snapshot != nil {
			form.File = snapshot.UserFile
		}
	} else {
		form.Global = false
	}
	return form
}

func codexAIMCPMapLines(value any) string {
	values, _ := value.(map[string]any)
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, key := range keys {
		lines = append(lines, key+"="+strings.TrimSuffix(fmt.Sprint(values[key]), "\r"))
	}
	return strings.Join(lines, "\n")
}

func codexAIMCPPairsJSON(lines, fieldName string) (string, error) {
	values := make(map[string]any)
	for _, rawLine := range strings.Split(lines, "\n") {
		line := strings.TrimSuffix(rawLine, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		separator := strings.IndexByte(line, '=')
		if separator <= 0 {
			return "", fmt.Errorf("%s请按每行 NAME=VALUE 填写", fieldName)
		}
		name := strings.TrimSpace(line[:separator])
		if name == "" {
			return "", fmt.Errorf("%s名称不能为空", fieldName)
		}
		if _, exists := values[name]; exists {
			return "", fmt.Errorf("%s名称重复：%s", fieldName, name)
		}
		values[name] = line[separator+1:]
	}
	encoded, err := json.Marshal(values)
	return string(encoded), err
}

func (s *app) renderCodexAIMCPEditor(c *ui.Context, p palette, state *codexAIState, form *codexAIMCPForm) {
	ui.DialogBase(c.Key("codex-ai-mcp-editor"), &form.Open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.34))
		w, h := c.Size()
		panel.Width(min(680, w-56)).Height(min(760, h-56)).Padding(18).Gap(10).Background(p.surface).Border(1, p.line).Radius(14)
		panel.Children(func() {
			label(c, map[bool]string{true: "编辑 MCP", false: "添加 MCP"}[form.Editing], 16, p.text).FontWeight(600)
			label(c, "配置只作用于当前任务目录或所选全局配置，保存不会启动 MCP 工具。", 10, p.muted).Wrap()
			ui.Scroll(c).Grow(1).MinHeight(0).Gap(10).Padding(2).Disabled(form.Saving || codexAIMCPBusy(state)).Children(func() {
				if form.Error != "" {
					codexAIErrorPanel(c, p, form.Error)
				}
				if form.Editing {
					label(c, "配置来源："+codexAIMCPScopeLabel(map[bool]string{true: "GLOBAL", false: "TASK"}[form.Global])+" · "+form.File, 9, p.muted).Wrap().Selectable()
				} else if state.identity.cwd != "" {
					ui.Row(c).Gap(6).Children(func() {
						s.settingsChoice(c, p, "codex-ai-mcp-form-global", "全局配置", form.Global, func() { form.Global = true })
						s.settingsChoice(c, p, "codex-ai-mcp-form-task", "当前任务", !form.Global, func() { form.Global = false })
					})
				}
				codexAIMCPInput(c, p, "name", "名称", &form.Name, false)
				ui.Column(c).Gap(5).Children(func() {
					label(c, "传输方式", 10, p.muted)
					ui.Row(c).Gap(6).Children(func() {
						s.settingsChoice(c, p, "codex-ai-mcp-form-local", "本地命令", form.Transport == "本地命令", func() {
							form.Transport, form.Endpoint = "本地命令", ""
						})
						s.settingsChoice(c, p, "codex-ai-mcp-form-http", "HTTP 服务", form.Transport == "HTTP 服务", func() {
							form.Transport, form.Endpoint = "HTTP 服务", ""
						})
					})
				})
				codexAIMCPInput(c, p, "endpoint", map[bool]string{true: "HTTP/HTTPS 服务地址", false: "启动程序"}[form.Transport == "HTTP 服务"], &form.Endpoint, false)
				if form.Transport == "HTTP 服务" {
					codexAIMCPTextArea(c, p, "bearer-token", "Bearer Token 环境变量名（可选）", &form.BearerTokenEnvVar, false, 44)
					codexAIMCPTextArea(c, p, "http-headers", "请求头 · 每行 NAME=VALUE（可选）", &form.HTTPHeaders, !form.ShowSecrets, 92)
				} else {
					codexAIMCPTextArea(c, p, "args", "命令参数 · 每行一个", &form.Args, false, 80)
					codexAIMCPTextArea(c, p, "environment", "环境变量 · 每行 NAME=VALUE", &form.Environment, !form.ShowSecrets, 96)
				}
				if form.Transport == "HTTP 服务" || form.Transport == "本地命令" {
					showText := "显示敏感值"
					if form.ShowSecrets {
						showText = "隐藏敏感值"
					}
					if action(c, "codex-ai-mcp-show-secrets", "eye", showText, p, false) {
						form.ShowSecrets = !form.ShowSecrets
					}
				}
				ui.Row(c).Gap(8).Children(func() {
					codexAIMCPInput(c, p, "startup-timeout", "启动超时（秒）", &form.StartupTimeoutSec, false)
					codexAIMCPInput(c, p, "tool-timeout", "工具超时（秒）", &form.ToolTimeoutSec, false)
				})
			})
			s.renderCodexAIOperation(c.Key("codex-ai-editor-operation"), p, state, "mcp-mutate")
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if action(c, "codex-ai-mcp-cancel", "close", "取消", p, false) && !form.Saving {
					state.mcpEditor = nil
				}
				if codexAIAction(c, "codex-ai-mcp-save", "check", map[bool]string{true: "保存中…", false: "保存"}[form.Saving], p, true, form.Saving || codexAIMCPBusy(state)) {
					s.saveCodexAIMCPForm(form)
				}
			})
		})
	})
	if !form.Open && !form.Saving {
		state.mcpEditor = nil
	}
}

func codexAIMCPInput(c *ui.Context, p palette, key, title string, value *string, secret bool) {
	ui.Column(c).Gap(4).Children(func() {
		label(c, title, 10, p.muted)
		input := ui.TextInput(c.Key("codex-ai-mcp-input-"+key), value).FillWidth().Height(38).Placeholder(title).Label(title + "输入")
		if secret {
			input.Password()
		}
	})
}

func codexAIMCPTextArea(c *ui.Context, p palette, key, title string, value *string, secret bool, height float32) {
	ui.Column(c).Gap(4).Children(func() {
		label(c, title, 10, p.muted)
		area := ui.TextArea(c.Key("codex-ai-mcp-area-"+key), value).FillWidth().Height(height).Padding(9).Background(p.surface).Border(1, p.line).Radius(7).Label(title + "输入")
		if secret {
			area.Password()
		}
	})
}

func (s *app) saveCodexAIMCPForm(form *codexAIMCPForm) {
	state := s.codexAI
	if form == nil || state == nil || state.mcpSnapshot == nil || form.Saving || codexAIMCPBusy(state) || !s.codexAIIdentityCurrent(state) {
		return
	}
	action, err := codexAIMCPFormAction(state.identity, state.mcpSnapshot, form)
	if err != nil {
		form.Error = err.Error()
		return
	}
	form.Saving = true
	state.mcpMutationLoading = true
	state.mcpError, state.mcpMessage = "", ""
	s.startCodexAIRequest("mcp-mutate", action)
}

func codexAIMCPFormAction(identity codexAIIdentity, snapshot *codexAISnapshot, form *codexAIMCPForm) (application.Action, error) {
	if form == nil || snapshot == nil {
		return application.Action{}, fmt.Errorf("MCP 配置尚未加载")
	}
	if !validMCPServerName(strings.TrimSpace(form.Name)) {
		return application.Action{}, fmt.Errorf("MCP 名称只能包含英文、数字、点、下划线和短横线，长度为 1 到 100 个字符")
	}
	if form.Editing {
		if form.File == "" || form.OldName == "" {
			return application.Action{}, fmt.Errorf("编辑 MCP 缺少配置来源或原服务名称")
		}
		found := false
		for _, server := range snapshot.Servers {
			if server.Name == form.OldName && codexAIPathKey(server.File) == codexAIPathKey(form.File) && codexAIMCPEditable(server) {
				found = true
				break
			}
		}
		if !found {
			return application.Action{}, fmt.Errorf("MCP 编辑目标已不属于当前任务配置，请刷新后重试")
		}
	}
	payloadForConfig := map[string]any{
		"name": form.Name, "transport": form.Transport, "endpoint": form.Endpoint,
		"args": form.Args, "env": "{}", "httpHeaders": "{}",
		"bearerTokenEnvVar": form.BearerTokenEnvVar,
		"startupTimeoutSec": form.StartupTimeoutSec, "toolTimeoutSec": form.ToolTimeoutSec,
	}
	if form.Transport == "本地命令" {
		environment, err := codexMCPFormPairs(form.Environment, form.Original["env"], "环境变量")
		if err != nil {
			return application.Action{}, err
		}
		payloadForConfig["env"] = environment
	} else {
		headers, err := codexMCPFormPairs(form.HTTPHeaders, form.Original["http_headers"], "请求头")
		if err != nil {
			return application.Action{}, err
		}
		payloadForConfig["httpHeaders"] = headers
	}
	config, err := mcpConfigFromForm(payloadForConfig, form.Original)
	if err != nil {
		return application.Action{}, err
	}
	file := form.File
	if !form.Editing {
		if form.Global {
			file = snapshot.UserFile
		} else {
			file = filepath.Join(identity.cwd, ".codex", "config.toml")
		}
	}
	if file == "" {
		return application.Action{}, fmt.Errorf("无法确定 MCP 配置文件路径")
	}
	kind := "codex.extensions/mcp/add"
	payload := map[string]any{"cwd": identity.cwd, "file": file, "name": strings.TrimSpace(form.Name), "config": config}
	if form.Editing {
		kind = "codex.extensions/mcp/edit"
		payload["oldName"] = form.OldName
	}
	return application.Action{Kind: kind, TaskID: identity.taskID, Payload: payload}, nil
}

func codexAIMCPFormOwnsIdentity(state *codexAIState, form *codexAIMCPForm) bool {
	if state == nil || state.mcpSnapshot == nil || form == nil || form.File == "" || form.OldName == "" {
		return false
	}
	for _, server := range state.mcpSnapshot.Servers {
		if server.Name == form.OldName && codexAIPathKey(server.File) == codexAIPathKey(form.File) && codexAIMCPEditable(server) {
			return true
		}
	}
	return false
}

func (s *app) renderCodexAIMCPDeleteDialog(c *ui.Context, p palette, state *codexAIState, deletion *codexAIMCPDelete) {
	ui.DialogBase(c.Key("codex-ai-mcp-delete"), &deletion.Open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.34))
		w, _ := c.Size()
		panel.Width(min(480, w-48)).Padding(18).Gap(12).Background(p.surface).Border(1, p.line).Radius(14)
		panel.Children(func() {
			label(c, "删除 MCP："+deletion.Server.Name, 15, p.text).FontWeight(600)
			label(c, "将从 "+codexAIMCPScopeLabel(deletion.Server.Scope)+" 配置中移除此服务。此更改会写入 Codex 配置文件。", 11, p.muted).Wrap()
			label(c, deletion.Server.File, 9, p.muted).Wrap().Selectable()
			s.renderCodexAIOperation(c.Key("codex-ai-delete-operation"), p, state, "mcp-mutate")
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if action(c, "codex-ai-mcp-delete-cancel", "close", "取消", p, false) && !state.mcpMutationLoading {
					state.mcpDelete = nil
				}
				if codexAIAction(c, "codex-ai-mcp-delete-confirm", "delete", "删除", p, true, codexAIMCPBusy(state)) {
					s.removeCodexAIMCP(deletion.Server)
				}
			})
		})
	})
	if !deletion.Open && !state.mcpMutationLoading {
		state.mcpDelete = nil
	}
}

func (s *app) removeCodexAIMCP(server codexAIMCPServer) {
	state := s.codexAI
	if state == nil || codexAIMCPBusy(state) || !s.codexAIIdentityCurrent(state) || !codexAIMCPEditable(server) {
		return
	}
	state.mcpMutationLoading = true
	state.mcpError, state.mcpMessage = "", ""
	s.startCodexAIRequest("mcp-mutate", codexAIMCPRemoveAction(state.identity, server))
}

func firstNonEmptyCodexAI(values ...any) string {
	for _, value := range values {
		switch typed := value.(type) {
		case string:
			if strings.TrimSpace(typed) != "" {
				return strings.TrimSpace(typed)
			}
		case *string:
			if typed != nil && strings.TrimSpace(*typed) != "" {
				return strings.TrimSpace(*typed)
			}
		}
	}
	return ""
}

func codexAIPayloadBool(payload map[string]any, key string) bool {
	value, _ := payload[key].(bool)
	return value
}
