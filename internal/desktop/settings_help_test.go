package desktop

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

func TestSettingsHelpNativeHoverKeyboardAndEscape(t *testing.T) {
	help := "仅查看功能说明，不保存配置或启动操作。"
	tt := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		ui.Column(c).Fill().Padding(32).Gap(12).Children(func() {
			settingsHelpLabel(c, p, "功能", help, 12, 600)
			settingsHelpLabel(c, p, "无说明", "", 12, 600)
			ui.Button(c, "下一项")
		})
	}, 480, 300)
	if tt.HasText(help) {
		t.Fatal("help is visible before hover or keyboard focus")
	}
	if _, found := tt.Find("无说明说明"); found {
		t.Fatal("an empty help created an extra icon")
	}
	icon, found := tt.Find("功能说明")
	if !found {
		t.Fatal("the help icon is missing")
	}
	tt.Move(icon.X+icon.W/2, icon.Y+icon.H/2)
	time.Sleep(80 * time.Millisecond)
	tt.Frame()
	if tt.HasText(help) {
		t.Fatal("hover help appeared before the native delay")
	}
	time.Sleep(550 * time.Millisecond)
	tt.Frame()
	if !tt.HasText(help) {
		t.Fatal("native hover help did not appear")
	}
	tt.Key(0, ui.KeyEscape)
	if tt.HasText(help) {
		t.Fatal("Escape did not dismiss hover help")
	}
	tt.Move(470, 290)
	tt.Frame()
	tt.Key(0, ui.KeyTab)
	if !tt.Focused("功能说明") || !tt.HasText(help) {
		t.Fatal("keyboard focus did not show the help")
	}
	tt.Key(0, ui.KeyEscape)
	if tt.HasText(help) {
		t.Fatal("Escape did not dismiss keyboard help")
	}
	tt.Key(0, ui.KeyTab)
	if !tt.Focused("下一项") {
		t.Fatal("help trapped the keyboard focus")
	}
}

func TestSettingsHelpDoesNotChangeSharedTaskCards(t *testing.T) {
	tt := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		ui.Column(c).Fill().Padding(24).Children(func() {
			settingsCard(c, p, "任务目录会话", "只列出当前任务目录的会话。关联不会创建新会话。", func() {})
		})
	}, 600, 400)
	if !tt.HasText("关联不会创建新会话") {
		t.Fatal("task-card descriptions were hidden by settings help")
	}
	if _, found := tt.Find("任务目录会话说明"); found {
		t.Fatal("settings help leaked into a shared task card")
	}
}

func TestMaterialsSettingsHelpCurrentScopeAndActiveStates(t *testing.T) {
	s, backend, _ := materialsGitDesktopFixture(t)
	s.materialsUnit.state.materialsGitCapture = true
	cfg, _ := s.materialsGitConfig()
	cfg.MaterialsGitSync.Enabled = true
	encoded, _ := json.Marshal(cfg)
	s.settingsUnit.state.configJSON = string(encoded)
	s.materialsUnit.state.materialsGit.scope = application.MaterialsGitScope(s.snapshot.Home, cfg)
	s.materialsUnit.state.materialsGit.status.LastSuccess = "2026-10-26T01:14:00Z"
	s.materialsUnit.state.materialsGitBranches = &materialsGitBranchesState{scope: s.materialsUnit.state.materialsGit.scope, loaded: true, branches: []string{"master"}}
	tt := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		ui.Column(c).Fill().Padding(24).Gap(10).Children(func() { s.settingsMaterialsGitSync(c, p) })
	}, 960, 760)
	if tt.HasText("等待下次同步") || tt.HasText("最近成功") {
		t.Fatal("idle status still takes up an inline row")
	}
	tt.Key(0, ui.KeyTab)
	if !tt.Focused("Git 自动同步说明") || !tt.HasText("等待下次同步") || !tt.HasText("最近成功") {
		t.Fatal("sync help does not contain the current idle status")
	}
	oldText := "最近成功 " + time.Date(2026, 10, 26, 1, 14, 0, 0, time.UTC).Local().Format("2006-01-02 15:04")
	s.materialsUnit.state.materialsGit.status.LastSuccess = "2026-10-26T02:15:00Z"
	tt.Frame()
	if tt.HasText(oldText) || !tt.HasText("最近成功 "+time.Date(2026, 10, 26, 2, 15, 0, 0, time.UTC).Local().Format("2006-01-02 15:04")) {
		t.Fatal("the open tooltip retained stale status text")
	}
	tt.Key(0, ui.KeyEscape)
	s.materialsUnit.state.materialsGit.phase = "等待资料保存或文件操作完成"
	tt.Frame()
	if !tt.HasText(s.materialsUnit.state.materialsGit.phase) {
		t.Fatal("the deferred-save warning was hidden")
	}
	s.materialsUnit.state.materialsGit.running, s.materialsUnit.state.materialsGit.phase = true, "正在合并远程更新"
	tt.Frame()
	if !tt.HasText("正在合并远程更新") {
		t.Fatal("active sync progress was hidden")
	}
	if tt.HasText("最近成功") {
		t.Fatal("the last-success detail escaped its tooltip while syncing")
	}
	s.materialsUnit.state.materialsGit.running, s.materialsUnit.state.materialsGit.needsResume = false, true
	s.materialsUnit.state.materialsGit.phase = ""
	s.materialsUnit.state.materialsGit.status.Error = "同步失败，请核对仓库身份"
	tt.Frame()
	if !tt.HasText("同步已暂停") || !tt.HasText(s.materialsUnit.state.materialsGit.status.Error) || !tt.HasText("重新检查并恢复") {
		t.Fatal("the actionable error or its recovery entry was hidden")
	}
	if tt.HasText("最近成功") {
		t.Fatal("the last-success detail escaped its tooltip while paused")
	}
	s.materialsUnit.state.materialsGit.reading = true
	tt.Frame()
	if !tt.HasText("正在读取同步状态…") {
		t.Fatal("the loading state was hidden")
	}
	s.snapshot.Home = t.TempDir()
	tt.Frame()
	if tt.HasText("最近成功") || tt.HasText("同步失败，请核对仓库身份") {
		t.Fatal("old-user details survived a scope change")
	}
	if backend.count.Load() != 0 {
		t.Fatal("viewing help triggered a sync")
	}
}

func TestSettingsSkillHelpDoesNotSelectOrChangeOtherPages(t *testing.T) {
	s := newApp(nil, Options{Page: "settings"})
	state := &skillsBrowserState{app: s, catalogLoaded: true, skills: []map[string]any{{"name": "示例 Skill", "directoryName": "example", "description": "仅供查看的 Skill 功能说明"}}}
	tt := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		s.renderSkillsBrowserList(c, p, state)
	}, 480, 500)
	if tt.HasText("仅供查看的 Skill 功能说明") {
		t.Fatal("the settings list still shows an inline description")
	}
	if err := tt.Click("示例 Skill说明"); err != nil {
		t.Fatal(err)
	}
	if state.selectedDirectory != "" {
		t.Fatal("the help icon selected a Skill")
	}
	s.page = "skills"
	tt.Frame()
	if !tt.HasText("仅供查看的 Skill 功能说明") {
		t.Fatal("the non-settings Skill list lost its description")
	}
	if _, found := tt.Find("示例 Skill说明"); found {
		t.Fatal("the settings help leaked into another page")
	}
}

func TestSettingsHelpAllPagesLayout(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			for _, section := range settingsSections {
				for _, page := range section.pages {
					t.Run(fmt.Sprintf("%s-%d-dark-%t", page.key, width, dark), func(t *testing.T) {
						s, backend, _ := materialsGitDesktopFixture(t)
						s.backend = nil // No CLI, network or filesystem actions in a layout pass.
						s.dark = dark
						s.page, s.settingsUnit.state.settingsTab, s.settingsUnit.state.settingsPage = "settings", section.name, page.key
						s.materialsUnit.state.materialsGitCapture = true
						s.desktopPreferences = &desktopPreferencesState{value: DefaultDesktopPreferences()}
						s.materialsUnit.state.materialsGitBranches = &materialsGitBranchesState{scope: s.materialsUnit.state.materialsGit.scope, loaded: true, branches: []string{"master"}}
						ext := s.ensureSettingsExtensionRequests()
						ext.workspaceToolsLoaded, ext.workspaceToolsScope = true, s.settingsExtensionCurrentScope()
						ext.workspaceTools = []taskCreationWorkspaceToolOption{{ID: "codex", DisplayName: "Codex", Description: "打开对应任务目录", Available: true}}
						s.settingsUnit.state.skillsBrowser = &skillsBrowserState{app: s, catalogLoaded: true}
						tt := ui.NewTester(func(c *ui.Context) {
							p := setTheme(c, dark)
							ui.Scroll(c).Fill().Padding(24).Gap(14).Background(p.bg).Children(func() {
								if page.key == "skill-local" {
									s.renderSkillsBrowserList(c, p, s.settingsUnit.state.skillsBrowser)
								} else {
									s.settingsPageContent(c, p)
								}
							})
						}, width, 1000)
						for _, old := range []string{"选择随系统变化，或固定使用浅色和深色主题。", "更改目录时选择是否迁移旧任务数据。", "0 表示全部折叠；修改后重新应用", "仅将需求标题与正文片段交给配置的模型", "工作区项目名和分支名可以直接点击复制。", "按最近更新时间保留，超过后清理", "未配置时自动检测；也可手动填写", "下载适用于当前系统的安装包"} {
							if tt.HasText(old) {
								t.Fatalf("inline auxiliary text remains on %s: %s", page.key, old)
							}
						}
						captureDesktopTestFrame(t, tt, fmt.Sprintf("settings-help-%s-%d-dark-%t", page.key, width, dark))
						if page.key == "paths" {
							shell := ui.NewTester(func(c *ui.Context) {
								p := setTheme(c, dark)
								ui.Row(c).Fill().AlignItems(ui.Stretch).Background(p.bg).Children(func() {
									s.sidebar(c, p, true)
									ui.Column(c).Grow(1).MinWidth(0).Children(func() { s.settingsScreen(c, p) })
								})
							}, width, 1000)
							for _, name := range []string{"Git 自动同步说明", "默认展开目录层级说明", "同步周期（分钟）"} {
								assertSettingsVisibleBounds(t, shell, name, width, 1000)
							}
							captureDesktopTestFrame(t, shell, fmt.Sprintf("settings-help-shell-paths-%d-dark-%t", width, dark))
						}
						if backend.count.Load() != 0 || len(tt.OpenedURLs()) != 0 {
							t.Fatal("rendering settings started an external operation")
						}
					})
				}
			}
		}
	}
}

func TestSettingsHelpLongTooltipFitsAndCommonMutedStaysVisible(t *testing.T) {
	help := strings.Repeat("目录展开说明：支持中文、长路径和多行内容。", 8)
	for _, width := range []int{360, 960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				tt := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, dark)
					ui.Column(c).Fill().Padding(16).Justify(ui.End).Children(func() {
						settingsMuted(c, p, "仍可读取的实际内容")
						settingsHelpLabel(c, p, "目录展开", help, 12, 600)
					})
				}, width, 600)
				tt.Key(0, ui.KeyTab)
				assertSettingsVisibleBounds(t, tt, help, width, 600)
				if !tt.HasText("仍可读取的实际内容") {
					t.Fatal("the common muted-content renderer changed")
				}
				captureDesktopTestFrame(t, tt, fmt.Sprintf("settings-help-tooltip-%d-dark-%t", width, dark))
			})
		}
	}
}
