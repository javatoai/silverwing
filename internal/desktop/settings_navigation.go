package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

type settingsSection struct {
	name  string
	group int
	pages []settingsPage
}

type settingsPage struct{ key, label string }

var settingsSections = []settingsSection{
	{"外观与交互", 0, []settingsPage{{"basic", "外观"}, {"tool-visibility", "导航与快捷工具"}, {"task-area", "任务工具栏"}, {"task-tabs", "任务页签"}}},
	{"任务与工作区", 0, []settingsPage{{"task-paths", "任务目录"}, {"task-creation", "创建与命名"}}},
	{"资料与说明", 0, []settingsPage{{"paths", "资料目录与浏览"}, {"materials-sync", "资料自动同步"}, {"agents", "全局与组说明"}, {"instruction-templates", "任务说明模板"}}},
	{"Git 与发布", 1, []settingsPage{{"git", "Git 环境与写保护"}, {"tag", "Tag 与构建"}}},
	{"AI 与扩展", 1, []settingsPage{{"codex-plugins", "Codex 插件"}, {"codex-mcp", "MCP 服务"}, {"skill-local", "本机 Skill"}, {"skill-sources", "Skill 来源与安装"}}},
	{"工具与连接", 1, []settingsPage{{"dev-tools", "编辑器与 IDE"}, {"cli-tools", "命令行与账号"}, {"proxy", "网络代理"}}},
	{"数据与维护", 2, []settingsPage{{"config-backup", "配置与备份"}, {"app-data", "数据目录"}, {"logs", "日志与诊断"}, {"about", "关于与更新"}}},
}

func settingsCanonicalPage(key string) string {
	switch key {
	case "cli-meegle", "cli-lark", "cli-genbu", "cli-codex", "cli-git":
		return "cli-tools"
	case "branch-naming":
		return "task-creation"
	case "updates":
		return "about"
	}
	return key
}

func settingsSectionForPage(key string) (settingsSection, bool) {
	key = settingsCanonicalPage(key)
	for _, section := range settingsSections {
		for _, page := range section.pages {
			if page.key == key {
				return section, true
			}
		}
	}
	return settingsSection{}, false
}

func (s *app) currentSettingsSection() settingsSection {
	state := &s.settingsUnit.state
	if section, ok := settingsSectionForPage(state.settingsPage); ok {
		old := state.settingsPage
		state.settingsTab, state.settingsPage = section.name, settingsCanonicalPage(old)
		if old != state.settingsPage {
			state.settingsLoaded = ""
			if tool := settingsCLIToolFromPage(old); tool != "" {
				state.settingsCLITool = tool
				state.settingsSearchTarget, state.settingsSearchPending = settingsCLITitle(tool)+" 状态", true
			}
		}
		return section
	}
	for _, section := range settingsSections {
		if section.name == state.settingsTab {
			state.settingsPage = section.pages[0].key
			return section
		}
	}
	state.settingsTab, state.settingsPage = settingsSections[0].name, settingsSections[0].pages[0].key
	return settingsSections[0]
}

func (s *app) navigateSettingsPage(key, target string) bool {
	cliTool := settingsCLIToolFromPage(key)
	key = settingsCanonicalPage(key)
	section, ok := settingsSectionForPage(key)
	if !ok || !s.leaveSettingsInstructionEditor() {
		return false
	}
	state := &s.settingsUnit.state
	if draft := s.inputs["setting:taskRootTarget"]; state.settingsPage == "task-paths" && key != "task-paths" && draft != nil {
		target := strings.TrimSpace(*draft)
		if target != "" && workspaceHealthPathKey(target) != workspaceHealthPathKey(s.configString("taskRoot")) {
			// Keep the confirmation's owner page stable until the user chooses.
			s.requestTaskRootChange(target)
			return false
		}
	}
	if state.settingsLastPages == nil {
		state.settingsLastPages = make(map[string]string)
	}
	if previous, valid := settingsSectionForPage(state.settingsPage); valid {
		state.settingsLastPages[previous.name] = settingsCanonicalPage(state.settingsPage)
	}
	if key != state.settingsPage || cliTool != "" && cliTool != s.currentSettingsCLITool() {
		state.settingsLoaded, state.settingsOutput = "", ""
	}
	if cliTool != "" {
		state.settingsCLITool = cliTool
	}
	state.settingsTab, state.settingsPage = section.name, key
	state.settingsLastPages[section.name] = key
	state.settingsSearchTarget, state.settingsSearchPending = target, target != ""
	state.settingsSearchEpoch++
	s.observeMeegleProjectPicker()
	return true
}

func (s *app) settingsScreen(c *ui.Context, p palette) {
	if s.loading {
		ui.Column(c).Grow(1).Padding(24, 28).Children(func() { label(c, "正在读取配置…", 13, p.muted) })
		return
	}
	s.currentSettingsSection()
	reveal := ui.Local(c.Root(), settingsRevealKey{}, func() settingsRevealState { return settingsRevealState{} })
	reveal.active, reveal.target, reveal.pending = true, s.settingsUnit.state.settingsSearchTarget, s.settingsUnit.state.settingsSearchPending
	epoch := s.settingsUnit.state.settingsSearchEpoch
	if reveal.epoch != epoch {
		reveal.epoch = epoch
		reveal.last = ui.Rect{}
		reveal.stable = false
		reveal.scroll = nil
	}
	defer func() {
		if epoch == s.settingsUnit.state.settingsSearchEpoch {
			s.settingsUnit.state.settingsSearchPending = reveal.pending
		}
		reveal.active = false
	}()
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Padding(16, 24, 24, 8).Gap(16).Children(func() {
		s.renderSettingsSidebar(c, p)
		ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Gap(12).Children(func() {
			section := s.currentSettingsSection()
			ui.Row(c).FillWidth().MinHeight(36).Wrap().Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Grow(1).MinWidth(0).Wrap().Gap(4).Children(func() {
					for _, page := range section.pages {
						s.settingsTabButton(c, p, page.key, page.label)
					}
				})
				s.renderSettingsSaveStatus(c, p)
			})
			if s.settingsUnit.state.settingsPage == "cli-tools" {
				s.renderSettingsCLITabs(c, p)
			}
			s.observePageLocalRead()
			s.ensureSettingsPageData()
			if !s.localSkillsViewActive() || s.pageLocalReadReady() {
				s.observeSkillsBrowserNavigation()
			}
			if s.localSkillsViewActive() {
				reveal.pending = false
				ui.Box(c).Grow(1).FillWidth().MinWidth(0).MinHeight(0).Children(func() { s.renderSkillsBrowser(c, p) })
			} else if s.settingsUnit.state.settingsPage == "agents" {
				reveal.pending = false
				ui.Box(c).Grow(1).FillWidth().MinWidth(0).MinHeight(0).Children(func() { s.settingsAgents(c, p) })
			} else {
				s.renderSettingsContent(c, p, reveal)
			}
		})
	})
	s.settingsConfigDialog(c, p)
	s.settingsImportDialog(c, p)
	s.settingsSkillPreviewDialog(c, p)
}

func (s *app) renderSettingsSidebar(c *ui.Context, p palette) {
	ui.Scroll(c).Width(164).Shrink(0).Padding(8).Gap(4).Background(p.soft).Border(1, p.line).Radius(12).Children(func() {
		for _, section := range settingsSections {
			item := section
			button := ui.ButtonBase(c.Key("settings-section-"+item.name)).Row().Height(42).FillWidth().Padding(0, 12).Gap(6).AlignItems(ui.Center).Justify(ui.Start).Radius(8).Label(item.name)
			fg, weight := p.text, 400
			if s.settingsUnit.state.settingsTab == item.name {
				button.Background(p.selected)
				weight = 600
			}
			button.Children(func() {
				label(c, item.name, 14, fg).FontWeight(weight).NoWrap()
				if item.name == "数据与维护" && s.hasAppUpdate() {
					appUpdateDot(c, p, false)
				}
			})
			if button.Clicked() && s.settingsUnit.state.settingsTab != item.name {
				key := s.settingsUnit.state.settingsLastPages[item.name]
				if section, ok := settingsSectionForPage(key); !ok || section.name != item.name {
					key = item.pages[0].key
				}
				if s.navigateSettingsPage(key, "") {
					s.settingsUnit.state.settingsSearchQuery = ""
				}
			}
		}
	})
}

func (s *app) settingsTabButton(c *ui.Context, p palette, key, name string) {
	b := ui.ButtonBase(c.Key("settings-page-"+key)).Row().Height(34).Padding(0, 12).Gap(6).AlignItems(ui.Center).Radius(8).Label(name)
	fg := p.muted
	if s.settingsUnit.state.settingsPage == key {
		b.Background(p.selected)
		fg = p.blue
		if s.dark {
			fg = p.text
		}
	}
	b.Children(func() {
		label(c, name, 12, fg).FontWeight(500)
		if key == "about" && s.hasAppUpdate() {
			appUpdateDot(c, p, false)
		}
	})
	if b.Clicked() && s.settingsUnit.state.settingsPage != key {
		s.navigateSettingsPage(key, "")
	}
}

func (s *app) settingsPageContent(c *ui.Context, p palette) {
	switch s.settingsUnit.state.settingsPage {
	case "basic":
		s.settingsBasic(c, p)
	case "tool-visibility":
		s.settingsNavigation(c, p)
		s.settingsToolVisibility(c, p)
	case "task-paths":
		s.settingsTaskPaths(c, p)
	case "paths":
		s.settingsPaths(c, p)
	case "materials-sync":
		settingsHelpCard(c, p, "资料 Git 自动同步", "仅同步任务资料仓库。", func() { s.settingsMaterialsGitSync(c, p) })
	case "config-backup":
		s.settingsConfigBackup(c, p)
	case "dev-tools":
		s.settingsDevelopmentTools(c, p)
	case "task-creation":
		s.settingsTaskCreation(c, p)
	case "task-area":
		s.settingsTaskArea(c, p)
	case "task-tabs":
		s.settingsTaskTabs(c, p)
	case "agents":
		s.settingsAgents(c, p)
	case "instruction-templates":
		s.settingsInstructionTemplates(c, p)
	case "codex-plugins":
		s.settingsCodexPlugins(c, p)
	case "codex-mcp":
		s.settingsCodexMCP(c, p)
	case "cli-tools":
		s.settingsCommandTools(c, p)
	case "skill-sources":
		s.settingsSkills(c, p)
	case "proxy":
		s.settingsProxy(c, p)
	case "git":
		s.settingsGit(c, p)
	case "tag":
		s.settingsTag(c, p)
	case "logs":
		s.settingsLogs(c, p)
	case "about":
		s.settingsAbout(c, p)
		s.settingsUpdates(c, p)
	case "app-data":
		s.settingsAppData(c, p)
	}
}

func settingsCLITitle(tool string) string {
	return map[string]string{"git": "Git", "codex": "Codex CLI", "meegle": "Meegle CLI", "lark": "Lark CLI", "genbu": "Genbu CLI"}[tool]
}

func (s *app) settingsCommandTools(c *ui.Context, p palette) {
	tool := s.currentSettingsCLITool()
	s.renderSettingsCLI(c, p, tool, settingsCLITitle(tool), tool+"ExecutablePath")
}

func (s *app) settingsCLIPageActive(tool string) bool {
	return s.settingsUnit.state.settingsPage == "cli-tools" && s.currentSettingsCLITool() == tool || s.settingsUnit.state.settingsPage == "cli-"+tool
}

func (s *app) settingsCLIDependency(c *ui.Context, p palette, tool, title string) {
	settingsHelpCard(c, p, title, "命令路径在“工具与连接”中统一管理。", func() {
		state, color := settingsToolAvailability(s.settingsUnit.state.settingsCLIStatus[tool], p)
		settingsToolStatusRow(c, p, settingsCLITitle(tool), state, color)
		if action(c, "settings-configure-"+tool, "", "配置 "+settingsCLITitle(tool), p, false) {
			s.navigateSettingsPage("cli-"+tool, settingsCLITitle(tool)+" 状态")
		}
	})
}

func (s *app) renderSettingsSaveStatus(c *ui.Context, p palette) {
	text, color := "更改后自动保存", p.muted
	if state := s.settingsUnit.state.settingsAutosave; state != nil && state.home == s.snapshot.Home {
		if state.failure != "" {
			text, color = "保存失败，输入已保留", p.danger
		} else if state.active != nil {
			text = "正在保存…"
		} else {
			if state.saved {
				text = "已保存"
			}
			for _, entry := range state.entries {
				if entry.pending {
					text = "等待保存…"
					break
				}
			}
		}
	}
	label(c, text, 11, color).Wrap()
}
