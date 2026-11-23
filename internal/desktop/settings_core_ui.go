package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

func (s *app) settingsBasic(c *ui.Context, p palette) {
	settingsAppearanceCard(c, p, func() {
		s.settingsAppearanceLocalSwitchRow(c, p, "workspace-navigation-collapsed", "折叠工作空间导航", "默认开启，仅保留最左侧导航图标，为右侧内容腾出更多空间。更改立即生效并自动记住。", &s.navigationCollapsed)
		settingsAppearanceDivider(c)
		s.settingsAppearanceThemeRow(c, p)
	})
}

func (s *app) settingsNavigation(c *ui.Context, p palette) {
	settingsHelpCard(c, p, "侧边栏展示", "任务和设置始终显示。隐藏入口不会关闭对应功能。", func() {
		for index, item := range []struct{ key, title string }{
			{"requirements", "需求"}, {"tools", "研发工具"}, {"production", "生产发布"}, {"services", "服务"},
		} {
			if item.key == "production" && !s.integrationReady("genbu") {
				continue
			}
			if index > 0 {
				settingsAppearanceDivider(c)
			}
			s.settingsAppearanceNestedSwitchRow(c, p, "sidebarVisibility", item.key, "sidebar-visibility-"+item.key, item.title, "", true)
		}
	})
}

func (s *app) settingsToolVisibility(c *ui.Context, p palette) {
	settingsHelpCard(c, p, "研发工具", "选择显示在主导航中的工具。", func() {
		for index, tab := range developerToolTabs {
			if index > 0 {
				settingsAppearanceDivider(c)
			}
			s.settingsAppearanceNestedSwitchRow(c, p, "toolVisibility", tab.visibilityKey, "tool-visibility-"+tab.visibilityKey, tab.title, tab.description, tab.visibleByDefault)
		}
	})
}

func (s *app) settingsTaskPaths(c *ui.Context, p palette) {
	if s.snapshot.ProfileID != "" {
		settingsHelpCard(c, p, "", "", func() {
			taskRoot := s.snapshotTaskRoot()
			settingsAppearanceRow(c, p, "任务目录", "保存研发任务及其 Git 工作区。Tag 和生产发布的临时 Worktree 会放在此目录下的独立子目录。", func(bool) {
				ui.Row(c).FillWidth().MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
					label(c, firstNonEmpty(taskRoot, "尚未设置"), 12, p.text).Grow(1).MinWidth(0).Wrap().Selectable()
					buttonText := "选择目录"
					if taskRoot != "" {
						buttonText = "更改目录"
					}
					if settingsSystemAction(c, "settings-browse-task-root", "folder", buttonText, p, settingsSystemActionOutline, s.busy || s.backend == nil || s.settingsUnit.state.settingsPickerBusy) {
						s.pickProfileTaskRoot()
					}
				})
			})
		})
		return
	}
	settingsHelpCard(c, p, "任务路径设置", "更改目录时选择是否迁移旧任务数据。", func() {
		path := s.input("setting:taskRootTarget", s.configString("taskRoot"))
		settingsAppearanceRow(c, p, "任务目录", "选择迁移旧任务，或保留旧目录并直接切换。", func(stacked bool) {
			s.taskRootMigrationPathControl(c, p, path, stacked)
		})
	})
}

func (s *app) settingsPaths(c *ui.Context, p palette) {
	settingsHelpCard(c, p, "任务资料仓库", "需求资料保存在仓库的 requirements 目录。", func() {
		s.settingsMaterialsGitSync(c, p)
	})
	settingsHelpCard(c, p, "目录与浏览", "", func() {
		s.settingsAppearanceTextRow(c, p, "requirementMaterialsSubdirectory", "任务角色名", "此名称用作每份需求资料的可写子目录，例如研发。", "例如研发", func(value string) { s.saveConfigField("requirementMaterialsSubdirectory", nullableString(value)) })
		settingsAppearanceDivider(c)
		s.settingsMaterialsExpansionDepth(c, p)
	})
}

func (s *app) settingsConfigBackup(c *ui.Context, p palette) {
	settingsHelpCard(c, p, "配置与备份", "将当前配置保存为 ZIP。恢复时可先查看变更。", func() {
		state := s.ensureSettingsConfigRecovery()
		disabled := s.busy || s.settingsUnit.state.settingsPickerBusy || state.previewLoading
		ui.Row(c).Gap(8).Wrap().Children(func() {
			if settingsSystemAction(c, "settings-config-backup", "save", "手动备份", p, settingsSystemActionOutline, disabled) {
				s.backupConfig()
			}
			if settingsSystemAction(c, "settings-config-restore", "folder", "手动恢复", p, settingsSystemActionOutline, disabled) {
				s.restoreConfig()
			}
		})
		if state.previewLoading {
			settingsMuted(c, p, "正在读取备份内容…")
		} else if state.previewError != "" {
			label(c, state.previewError, 12, p.amber).Wrap()
		}
	})
}

func (s *app) settingsDevelopmentTools(c *ui.Context, p palette) {
	settingsHelpCardWithActions(c, p, "开发工具", "路径留空时由系统自动探测。选择后可直接测试打开。", func() {
		s.overflowMenu(c, p, "settings-dev-tools-more", "更多开发工具操作", s.busy, func(menu *overflowMenuBuilder) {
			if menu.Item("detect-missing", "检测缺失工具").Icon("search").Disabled(s.busy).Chosen() {
				s.act("development-tools/detect", "", "", "", "", "", nil)
			}
		})
	}, func() {
		for _, tool := range []struct{ kind, name string }{
			{"INTELLIJ_IDEA", "IntelliJ IDEA"}, {"WEBSTORM", "WebStorm"}, {"PYCHARM", "PyCharm"},
			{"VISUAL_STUDIO_CODE", "Visual Studio Code"}, {"ANDROID_STUDIO", "Android Studio"}, {"DEVECO_STUDIO", "DevEco Studio"},
		} {
			typeKey, title := tool.kind, tool.name
			toolStatus := findMap(mapsFromData(dataValue(s.settingsUnit.state.settingsDevelopmentToolsSnapshot, "tools")), "type", typeKey)
			initialPath := firstNonEmpty(mapString(toolStatus, "path"), s.developmentToolPath(typeKey))
			value := s.input("setting:development-tool:"+typeKey, initialPath)
			settingsReveal(c, ui.Column(c).FillWidth().Padding(10, 12).Gap(7).Background(p.soft).Radius(9), p, title).Children(func() {
				ui.Row(c).Gap(8).Children(func() {
					label(c, title, 12, p.text).FontWeight(500).Grow(1)
					state, color := developmentToolStatusLabel(toolStatus, p)
					label(c, state, 10, color).SingleLine()
				})
				ui.Row(c).Gap(7).Children(func() {
					ui.TextInput(c.Key("settings-tool-path-"+typeKey), value).Grow(1).FillWidth().Height(36).Placeholder("应用程序路径")
					if iconButton(c, "settings-tool-pick-"+typeKey, "folder", "手动选择", p).Clicked() {
						s.pickNativePath("选择"+title, *value, false, nil, func(path string) { *value = path })
					}
					s.overflowMenu(c, p, "settings-tool-more-"+typeKey, title+"更多操作", false, func(menu *overflowMenuBuilder) {
						if menu.Item("test-open", "测试打开").Icon("file").Disabled(s.busy || strings.TrimSpace(*value) == "").Chosen() {
							s.act("system.ide-test", "", "", "", s.configString("taskRoot"), "", map[string]any{"tool": typeKey, "configuredPath": *value})
						}
						if menu.Item("restore-auto", "恢复自动").Icon("refresh").Disabled(strings.TrimSpace(*value) == "").Chosen() {
							*value = ""
						}
					})
				})
				s.autosaveSettingsInput("development-tool:"+typeKey, value, func(path string) {
					s.saveDevelopmentToolPath(typeKey, path)
				})
			})
		}
		settingsReveal(c, ui.Row(c).FillWidth().MinWidth(0).Wrap().Gap(8), p, "全局默认开发工具").Children(func() {
			label(c, "全局默认开发工具", 12, p.text).Width(140)
			for _, tool := range []struct{ value, label string }{{"INTELLIJ_IDEA", "IntelliJ IDEA"}, {"WEBSTORM", "WebStorm"}, {"PYCHARM", "PyCharm"}, {"VISUAL_STUDIO_CODE", "VS Code"}, {"ANDROID_STUDIO", "Android Studio"}, {"DEVECO_STUDIO", "DevEco Studio"}} {
				choice := tool
				s.settingsChoice(c, p, "default-tool-"+choice.value, choice.label, s.configString("defaultDevelopmentTool") == choice.value, func() { s.saveConfigField("defaultDevelopmentTool", choice.value) })
			}
		})
		s.settingsSwitch(c, p, "allowTemporaryDevelopmentToolSelection", "允许临时选择开发工具", "在任务工作区中临时切换工具，不修改全局默认值。", false)
	})
}

func (s *app) settingsTaskCreation(c *ui.Context, p palette) {
	s.settingsWorkspaceTools(c, p)
	s.settingsBranchNaming(c, p)
	settingsHelpCardWithActions(c, p, "AI 命名", "仅将需求标题与正文片段交给配置的模型；保留本地编辑能力。", func() {
		s.settingsConfigureCLIAction(c, p, "codex")
	}, func() {
		s.settingsCLIDependencyStatus(c, p, "codex")
		s.settingsSwitch(c, p, "aiRequirementNamingEnabled", "开启 AI 生成分支名和文件夹名", "创建任务时自动补全尚未手动修改的名称。", false)
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

func (s *app) settingsTaskArea(c *ui.Context, p palette) {
	settingsHelpCard(c, p, "任务详情工具栏", "选择任务详情中显示的操作组。", func() {
		s.settingsSwitch(c, p, "showTaskDetailGitActionGroup", "Git 工具组", "显示提交、提交并推送和推送操作。", false)
		s.settingsSwitch(c, p, "showTaskDetailPathActionGroup", "路径工具组", "显示复制任务路径、终端和打开任务目录操作。", false)
	})
	settingsHelpCard(c, p, "工作区卡片工具栏", "选择工作区卡片中显示的操作组。", func() {
		s.settingsSwitch(c, p, "showWorkspaceGitActionGroup", "Git 工具组", "显示提交、提交并推送和推送操作。", false)
		s.settingsSwitch(c, p, "showWorkspacePathActionGroup", "路径工具组", "显示终端、打开文件夹和复制路径操作。", false)
	})
	settingsHelpCard(c, p, "信息复制", "控制工作区和需求信息旁边的复制图标。\n\n工作区项目名和分支名可以直接点击复制。", func() {
		s.settingsRequirementCopyIcons(c, p)
	})
}

func (s *app) settingsAgents(c *ui.Context, p palette) {
	state := s.ensureTaskNotesUI()
	if state.settingsReadDeferred && !s.busy {
		s.requestSettingsInstructionRead(state.settingsReadAcceptDisk)
	}
	var markdown *markdownEditorState
	if s.settingsUnit.state.settingsAgentLoaded {

		key := s.instructionAutosaveKey("global", "", "", s.settingsUnit.state.settingsAgentPath)
		markdown = s.ensureInstructionMarkdownEditor(key, "settings", 0, s.settingsUnit.state.settingsAgentContent)
	}
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Padding(16).Gap(8).Background(p.surface).Border(1, p.line).Radius(12).Children(func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Wrap().Shrink(0).Children(func() {
			ui.Spacer(c)
			if markdown != nil && markdown.readOnly {
				s.renderReadonlyMarkdownReaderActions(c, p, markdown, func(menu *overflowMenuBuilder) {
					s.renderSettingsInstructionMenuItems(c, menu)
				})
			} else {
				if markdown != nil {
					s.renderInstructionSaveStatus(c, p, markdown.binding.id)
				}
				s.overflowMenu(c, p, "settings-agents-more-"+s.snapshot.Home+":"+s.settingsUnit.state.settingsAgentContext, "更多文档操作", false, func(menu *overflowMenuBuilder) {
					s.renderSettingsInstructionMenuItems(c, menu)
				})
			}
		})
		if s.settingsUnit.state.settingsAgentLoaded && state.settingsReadError != "" {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Padding(8).Background(p.amberBg).Radius(8).Children(func() {
				label(c, state.settingsReadError, 11, p.amber).Grow(1).Wrap()
				if action(c, "settings-agents-read-retry", "refresh", "重试读取", p, false) {
					s.requestSettingsInstructionRead(false)
				}
			})
		}
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

			key := s.instructionAutosaveKey("global", "", "", s.settingsUnit.state.settingsAgentPath)
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

func (s *app) loadSettingsAgentInstructions() {
	if !s.leaveSettingsInstructionEditor() {
		return
	}

	s.settingsUnit.state.settingsAgentContext = "global:"
	s.settingsUnit.state.settingsAgentLoaded = false
	s.requestSettingsInstructionRead(false)
}
