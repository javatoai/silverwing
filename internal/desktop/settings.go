package desktop

import (
	"github.com/egoist/mygo/ui"
)

type settingsSection struct {
	name  string
	group int
	pages []settingsPage
}

type settingsPage struct{ key, label string }

var settingsSections = []settingsSection{
	{"基础", 0, []settingsPage{{"basic", "外观"}}},
	{"快捷工具", 0, []settingsPage{{"tool-visibility", "研发工具"}}},
	{"存储", 0, []settingsPage{{"paths", "目录"}, {"config-backup", "配置与备份"}}},
	{"开发工具", 1, []settingsPage{{"dev-tools", "开发工具"}}},
	{"任务", 1, []settingsPage{{"task-creation", "任务创建区"}, {"task-area", "任务详情区"}}},
	{"Git", 1, []settingsPage{{"git", "Git 配置"}, {"branch-naming", "分支名设置"}, {"tag", "Tag 设置"}}},
	{"AGENTS.md", 2, []settingsPage{{"agents", "全局与组说明"}, {"instruction-templates", "任务说明模板"}}},
	{"Codex", 2, []settingsPage{{"codex-plugins", "Codex 插件"}, {"codex-mcp", "MCP 管理"}}},
	{"skill", 2, []settingsPage{{"skill-local", "本机 Skill"}, {"skill-sources", "安装 Skill"}}},
	{"CLI", 2, []settingsPage{{"cli-meegle", "Meegle CLI"}, {"cli-genbu", "Genbu CLI"}}},
	{"代理", 2, []settingsPage{{"proxy", "网络代理"}}},
	{"日志", 3, []settingsPage{{"logs", "日志"}}},
	{"系统", 3, []settingsPage{{"about", "关于"}, {"updates", "更新"}, {"app-data", "应用数据目录"}}},
}

func (s *app) settingsScreen(c *ui.Context, p palette) {
	if s.loading {
		ui.Column(c).Grow(1).MinHeight(0).Padding(24, 28).Background(p.bg).Children(func() {
			label(c, "正在读取配置…", 13, p.muted)
		})
		return
	}
	s.currentSettingsSection()
	ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Padding(16, 28, 28, 8).Gap(16).Children(func() {
		s.renderSettingsSidebar(c, p)
		ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Gap(12).Children(func() {
			section := s.currentSettingsSection()
			if len(section.pages) > 1 {
				ui.Row(c).Height(38).Gap(4).AlignItems(ui.Center).Children(func() {
					for _, page := range section.pages {
						s.settingsTabButton(c, p, page.key, page.label)
					}
				})
				ui.Divider(c)
			}
			s.observePageLocalRead()
			s.ensureSettingsPageData()
			if !s.localSkillsViewActive() || s.pageLocalReadReady() {
				s.observeSkillsBrowserNavigation()
			}
			if s.localSkillsViewActive() {
				ui.Box(c).Grow(1).FillWidth().MinWidth(0).MinHeight(0).Children(func() {
					s.renderSkillsBrowser(c, p)
				})
			} else if s.settingsPage == "agents" {
				ui.Box(c).Grow(1).FillWidth().MinWidth(0).MinHeight(0).Children(func() {
					s.settingsAgents(c, p)
				})
			} else {
				ui.Scroll(c).Grow(1).MinHeight(0).Gap(14).Children(func() {
					s.settingsPageContent(c, p)
				})
			}
		})
	})
	s.settingsConfigDialog(c, p)
	s.settingsImportDialog(c, p)
	s.settingsSkillPreviewDialog(c, p)
}

func (state *app) renderSettingsSidebar(context *ui.Context, theme palette) {
	ui.Scroll(context).Width(160).Shrink(0).Padding(10).Gap(4).Background(theme.soft).Border(1, theme.line).Radius(16).Children(func() {
		previousGroup := settingsSections[0].group
		for index, item := range settingsSections {
			if index > 0 && item.group != previousGroup {
				ui.Divider(context).Height(1).Margin(4, 14).Label("设置分组分隔线")
			}
			previousGroup = item.group
			button := ui.ButtonBase(context.Key("settings-section-"+item.name)).Height(43).FillWidth().Padding(0, 14).Justify(ui.Start).Radius(10).Label(item.name)
			foreground := theme.text
			weight := 400
			if state.settingsTab == item.name {
				button.Background(theme.selected)
				weight = 600
				if state.dark {
					foreground = ui.Hex("#DBEAFE")
				} else {
					foreground = ui.Hex("#173B86")
				}
			}
			button.Children(func() { label(context, item.name, 15, foreground).FontWeight(weight).LineHeight(23.0 / 15).NoWrap() })
			if button.Clicked() && state.settingsTab != item.name {
				state.deactivateInstructionEditor()
				state.settingsTab = item.name
				state.settingsPage = item.pages[0].key
				state.settingsOutput = ""
				state.settingsLoaded = ""
			}
		}
	})
}

func (s *app) settingsTabButton(c *ui.Context, p palette, key, name string) {
	button := ui.ButtonBase(c.Key("settings-page-"+key)).Height(34).Padding(0, 13).Radius(8).Label(name)
	fg := p.muted
	if s.settingsPage == key {
		button.Background(p.selected)
		fg = p.blue
	}
	button.Children(func() { label(c, name, 12, fg).FontWeight(500) })
	if button.Clicked() && s.settingsPage != key {
		s.deactivateInstructionEditor()
		s.settingsPage = key
		s.settingsOutput = ""
		s.settingsLoaded = ""
	}
}

func (s *app) currentSettingsSection() settingsSection {
	for _, section := range settingsSections {
		if section.name == s.settingsTab {
			for _, page := range section.pages {
				if page.key == s.settingsPage {
					return section
				}
			}
			s.settingsPage = section.pages[0].key
			s.settingsLoaded = ""
			return section
		}
	}
	s.settingsTab, s.settingsPage = settingsSections[0].name, settingsSections[0].pages[0].key
	return settingsSections[0]
}

func (s *app) settingsPageContent(c *ui.Context, p palette) {
	switch s.settingsPage {
	case "basic":
		s.settingsBasic(c, p)
	case "tool-visibility":
		s.settingsToolVisibility(c, p)
	case "paths":
		s.settingsPaths(c, p)
	case "config-backup":
		s.settingsConfigBackup(c, p)
	case "dev-tools":
		s.settingsDevelopmentTools(c, p)
	case "task-creation":
		s.settingsTaskCreation(c, p)
	case "task-area":
		s.settingsTaskArea(c, p)
	case "agents":
		s.settingsAgents(c, p)
	case "instruction-templates":
		s.settingsInstructionTemplates(c, p)
	case "codex-plugins":
		s.settingsCodexPlugins(c, p)
	case "codex-mcp":
		s.settingsCodexMCP(c, p)
	case "cli-meegle", "cli-genbu":
		s.settingsCLI(c, p)
	case "skill-sources":
		s.settingsSkills(c, p)
	case "proxy":
		s.settingsProxy(c, p)
	case "git":
		s.settingsGit(c, p)
	case "branch-naming":
		s.settingsBranchNaming(c, p)
	case "tag":
		s.settingsTag(c, p)
	case "logs":
		s.settingsLogs(c, p)
	case "about":
		s.settingsAbout(c, p)
	case "updates":
		s.settingsUpdates(c, p)
	case "app-data":
		s.settingsAppData(c, p)
	}
}

func settingsCard(c *ui.Context, p palette, title, description string, children func()) {
	settingsCardWithActions(c, p, title, description, nil, children)
}

func settingsCardWithActions(c *ui.Context, p palette, title, description string, headerActions, children func()) {
	settingsCardLayout(c, p, title, description, headerActions, children, false)
}

// Task Codex views also use settingsCard. Opt settings pages in explicitly so
// their compact help never changes task content or its original descriptions.
func settingsHelpCard(c *ui.Context, p palette, title, description string, children func()) {
	settingsHelpCardWithActions(c, p, title, description, nil, children)
}

func settingsHelpCardWithActions(c *ui.Context, p palette, title, description string, headerActions, children func()) {
	settingsCardLayout(c, p, title, description, headerActions, children, true)
}

func settingsCardLayout(c *ui.Context, p palette, title, description string, headerActions, children func(), compactHelp bool) {
	ui.Column(c).FillWidth().Padding(16).Gap(12).Background(p.surface).Border(1, p.line).Radius(12).Children(func() {
		hasHeading := title != "" || description != ""
		heading := func() {
			if compactHelp {
				settingsHelpLabel(c, p, title, description, 14, 600)
				return
			}
			if title != "" {
				label(c, title, 14, p.text).FontWeight(600).Wrap()
			}
			if description != "" {
				label(c, description, 11, p.muted).LineHeight(1.45).Wrap()
			}
		}
		if hasHeading && headerActions == nil {
			ui.Column(c).Gap(4).Children(heading)
		} else if hasHeading || headerActions != nil {
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Start).Children(func() {
				if hasHeading {
					ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(heading)
				} else {
					ui.Spacer(c)
				}
				ui.Box(c).Shrink(0).Children(headerActions)
			})
		}
		if hasHeading || headerActions != nil {
			ui.Divider(c)
		}
		children()
	})
}

var settingsAppearanceIcon = func() *ui.SVG {
	icon, err := ui.ParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.65" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3a9 9 0 0 0 0 18h1.3a2.2 2.2 0 0 0 1.6-3.7 2.1 2.1 0 0 1 1.5-3.6H18a3 3 0 0 0 3-3c0-4.8-4-7.7-9-7.7z"/><circle cx="7.5" cy="11.5" r="1"/><circle cx="10.5" cy="7.5" r="1"/><circle cx="15.5" cy="7.5" r="1"/></svg>`))
	if err != nil {
		panic(err)
	}
	return icon
}()

func settingsAppearanceCard(c *ui.Context, p palette, children func()) {
	ui.Column(c).FillWidth().Padding(20).Gap(12).Background(p.surface).Border(1, p.line).Radius(12).Children(func() {
		ui.Row(c).Gap(11).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Width(38).Height(38).Radius(10).Background(p.selected).Center().Children(func() {
				ui.Icon(c, settingsAppearanceIcon).Width(20).Height(20).Shrink(0).TextColor(p.blue)
			})
			label(c, "外观", 16, p.text).FontWeight(600)
		})
		ui.Divider(c)
		children()
	})
}

func settingsMuted(c *ui.Context, p palette, text string) {
	label(c, text, 11, p.muted).LineHeight(1.45)
}
