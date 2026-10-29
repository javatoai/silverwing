package desktop

import (
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (s *app) renderCodexAI(c *ui.Context, p palette) {
	state := s.toolsUnit.state.codexAI
	if state == nil {
		return
	}
	if !state.Open {
		s.closeCodexAI()
		return
	}
	if !s.codexAIIdentityCurrent(state) {
		s.closeCodexAI()
		return
	}
	ui.DialogBase(c.Key("codex-ai-dialog"), &state.Open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.34))
		w, h := c.Size()
		panel.Width(min(1040, w-48)).Height(min(760, h-48)).Padding(18).Gap(10).Background(p.surface).Border(1, p.line).Radius(16)
		panel.Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				icon(c, "skills", 19, p.blue)
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					label(c, "任务 Codex 配置", 17, p.text).FontWeight(600)
					label(c, state.identity.cwd, 10, p.muted).SingleLine().Tooltip(state.identity.cwd)
				})
				if codexAIAction(c, "codex-ai-refresh", "refresh", "刷新", p, false,
					state.snapshotLoading || state.tab == codexAITabSessions && state.threadsLoading || state.bindLoading || codexAIMCPBusy(state)) {
					s.requestCodexAISnapshot()
					if state.tab == codexAITabSessions {
						s.requestCodexAIThreads()
					}
				}
				if action(c, "codex-ai-close", "close", "关闭", p, false) {
					s.closeCodexAI()
				}
			})
			ui.Row(c).Gap(7).Children(func() {
				for _, tab := range []string{codexAITabMCP, codexAITabSkills, codexAITabGuides, codexAITabSessions} {
					selected := state.tab == tab
					s.settingsChoice(c, p, "codex-ai-tab-"+tab, tab, selected, func() {
						if state.tab == codexAITabMCP && tab != codexAITabMCP {
							state.mcpManaging = false
							s.cancelCodexAIRequest(state, "mcp-status")
							s.cancelCodexAIRequest(state, "mcp-check")
						}
						state.tab = tab
						state.filePath, state.fileContent, state.fileError = "", "", ""
						state.resetFilePreview()
						state.fileLoading = false
						s.cancelCodexAIRequest(state, "file-read")
						if tab == codexAITabSessions && !state.threadsLoaded && !state.threadsLoading {
							s.requestCodexAIThreads()
						}
					})
				}
			})
			if state.snapshotLoading {
				label(c, "正在读取当前目录的 Codex 配置…", 11, p.muted)
			}
			s.renderCodexAIOperations(c, p, state)
			if state.snapshotError != "" {
				codexAIErrorPanel(c, p, state.snapshotError)
			}
			if state.externalError != "" {
				codexAIErrorPanel(c, p, state.externalError)
			}
			if state.externalMessage != "" {
				label(c, state.externalMessage, 10, p.green)
			}
			if state.filePath != "" {
				s.renderCodexAIFile(c, p, state)
			} else {
				ui.Scroll(c).Grow(1).MinHeight(0).Padding(2).Gap(10).Children(func() {
					switch state.tab {
					case codexAITabSkills:
						s.renderCodexAISkills(c, p, state)
					case codexAITabGuides:
						s.renderCodexAIGuides(c, p, state)
					case codexAITabSessions:
						s.renderCodexAISessions(c, p, state)
					default:
						s.renderCodexAIMCP(c, p, state)
					}
				})
			}
		})
	})
	if !state.Open {
		s.closeCodexAI()
	}
}

func (s *app) renderCodexAIMCP(c *ui.Context, p palette, state *codexAIState) {
	if state.mcpManaging {
		s.renderCodexAIMCPManagement(c, p, state)
		return
	}
	snapshot := state.mcpSnapshot
	if snapshot == nil {
		snapshot = state.snapshot
	}
	settingsCard(c, p, "MCP 服务", "按任务目录读取 Codex 配置；会话内临时覆盖不在这里显示。", func() {
		label(c, "当前任务没有配置 MCP？可以先查看清单，或进入管理页面添加本地命令和 HTTP 服务。", 10, p.muted).Wrap()
		if action(c, "codex-ai-mcp-manage", "settings", "管理 MCP", p, true) {
			state.mcpManaging = true
			state.mcpScope = "ALL"
			state.mcpMessage, state.mcpError, state.mcpCheckError = "", "", ""
			s.requestCodexAIMCPSnapshot()
		}
		if state.mcpStatusLoading {
			label(c, "正在读取当前任务的 MCP 配置…", 10, p.muted)
		}
		if state.mcpError != "" {
			codexAIErrorPanel(c, p, state.mcpError)
		}
		if snapshot == nil {
			if !state.snapshotLoading && !state.mcpStatusLoading && state.snapshotError == "" && state.mcpError == "" {
				settingsMuted(c, p, "等待读取 Codex 配置。")
			}
			return
		}
		servers := codexAISortedServers(snapshot.Servers, "ALL")
		if len(servers) == 0 {
			settingsMuted(c, p, "当前任务没有配置 MCP。")
		}
		for _, server := range servers {
			ui.Column(c).FillWidth().Gap(3).Padding(7, 0).Children(func() {
				label(c, server.Name, 11, p.text).FontWeight(600)
				label(c, codexAIMCPScopeLabel(server.Scope)+" · "+codexAIEnabledLabel(codexAIMCPEnabled(server))+server.Note, 9, p.muted).Wrap()
			})
		}
	})
}

func (s *app) renderCodexAIMCPServer(c *ui.Context, p palette, state *codexAIState, server codexAIMCPServer, index int) {
	name := strings.TrimSpace(server.Name)
	if name == "" {
		name = "未命名 MCP"
	}
	enabled := true
	if value, ok := server.Raw["enabled"].(bool); ok {
		enabled = value
	}
	activeLabel, activeColor := "已启用", p.green
	if !enabled {
		activeLabel, activeColor = "已停用", p.muted
	} else if !server.Active {
		activeLabel, activeColor = "未生效", p.amber
	}
	detail := codexAIMCPEndpoint(server)
	ui.Column(c).FillWidth().Padding(10, 11).Gap(5).Background(p.soft).Radius(8).Children(func() {
		ui.Row(c).Gap(7).Children(func() {
			label(c, name, 12, p.text).FontWeight(600)
			label(c, codexAIMCPScopeLabel(server.Scope), 9, p.muted)
			ui.Spacer(c)
			label(c, activeLabel, 9, activeColor)
		})
		if detail != "" {
			label(c, detail, 10, p.muted).SingleLine().Tooltip(detail).Selectable()
		}
		if server.PluginID != "" {
			label(c, "插件："+server.PluginID, 9, p.muted).SingleLine()
		}
		if server.File != "" {
			label(c, server.File, 9, p.muted).Wrap().Selectable()
		}
		if server.Note != "" {
			label(c, server.Note, 10, p.amber).Wrap()
		}
		if server.File != "" && codexAIFileAllowed(state.snapshot, server.File) {
			if action(c, fmt.Sprintf("codex-ai-mcp-open-%d", index), "launch", "外部编辑", p, false) {
				s.openCodexAIFileExternally(server.File)
			}
		}
	})
}

func codexAISortedServers(servers []codexAIMCPServer, scope string) []codexAIMCPServer {
	result := make([]codexAIMCPServer, 0, len(servers))
	for _, server := range servers {
		if scope == "ALL" || strings.EqualFold(server.Scope, scope) {
			result = append(result, server)
		}
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Scope != result[j].Scope {
			return codexAIMCPScopeOrder(result[i].Scope) < codexAIMCPScopeOrder(result[j].Scope)
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return result
}

func codexAIMCPScopeOrder(scope string) int {
	switch strings.ToUpper(scope) {
	case "GLOBAL":
		return 0
	case "TASK":
		return 1
	case "PLUGIN":
		return 2
	case "MANAGED":
		return 3
	default:
		return 4
	}
}

func codexAIHasMCPScope(snapshot *codexAISnapshot, scope string) bool {
	if snapshot == nil {
		return false
	}
	for _, server := range snapshot.Servers {
		if strings.EqualFold(server.Scope, scope) {
			return true
		}
	}
	return false
}

func codexAIMCPScopeLabel(scope string) string {
	switch strings.ToUpper(scope) {
	case "GLOBAL":
		return "全局"
	case "TASK":
		return "当前任务"
	case "PLUGIN":
		return "插件提供"
	case "MANAGED":
		return "受管理"
	default:
		return scope
	}
}

func codexAILayerTypeLabel(layerType string) string {
	switch strings.ToLower(layerType) {
	case "user":
		return "用户配置"
	case "project":
		return "项目配置"
	case "managed":
		return "受管理配置"
	default:
		return firstNonEmpty(layerType, "Codex 配置")
	}
}

func codexAIMCPEndpoint(server codexAIMCPServer) string {
	if rawURL, _ := server.Raw["url"].(string); strings.TrimSpace(rawURL) != "" {
		parsed, err := url.Parse(strings.TrimSpace(rawURL))
		if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
			return "HTTP 服务"
		}
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		return parsed.String()
	}
	if command, _ := server.Raw["command"].(string); strings.TrimSpace(command) != "" {
		return strings.TrimSpace(command)
	}
	if server.PluginID != "" {
		return "由插件启动"
	}
	return ""
}

func (s *app) renderCodexAISkills(c *ui.Context, p palette, state *codexAIState) {
	settingsCard(c, p, "当前目录 Skills", "读取 Codex 针对当前任务目录发现的 Skill；外部编辑会交给系统默认应用。", func() {
		if state.snapshot == nil {
			settingsMuted(c, p, "等待读取当前目录的 Skill 清单。")
			return
		}
		for _, warning := range state.snapshot.Warnings {
			label(c, warning, 10, p.amber).Wrap()
		}
		if len(state.snapshot.Skills) == 0 {
			settingsMuted(c, p, "该任务目录没有可用 Skill。可在 Skills 页面查看或安装。")
			return
		}
		for index, skill := range state.snapshot.Skills {
			s.renderCodexAIFileRow(c, p, state, skill.Name, codexAISkillScopeLabel(skill.Scope)+" · "+codexAIEnabledLabel(skill.Enabled), skill.Description, skill.Path, true, index)
		}
	})
}

func codexAISkillScopeLabel(scope string) string {
	switch strings.ToLower(scope) {
	case "user":
		return "用户"
	case "repo", "project":
		return "当前项目"
	case "system":
		return "系统"
	case "admin":
		return "受管理"
	default:
		return firstNonEmpty(scope, "未知来源")
	}
}

func codexAIEnabledLabel(enabled bool) string {
	if enabled {
		return "已启用"
	}
	return "已停用"
}

func (s *app) renderCodexAIGuides(c *ui.Context, p palette, state *codexAIState) {
	settingsCard(c, p, "有效说明文件", "按当前任务目录与 Codex 用户配置识别出的文件。这里只读取，不会改写文件内容。", func() {
		if state.snapshot == nil {
			settingsMuted(c, p, "等待读取当前目录的说明文件。")
			return
		}
		if len(state.snapshot.Files) == 0 {
			settingsMuted(c, p, "当前目录没有发现有效说明文件。")
			return
		}
		for index, file := range state.snapshot.Files {
			description := file.Source
			if !file.Exists {
				description += " · 文件缺失"
			}
			s.renderCodexAIFileRow(c, p, state, file.Title, description, "", file.File, file.Exists, index)
		}
	})
}

func (s *app) renderCodexAIFileRow(c *ui.Context, p palette, state *codexAIState, title, subtitle, detail, path string, exists bool, index int) {
	ui.Column(c).FillWidth().Gap(5).Padding(9, 10).Background(p.soft).Radius(8).Children(func() {
		ui.Row(c).Gap(7).Children(func() {
			label(c, firstNonEmpty(title, "Codex 文件"), 12, p.text).FontWeight(600).Grow(1)
			if !exists {
				label(c, "文件缺失", 9, p.amber)
			}
		})
		if subtitle != "" {
			label(c, subtitle, 10, p.muted).Wrap()
		}
		if detail != "" {
			label(c, detail, 10, p.muted).Wrap()
		}
		if path != "" {
			label(c, path, 9, p.muted).Wrap().Selectable()
		}
		ui.Row(c).Gap(7).Children(func() {
			if exists && action(c, fmt.Sprintf("codex-ai-file-read-%s-%d", state.tab, index), "file", "阅读", p, false) {
				s.readCodexAIFile(path)
			}
			if exists && action(c, fmt.Sprintf("codex-ai-file-open-%s-%d", state.tab, index), "launch", "外部编辑", p, false) {
				s.openCodexAIFileExternally(path)
			}
		})
	})
}

func (s *app) renderCodexAIFile(c *ui.Context, p palette, state *codexAIState) {
	ui.Column(c).Grow(1).MinHeight(0).Gap(8).Children(func() {
		ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
			if action(c, "codex-ai-file-back", "chevron-left", "返回", p, false) {
				s.cancelCodexAIRequest(state, "file-read")
				state.filePath, state.fileContent, state.fileError = "", "", ""
				state.resetFilePreview()
				state.fileLoading = false
			}
			ui.Column(c).Grow(1).Gap(2).Children(func() {
				label(c, filepathBaseCodexAI(state.filePath), 12, p.text).FontWeight(600)
				label(c, state.filePath, 9, p.muted).SingleLine().Tooltip(state.filePath)
			})
			if markdown := state.markdownFilePreview(); markdown != nil {
				s.renderReadonlyMarkdownReaderActions(c, p, markdown, nil)
			}
			if iconButton(c, "codex-ai-file-copy-source", "copy", "复制源码（保留原始换行）", p).Clicked() {
				s.copyTextToClipboard(c.Services(), state.fileContent, "")
			}
			if action(c, "codex-ai-file-read-again", "refresh", "重新读取", p, false) {
				s.readCodexAIFile(state.filePath)
			}
			if action(c, "codex-ai-file-open-external", "launch", "外部编辑", p, false) {
				s.openCodexAIFileExternally(state.filePath)
			}
		})
		if state.filePath == "" {
			return
		}
		if state.fileLoading {
			label(c, "正在读取文件…", 11, p.muted)
		}
		if state.fileError != "" {
			codexAIErrorPanel(c, p, state.fileError)
		}
		s.renderCodexAIFileContent(c, p, state)
	})
}

func filepathBaseCodexAI(path string) string {
	path = strings.TrimRight(path, `/\\`)
	if path == "" {
		return "Codex 文件"
	}
	index := strings.LastIndexAny(path, `/\\`)
	if index >= 0 && index+1 < len(path) {
		return path[index+1:]
	}
	return path
}

func (s *app) renderCodexAISessions(c *ui.Context, p palette, state *codexAIState) {
	settingsCard(c, p, "任务目录会话", "只列出 Codex 返回且工作目录与当前任务一致的会话。关联不会创建新会话。", func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			label(c, "当前关联", 10, p.muted)
			if state.boundThreadID == "" {
				label(c, "未关联", 10, p.muted)
			} else {
				label(c, state.boundThreadID, 10, p.blue).SingleLine().Tooltip(state.boundThreadID).Selectable()
			}
			ui.Spacer(c)
			if codexAIAction(c, "codex-ai-threads-refresh", "refresh", "刷新会话", p, false, state.threadsLoading || state.bindLoading) {
				s.requestCodexAIThreads()
			}
		})
		label(c, state.identity.cwd, 9, p.muted).SingleLine().Tooltip(state.identity.cwd)
		if state.threadsLoading {
			label(c, "正在读取会话…", 10, p.muted)
		}
		if state.threadsError != "" {
			codexAIErrorPanel(c, p, state.threadsError)
		}
		if state.bindError != "" {
			codexAIErrorPanel(c, p, state.bindError)
		}
		if state.bindMessage != "" {
			label(c, state.bindMessage, 10, p.green)
		}
		if len(state.threads) == 0 && !state.threadsLoading && state.threadsError == "" {
			ui.Column(c).FillWidth().Padding(12).Gap(8).Background(p.soft).Radius(9).Children(func() {
				label(c, "还没有该目录的 Codex 会话。", 11, p.text).FontWeight(600)
				label(c, "可以为当前任务目录新建一个 Codex 会话。", 10, p.muted).Wrap()
				if codexAIAction(c, "codex-ai-new-session", "launch", "在 Codex 中新建会话", p, true, state.newSessionLoading) {
					s.requestCodexAINewSession()
				}
			})
		}
		if state.newSessionLoading {
			ui.Progress(c, -1).FillWidth().Label("正在为当前任务目录新建 Codex 会话…")
		}
		if state.newSessionError != "" {
			codexAIErrorPanel(c, p, state.newSessionError)
		}
		if state.newSessionMessage != "" {
			label(c, state.newSessionMessage, 10, p.green).Wrap()
		}
		for index, thread := range state.threads {
			selected := state.selectedThread == thread.ID
			button := ui.ButtonBase(c.Key(fmt.Sprintf("codex-ai-thread-%d", index))).Row().FillWidth().Padding(9, 10).Gap(9).Radius(8).Label(thread.Title).Disabled(state.bindLoading)
			if selected {
				button.Background(p.selected)
			} else {
				button.Background(p.soft)
			}
			button.Children(func() {
				iconName := "user"
				if selected {
					iconName = "check"
				}
				icon(c, iconName, 16, map[bool]ui.Color{true: p.blue, false: p.muted}[selected])
				ui.Column(c).Grow(1).Gap(3).Children(func() {
					label(c, firstNonEmpty(thread.Title, thread.ID), 11, p.text).Wrap()
					label(c, thread.ID, 9, p.muted).SingleLine().Selectable()
				})
				if thread.ID == state.boundThreadID {
					compactState(c, "已关联", p)
				}
			})
			if button.Clicked() {
				state.selectedThread = thread.ID
				state.bindError, state.bindMessage = "", ""
			}
		}
		ui.Row(c).Gap(8).Children(func() {
			canBind := !state.bindLoading && codexAIThreadSelectionValid(state)
			if codexAIAction(c, "codex-ai-thread-bind", "branch", "关联选中会话", p, true, !canBind) {
				s.bindCodexAISelectedThread()
			}
			if codexAIAction(c, "codex-ai-thread-unbind", "close", "解除关联", p, false, state.bindLoading || state.boundThreadID == "") {
				s.unbindCodexAIThread()
			}
		})
		if state.bindLoading {
			label(c, "正在保存会话关联…", 10, p.muted)
		}
	})
}

func codexAIThreadSelectionValid(state *codexAIState) bool {
	_, ok := codexAISelectedThread(state)
	return ok
}

func codexAIErrorPanel(c *ui.Context, p palette, message string) {
	ui.Box(c).FillWidth().Padding(8, 10).Radius(8).Background(p.amberBg).Children(func() {
		label(c, message, 10, p.amber).Wrap().Selectable()
	})
}

func codexAIAction(c *ui.Context, key, iconName, title string, p palette, primary, disabled bool) bool {
	b := ui.ButtonBase(c.Key(key)).Row().Gap(7).Height(34).Padding(0, 16).Radius(20).
		AlignItems(ui.Center).Label(title).Disabled(disabled)
	background, foreground := p.surface, p.text
	if primary {
		background, foreground = ui.Hex("#356AE6"), ui.Hex("#FFFFFF")
	} else {
		b.Border(1, p.line)
	}
	if disabled {
		foreground = p.muted
	} else if b.Hovered() {
		if primary {
			background = ui.Hex("#2454C6")
		} else {
			background = p.soft
		}
	}
	b.Background(background).Children(func() {
		icon(c, iconName, 16, foreground)
		label(c, title, 11, foreground).NoWrap()
	})
	return b.Clicked()
}
