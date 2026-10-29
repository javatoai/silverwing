package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"path/filepath"
	"silverwing-go/internal/application"
	"strings"
)

func (s *app) settingsBasic(c *ui.Context, p palette) {
	settingsAppearanceCard(c, p, func() {
		s.settingsAppearanceLocalSwitchRow(c, p, "workspace-navigation-collapsed", "折叠工作空间导航", "默认开启，仅保留最左侧导航图标，为右侧内容腾出更多空间。更改立即生效并自动记住。", &s.navigationCollapsed)
		settingsAppearanceDivider(c)
		s.settingsAppearanceThemeRow(c, p)
	})
	settingsHelpCard(c, p, "侧边栏展示", "任务和设置始终显示。隐藏入口不会关闭对应功能。", func() {
		for index, item := range []struct{ key, title string }{
			{"requirements", "需求"}, {"tools", "实用工具"},
		} {
			if index > 0 {
				settingsAppearanceDivider(c)
			}
			s.settingsAppearanceNestedSwitchRow(c, p, "sidebarVisibility", item.key, "sidebar-visibility-"+item.key, item.title, "", true)
		}
	})
}

func (s *app) settingsToolVisibility(c *ui.Context, p palette) {
	settingsHelpCard(c, p, "实用工具", "选择显示在主导航中的工具。", func() {
		for _, item := range []struct{ key, title, description string }{
			{"json", "JSON", "格式化、压缩与转义 JSON。"},
			{"dateTime", "日期时间", "时间戳与日期转换。"},
			{"textDiff", "文本对比", "查看两段文本的差异。"},
			{"killPort", "杀端口", "查找并结束占用端口的进程。"},
		} {
			if item.key != "json" {
				settingsAppearanceDivider(c)
			}
			s.settingsAppearanceNestedSwitchRow(c, p, "toolVisibility", item.key, "tool-visibility-"+item.key, item.title, item.description, true)
		}
	})
}

func (s *app) settingsPaths(c *ui.Context, p palette) {
	settingsHelpCard(c, p, "资料与 Skill 的 Git", "仅用于任务资料同步与 Skill 来源。", func() {
		s.settingsConfigPathRow(c, p, "gitExecutablePath", "Git 可执行文件", "留空时从本机环境查找", false)
	})
	settingsHelpCard(c, p, "任务路径设置", "更改目录时选择是否迁移旧任务数据。", func() {
		path := s.input("setting:taskRootTarget", s.configString("taskRoot"))
		settingsAppearanceRow(c, p, "任务根目录", "选择迁移旧任务，或保留旧目录并直接切换。", func(stacked bool) {
			s.taskRootMigrationPathControl(c, p, path, stacked)
		})
	})
	settingsHelpCard(c, p, "任务资料目录设置", "根目录与子目录均配置后才启用任务资料目录。", func() {
		root := s.input("setting:requirementMaterialsRoot", s.configString("requirementMaterialsRoot"))
		s.settingsAppearancePathRow(c, p, "任务资料根目录", "根目录和子目录都配置后，才会启用任务资料目录。", "选择任务资料目录", root, true, func(value string) {
			s.saveMaterialsRoot(value)
		})
		settingsAppearanceDivider(c)
		s.settingsAppearanceTextRow(c, p, "requirementMaterialsSubdirectory", "任务资料子目录", "相对根目录的子目录，例如：测试。", "例如：测试", func(value string) { s.saveConfigField("requirementMaterialsSubdirectory", nullableString(value)) })
		settingsAppearanceDivider(c)
		s.settingsMaterialsGitSync(c, p)
		settingsAppearanceDivider(c)
		s.settingsMaterialsExpansionDepth(c, p)
	})
}

func (s *app) settingsConfigBackup(c *ui.Context, p palette) {
	settingsHelpCard(c, p, "配置与备份", "查看当前生效配置，导入前会先预览变更。\n\n更改或导入配置时会自动保留备份。", func() {
		ui.Row(c).Gap(8).Wrap().Children(func() {
			if action(c, "settings-config-view", "file", "查看配置", p, false) {
				s.openSettingsConfig()
			}
			if action(c, "settings-config-export", "save", "导出配置", p, false) {
				s.exportConfig()
			}
			if action(c, "settings-config-import", "", "导入配置", p, false) {
				s.importConfig()
			}
			s.settingsBackupReloadButton(c, p)
		})
		if s.snapshot.Home != "" {
			settingsPathValue(c, p, "备份目录", filepath.Join(s.snapshot.Home, "backups"))
		}
		state := s.ensureSettingsConfigRecovery()
		if state.previewLoading {
			settingsMuted(c, p, "正在读取导入预览…")
		} else if state.previewError != "" {
			label(c, state.previewError, 12, p.amber).Wrap()
		}
		if state.backupDeleteError != "" {
			label(c, state.backupDeleteError, 11, p.danger).Wrap()
		}
		if state.backupsLoading {
			settingsMuted(c, p, "正在读取配置备份…")
		} else if state.backupsError != "" {
			label(c, state.backupsError, 12, p.amber).Wrap()
		} else if len(s.settingsUnit.state.settingsBackups) == 0 {
			settingsMuted(c, p, "暂无配置备份。")
		} else {
			s.renderSettingsBackupList(c, p)
		}
	})
}

func groupPosition(groups []application.Group, id string) int {
	for index, group := range groups {
		if group.ID == id {
			return index
		}
	}
	return -1
}

func (s *app) moveConfigGroup(groupID string, delta int) {
	config, err := s.settingsConfigMap()
	if err != nil {
		s.lastError = err.Error()
		return
	}
	groups, _ := config["groups"].([]any)
	position := -1
	for index, raw := range groups {
		if mapString(mapData(raw), "id") == groupID {
			position = index
			break
		}
	}
	next := position + delta
	if position < 0 || next < 0 || next >= len(groups) {
		return
	}
	groups[position], groups[next] = groups[next], groups[position]
	config["groups"] = groups
	s.saveSettingsConfigMap(config)
}

func (s *app) settingsTaskCreation(c *ui.Context, p palette) {
	s.settingsWorkspaceTools(c, p)
	status := s.settingsUnit.state.settingsCLIStatus["codex"]
	command := firstNonEmpty(mapString(status, "command"), mapString(status, "commandPath"))
	settingsHelpCardWithActions(c, p, "Codex CLI 状态", "用于任务命名与 Codex 扩展管理。", func() {
		s.overflowMenu(c, p, "settings-codex-cli-more", "更多 Codex CLI 操作", false, func(menu *overflowMenuBuilder) {
			if menu.Item("check-status", "检查状态").Icon("refresh").Chosen() {
				s.act("cli.status", "", "", "", "", "", map[string]any{"tool": "codex"})
			}
			disabled := s.busy || strings.TrimSpace(command) == ""
			if menu.Item("copy-command", "复制命令路径").Icon("copy").Disabled(disabled).Chosen() {
				s.copyTextToClipboard(c.Services(), command, "命令路径已复制")
			}
			if menu.Item("run-terminal", "在终端中运行").Icon("terminal").Disabled(disabled).Chosen() {
				s.act("system.cli-terminal", "", "", "", command, "", map[string]any{"tool": "codex"})
			}
		})
	}, func() {
		s.settingsCLIStatusDetails(c, p, "codex")
	})
	settingsHelpCard(c, p, "Codex CLI 命令路径", "留空时自动探测，也可以手动选择；更改后自动保存。", func() {
		s.settingsConfigPathRow(c, p, "codexExecutablePath", "Codex CLI 可执行文件", "留空时使用 PATH 自动探测", false)
		if s.toolsUnit.state.cliJobs == nil || s.toolsUnit.state.cliJobs.requests["status:codex"] == nil {
			s.settingsCLIPathDiscoveryHint(c, p, "codex")
		}
		s.settingsCLICommandActions(c, p, "codex", command)
	})
	settingsHelpCard(c, p, "AI 命名", "仅将需求标题与正文片段交给配置的模型；保留本地编辑能力。", func() {
		s.settingsSwitch(c, p, "aiRequirementNamingEnabled", "开启 AI 生成任务名称和文件夹名", "创建任务时自动补全尚未手动修改的名称。", false)
		s.settingsSwitch(c, p, "aiRequirementNamingPrewarmEnabled", "拉取需求列表后自动预热命名", "提前准备本次读取到的需求，不受搜索和筛选影响。", false)
		s.settingsTextRow(c, p, "aiRequirementNamingModel", "AI 命名模型", "例如 gpt-6-luna", false, func(value string) {
			if strings.ContainsAny(strings.TrimSpace(value), " \t\r\n") || strings.TrimSpace(value) == "" {
				s.lastError = "模型名称不能为空且不能包含空白字符。"
				return
			}
			s.saveConfigField("aiRequirementNamingModel", strings.TrimSpace(value))
		})
	})
}

func (s *app) settingsAgents(c *ui.Context, p palette) {
	state := s.ensureTaskNotesUI()
	if state.settingsReadDeferred && !s.busy {
		s.requestSettingsInstructionRead(state.settingsReadAcceptDisk)
	}
	var markdown *markdownEditorState
	if s.settingsUnit.state.settingsAgentLoaded {
		scope, groupID := s.settingsInstructionScope()
		key := s.instructionAutosaveKey(scope, "", groupID, s.settingsUnit.state.settingsAgentPath)
		markdown = s.ensureInstructionMarkdownEditor(key, "settings", 0, s.settingsUnit.state.settingsAgentContent)
	}
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Padding(16).Gap(8).Background(p.surface).Border(1, p.line).Radius(12).Children(func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Wrap().Shrink(0).Children(func() {
			s.settingsChoice(c, p, "agents-scope-global", "全局", s.settingsUnit.state.settingsAgentScope == "global", func() {
				s.deactivateInstructionEditor()
				s.settingsUnit.state.settingsAgentScope, s.settingsUnit.state.settingsAgentGroupID = "global", ""
				s.loadSettingsAgentInstructions()
			})
			for _, group := range s.snapshot.Groups {
				item := group
				s.settingsChoice(c, p, "agents-scope-"+item.ID, item.Name, s.settingsUnit.state.settingsAgentScope == item.ID, func() {
					s.deactivateInstructionEditor()
					s.settingsUnit.state.settingsAgentScope, s.settingsUnit.state.settingsAgentGroupID = item.ID, item.ID
					s.loadSettingsAgentInstructions()
				})
			}
			ui.Spacer(c)
			if markdown != nil {
				s.renderReadonlyMarkdownReaderActions(c, p, markdown, func(menu *overflowMenuBuilder) {
					s.renderSettingsInstructionMenuItems(c, menu)
				})
			} else {
				s.overflowMenu(c, p, "settings-agents-more-"+s.snapshot.Home+":"+s.settingsUnit.state.settingsAgentContext, "更多文档操作", false, func(menu *overflowMenuBuilder) {
					s.renderSettingsInstructionMenuItems(c, menu)
				})
			}
		})
		if !s.settingsUnit.state.settingsAgentLoaded {
			if state.settingsReadError != "" {
				ui.Row(c).Padding(10).Background(p.amberBg).Radius(8).Children(func() {
					label(c, state.settingsReadError, 11, p.amber).Wrap()
				})
			} else if state.settingsRequestPending || state.settingsReadDeferred {
				ui.Column(c).Grow(1).MinHeight(0).Padding(26).Gap(10).Children(func() {
					for _, width := range []float32{220, 380, 310} {
						ui.Box(c).FillWidth().MaxWidth(width).Height(14).Background(p.soft).Radius(4)
					}
				})
			}
		} else {
			scope, groupID := s.settingsInstructionScope()
			key := s.instructionAutosaveKey(scope, "", groupID, s.settingsUnit.state.settingsAgentPath)
			s.renderInstructionTextArea(c, p, key, "settings", 0, &s.settingsUnit.state.settingsAgentContent, 0)
		}
	})
}

func (s *app) renderSettingsInstructionMenuItems(c *ui.Context, menu *overflowMenuBuilder) {
	state := s.ensureTaskNotesUI()
	if menu.Item("reload", "重新读取").Icon("refresh").Disabled(s.busy || state.settingsRequestPending || state.settingsReadDeferred).Chosen() {
		s.requestSettingsInstructionRead(false)
	}
	if menu.Item("reveal", "显示文件位置").Icon("folder").Disabled(s.settingsUnit.state.settingsAgentPath == "" || s.systemRevealBusy()).Chosen() {
		s.act("system.reveal", "", "", "", s.settingsUnit.state.settingsAgentPath, "", nil)
	}
	if menu.Item("copy", "复制说明").Icon("copy").Disabled(!s.settingsUnit.state.settingsAgentLoaded).Chosen() {
		s.copyTextToClipboard(c.Services(), s.settingsUnit.state.settingsAgentContent, "已复制说明")
	}
	if state.settingsReadError != "" {
		menu.Separator()
		menu.Item("read-error", "读取失败："+state.settingsReadError).Disabled(true).Chosen()
	}
}

func (s *app) settingsInstructionTemplates(c *ui.Context, p palette) {
	state := s.ensureTaskNotesUI()
	settingsHelpCard(c, p, "", "", func() {
		if state.settingsTemplatesLoading {
			settingsMuted(c, p, "正在读取任务说明模板…")
		}
		if state.settingsTemplatesError != "" {
			ui.Row(c).Gap(8).Children(func() {
				label(c, "模板读取失败："+state.settingsTemplatesError, 11, p.amber).Grow(1).Wrap()
				if action(c, "settings-template-retry", "refresh", "重试", p, false) {
					s.requestSettingsInstructionTemplates()
				}
			})
		}
		if len(s.settingsUnit.state.settingsTemplates) == 0 && state.settingsTemplatesError == "" {
			if !state.settingsTemplatesLoading {
				settingsMuted(c, p, "还没有任务说明模板。")
			}
		}
		for index, template := range s.settingsUnit.state.settingsTemplates {
			id, name, content := mapString(template, "id"), mapString(template, "name"), mapString(template, "content")
			if name == "" {
				name = id
			}
			ui.Row(c).Padding(9, 10).Gap(8).Background(p.soft).Radius(8).Children(func() {
				ui.Column(c).Grow(1).Gap(3).Children(func() {
					label(c, name, 12, p.text).FontWeight(500)
					label(c, firstLine(content), 10, p.muted).SingleLine()
				})
				if iconButton(c, fmt.Sprintf("settings-template-edit-%d", index), "edit", "编辑模板", p).Clicked() {
					s.openForm("instructions.templates.save", "编辑任务说明模板", "", "", "", "", formField{key: "id", label: "模板 ID", value: id}, formField{key: "name", label: "模板名称", value: name}, formField{key: "content", label: "模板内容", value: content, multiline: true})
				}
				if iconButton(c, fmt.Sprintf("settings-template-remove-%d", index), "delete", "删除模板", p).Clicked() {
					s.confirmNative("删除任务说明模板", "此操作不可撤销。", name, func() { s.act("instructions.templates.remove", "", "", "", "", "", map[string]any{"id": id}) })
				}
			})
		}
		ui.Row(c).Justify(ui.End).Children(func() {
			if action(c, "settings-template-add", "plus", "新建模板", p, false) {
				s.openForm("instructions.templates.save", "新建任务说明模板", "", "", "", "", formField{key: "name", label: "模板名称"}, formField{key: "content", label: "模板内容", multiline: true})
			}
		})
	})
}

func (s *app) loadSettingsAgentInstructions() {
	s.deactivateInstructionEditor()
	scope, groupID := s.settingsInstructionScope()
	s.settingsUnit.state.settingsAgentContext = scope + ":" + groupID
	s.settingsUnit.state.settingsAgentLoaded = false
	s.requestSettingsInstructionRead(false)
}
