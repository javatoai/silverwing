package desktop

import (
	"github.com/egoist/mygo/ui"
	"path/filepath"
	"testing"
)

func settingsSidebarTester(state *app, height int) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, state.dark)
		ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() { state.renderSettingsSidebar(c, p) })
	}, 220, height)
}

func TestSettingsSidebarSectionsAndVisualGroups(t *testing.T) {
	want := []string{"外观与交互", "任务与工作区", "资料与说明", "Git 与发布", "AI 与扩展", "工具与连接", "数据与维护"}
	if len(settingsSections) != len(want) {
		t.Fatalf("categories=%d", len(settingsSections))
	}
	seen := map[string]bool{}
	for i, section := range settingsSections {
		if section.name != want[i] {
			t.Fatalf("category %d=%s", i, section.name)
		}
		for _, page := range section.pages {
			if seen[page.key] {
				t.Fatalf("duplicate editable route %s", page.key)
			}
			seen[page.key] = true
		}
	}
	if len(seen) != 23 {
		t.Fatalf("pages=%d", len(seen))
	}
	for _, old := range []string{"basic", "tool-visibility", "paths", "config-backup", "dev-tools", "task-creation", "task-area", "git", "branch-naming", "tag", "agents", "instruction-templates", "codex-plugins", "codex-mcp", "skill-local", "skill-sources", "cli-meegle", "cli-genbu", "proxy", "logs", "about", "updates", "app-data"} {
		if _, ok := settingsSectionForPage(old); !ok {
			t.Fatalf("lost existing route %s", old)
		}
	}
}

func TestSettingsSidebarPointerAndKeyboardNavigation(t *testing.T) {
	s := &app{settingsUnit: settingsController{state: settingsState{settingsTab: "外观与交互", settingsPage: "basic"}}}
	view := settingsSidebarTester(s, 820)
	for _, section := range settingsSections {
		if err := view.Click(section.name); err != nil {
			t.Fatal(err)
		}
		if s.settingsUnit.state.settingsTab != section.name || s.settingsUnit.state.settingsPage != section.pages[0].key {
			t.Fatal("wrong category target", section.name)
		}
	}
	s = &app{settingsUnit: settingsController{state: settingsState{settingsTab: "外观与交互", settingsPage: "basic"}}}
	view = settingsSidebarTester(s, 820)
	for _, section := range settingsSections {
		view.Key(0, ui.KeyTab)
		if !view.Focused(section.name) {
			t.Fatal("keyboard category unreachable", section.name)
		}
	}
	if !s.navigateSettingsPage("instruction-templates", "") {
		t.Fatal("navigate")
	}
	s.navigateSettingsPage("git", "")
	view.Frame()
	if err := view.Click("资料与说明"); err != nil {
		t.Fatal(err)
	}
	if s.settingsUnit.state.settingsPage != "instruction-templates" {
		t.Fatal("category did not remember its last subpage")
	}
}

func TestSettingsSidebarScrollsAtShortHeight(t *testing.T) {
	s := &app{settingsUnit: settingsController{state: settingsState{settingsTab: "外观与交互", settingsPage: "basic"}}}
	v := settingsSidebarTester(s, 190)
	v.Scroll(80, 150, 0, 900)
	if err := v.Click("数据与维护"); err != nil {
		t.Fatal(err)
	}
	if s.settingsUnit.state.settingsPage != "config-backup" {
		t.Fatal("last category unreachable")
	}
}

func TestSettingsPreferenceAliasesAndRestore(t *testing.T) {
	cases := map[string]string{"basic": "外观与交互", "developer-tools": "外观与交互", "tools": "外观与交互", "storage": "资料与说明", "agents": "资料与说明", "development": "工具与连接", "commands": "工具与连接", "proxy": "工具与连接", "skill-sources": "AI 与扩展", "codex": "AI 与扩展", "system": "数据与维护", "app-data": "数据与维护", "logs": "数据与维护", "branch-naming": "任务与工作区", "tasks": "任务与工作区", "git": "Git 与发布"}
	for key, name := range cases {
		if got := settingsPreferenceName(key); got != name {
			t.Fatalf("%s=%s want %s", key, got, name)
		}
	}
	for _, section := range settingsSections {
		if got := settingsPreferenceName(settingsPreferenceKey(section.name)); got != section.name {
			t.Fatal("category preference roundtrip", section.name, got)
		}
	}
	home := t.TempDir()
	store := NewDesktopPreferencesStore(filepath.Join(home, desktopPreferencesFile))
	if _, err := store.Update(func(p *DesktopPreferences) { p.SettingsSection = "system" }); err != nil {
		t.Fatal(err)
	}
	s := &app{desktopPreferences: &desktopPreferencesState{enabled: true}}
	s.restoreDesktopPreferences(home)
	if s.settingsUnit.state.settingsTab != "数据与维护" || s.settingsUnit.state.settingsPage != "config-backup" {
		t.Fatal("restore lost maintenance category")
	}
}

func TestSettingsSearchAliasesAndTargetNavigation(t *testing.T) {
	for query, page := range map[string]string{"资料同步": "materials-sync", "AGENTS": "agents", "Meego": "cli-meegle", "配置源码": "config-backup", "端口占用": "tool-visibility", "分支名": "task-creation", "MCP": "codex-mcp"} {
		matches := settingsSearch(query)
		if len(matches) == 0 || matches[0].page != page {
			t.Fatalf("%s=%v", query, matches)
		}
	}
	if len(settingsSearch("not-a-real-setting-58398")) != 0 {
		t.Fatal("unknown query matched")
	}
	s := newApp(nil, Options{Page: "settings", CapturePath: "settings-test"})
	s.loading = false
	s.settingsUnit.state.configJSON = `{}`
	s.settingsUnit.state.settingsSearchQuery = "资料同步"
	v := ui.NewTester(func(c *ui.Context) { s.settingsScreen(c, setTheme(c, false)) }, 1080, 800)
	if _, ok := v.Find("资料与说明 · 资料 Git 自动同步"); ok {
		t.Fatal("removed settings-search results returned")
	}
	// Aliases remain available to internal links, without a visible search UI.
	s.openSettingsSearchResult(settingsSearch("资料同步")[0])
	if s.settingsUnit.state.settingsPage != "materials-sync" || s.settingsUnit.state.settingsSearchQuery != "" {
		t.Fatal("search did not route")
	}

	v.Frame()
	if s.settingsUnit.state.settingsSearchPending {
		t.Fatal("search target not revealed")
	}
	if len(v.OpenedURLs()) != 0 {
		t.Fatal("navigation opened external URL")
	}
}

func TestSettingsCommandToolsAndExpandedProxy(t *testing.T) {
	s := newApp(nil, Options{Page: "settings", CapturePath: "settings-test"})
	s.settingsUnit.state.configJSON, s.settingsUnit.state.settingsPage = `{}`, "cli-tools"
	s.settingsUnit.state.settingsCLIErrors = map[string]string{"codex": "synthetic-codex-error"}
	v := ui.NewTester(func(c *ui.Context) {
		ui.Scroll(c).Fill().Children(func() { s.settingsCommandTools(c, setTheme(c, false)) })
	}, 1100, 2200)
	if !v.HasText("Git 状态") || v.HasText("Codex CLI 状态") || v.HasText("Meegle CLI 状态") || v.HasText("Genbu CLI 状态") || v.HasText("synthetic-codex-error") {
		t.Fatal("default Git tab leaked another command")
	}
	s.navigateSettingsPage("cli-codex", "")
	v.Frame()
	if !v.HasText("Codex CLI 状态") || v.HasText("Git 状态") || v.HasText("Meegle CLI 状态") || v.HasText("Genbu CLI 状态") {
		t.Fatal("selected Codex tab did not isolate its command")
	}
	errors := 0
	for _, text := range v.Texts() {
		if text == "synthetic-codex-error" {
			errors++
		}
	}
	if errors != 1 {
		t.Fatal("CLI failure leaked across tools", errors)
	}
	v = ui.NewTester(func(c *ui.Context) { s.settingsProxy(c, setTheme(c, false)) }, 1100, 1000)
	if _, ok := v.Find("代理密码"); !ok {
		t.Fatal("disabled auth fields hidden instead of expanded")
	}
}
