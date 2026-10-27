package desktop

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func settingsSidebarTester(state *app, height int) *ui.Tester {
	return ui.NewTester(func(context *ui.Context) {
		theme := setTheme(context, state.dark)
		ui.Row(context).Fill().AlignItems(ui.Stretch).Children(func() {
			state.renderSettingsSidebar(context, theme)
		})
	}, 220, height)
}

func TestSettingsSidebarSectionsAndVisualGroups(test *testing.T) {
	expected := []string{"基础", "快捷工具", "存储", "项目组", "任务", "AGENTS.md", "Codex", "skill", "CLI", "代理", "日志", "系统"}
	expectedGroups := []int{0, 0, 0, 1, 1, 2, 2, 2, 2, 2, 3, 3}
	if len(settingsSections) != len(expected) {
		test.Fatalf("sidebar has %d categories, want %d", len(settingsSections), len(expected))
	}
	groupStarts := 0
	for index, section := range settingsSections {
		if section.name != expected[index] {
			test.Fatalf("category %d is %q, want %q", index, section.name, expected[index])
		}
		if section.group != expectedGroups[index] {
			test.Fatalf("category %q belongs to group %d, want %d", section.name, section.group, expectedGroups[index])
		}
		if index > 0 && section.group != settingsSections[index-1].group {
			groupStarts++
		}
	}
	if groupStarts != 3 {
		test.Fatalf("sidebar has %d visual group boundaries, want 3", groupStarts)
	}
	if settingsSections[1].pages[0].key != "tool-visibility" || settingsSections[1].pages[0].label != "实用工具" {
		test.Fatal("renaming the settings category changed its existing page identity")
	}

	state := &app{settingsTab: "基础", settingsPage: "basic"}
	tester := settingsSidebarTester(state, 820)
	dividerCount := 0
	for _, text := range tester.Texts() {
		if text == "设置分组分隔线" {
			dividerCount++
		}
	}
	if dividerCount != 3 {
		test.Fatalf("rendered sidebar has %d separators, want 3; texts=%q", dividerCount, tester.Texts())
	}
	line, lineFound := tester.Find("设置分组分隔线")
	previous, previousFound := tester.Find("存储")
	following, followingFound := tester.Find("项目组")
	if !lineFound || !previousFound || !followingFound || line.H != 1 || line.Y-previous.Y-previous.H != 8 || following.Y-line.Y-line.H != 8 || line.X != previous.X+14 {
		test.Fatalf("first separator must be 1 DIP, aligned with menu text, with 8 DIP breathing room: previous=%+v line=%+v following=%+v", previous, line, following)
	}
	if tester.Focused("设置分组分隔线") {
		test.Fatal("visual category separators must not take keyboard focus")
	}
}

func TestSettingsSidebarPointerAndKeyboardNavigation(test *testing.T) {
	expected := []struct {
		name string
		page string
	}{
		{"基础", "basic"},
		{"快捷工具", "tool-visibility"},
		{"存储", "paths"},
		{"项目组", "groups"},
		{"任务", "task-creation"},
		{"AGENTS.md", "agents"},
		{"Codex", "codex-plugins"},
		{"skill", "skill-local"},
		{"CLI", "cli-meegle"},
		{"代理", "proxy"},
		{"日志", "logs"},
		{"系统", "about"},
	}
	state := &app{settingsTab: "基础", settingsPage: "basic"}
	tester := settingsSidebarTester(state, 820)
	for _, item := range expected {
		if err := tester.Click(item.name); err != nil {
			test.Fatal(err)
		}
		if state.settingsTab != item.name || state.settingsPage != item.page {
			test.Fatalf("clicking %q selected %q / %q, want %q / %q", item.name, state.settingsTab, state.settingsPage, item.name, item.page)
		}
	}

	state = &app{settingsTab: "基础", settingsPage: "basic"}
	tester = settingsSidebarTester(state, 820)
	for index, item := range expected {
		tester.Key(0, ui.KeyTab)
		if !tester.Focused(item.name) {
			test.Fatalf("Tab %d focused another control instead of %q", index+1, item.name)
		}
		if tester.Focused("设置分组分隔线") {
			test.Fatal("a visual separator entered the keyboard focus order")
		}
	}
}

func TestSettingsSidebarScrollsAtShortHeight(test *testing.T) {
	state := &app{settingsTab: "基础", settingsPage: "basic"}
	tester := settingsSidebarTester(state, 190)
	last, found := tester.Find("系统")
	if found && last.H > 0 {
		test.Fatal("the last category should require scrolling in a short sidebar")
	}
	tester.Scroll(80, 150, 0, 900)
	last, found = tester.Find("系统")
	if !found || last.H == 0 {
		test.Fatalf("scrolling did not expose the last settings category: rect=%+v found=%t", last, found)
	}
	if err := tester.Click("系统"); err != nil {
		test.Fatal(err)
	}
	if state.settingsTab != "系统" || state.settingsPage != "about" {
		test.Fatalf("last category is not reachable after scrolling: tab=%q page=%q", state.settingsTab, state.settingsPage)
	}
}

func TestSettingsSystemPagesAndAppDataRoute(test *testing.T) {
	expected := []settingsPage{{"about", "关于"}, {"updates", "更新"}, {"app-data", "应用数据目录"}}
	system := settingsSections[len(settingsSections)-1]
	if system.name != "系统" || len(system.pages) != len(expected) {
		test.Fatalf("system settings contain %d pages, want %d", len(system.pages), len(expected))
	}
	for index, page := range expected {
		if system.pages[index] != page {
			test.Fatalf("system page %d is %+v, want %+v", index, system.pages[index], page)
		}
	}

	state := &app{settingsTab: "系统", settingsPage: "app-data"}
	section := state.currentSettingsSection()
	if state.settingsPage != "app-data" || section.name != "系统" {
		test.Fatalf("app-data route was not preserved in the system category: page=%q section=%q", state.settingsPage, section.name)
	}

	state = &app{settingsTab: "快捷工具", settingsPage: "tool-visibility"}
	tester := settingsSidebarTester(state, 820)
	if err := tester.Click("系统"); err != nil {
		test.Fatal(err)
	}
	if state.settingsTab != "系统" || state.settingsPage != "about" {
		test.Fatalf("selecting the system category must open About first: %q / %q", state.settingsTab, state.settingsPage)
	}
}

func TestSettingsPreferenceAliasesAndRestore(test *testing.T) {
	for _, item := range []struct {
		name string
		key  string
	}{
		{"快捷工具", "developer-tools"},
		{"工具", "developer-tools"},
		{"存储", "storage"},
		{"项目组", "groups"},
		{"系统", "system"},
	} {
		if key := settingsPreferenceKey(item.name); key != item.key {
			test.Fatalf("preference key for %q is %q, want %q", item.name, key, item.key)
		}
	}
	for _, item := range []struct {
		key       string
		name      string
		roundTrip string
	}{
		{"developer-tools", "快捷工具", "developer-tools"},
		{"tools", "快捷工具", "developer-tools"},
		{"storage", "存储", "storage"},
		{"groups", "项目组", "groups"},
		{"app-data", "系统", "system"},
		{"system", "系统", "system"},
		{"about", "系统", "system"},
		{"updates", "系统", "system"},
	} {
		if name := settingsPreferenceName(item.key); name != item.name {
			test.Fatalf("preference name for %q is %q, want %q", item.key, name, item.name)
		}
		if key := settingsPreferenceKey(settingsPreferenceName(item.key)); key != item.roundTrip {
			test.Fatalf("preference %q did not round-trip to %q: %q", item.key, item.roundTrip, key)
		}
	}

	for _, legacyKey := range []string{"developer-tools", "tools"} {
		test.Run(legacyKey, func(test *testing.T) {
			home := test.TempDir()
			store := NewDesktopPreferencesStore(filepath.Join(home, desktopPreferencesFile))
			if _, err := store.Update(func(value *DesktopPreferences) { value.SettingsSection = legacyKey }); err != nil {
				test.Fatal(err)
			}
			state := &app{desktopPreferences: &desktopPreferencesState{enabled: true}}
			state.restoreDesktopPreferences(home)
			if state.settingsTab != "快捷工具" || state.settingsPage != "tool-visibility" {
				test.Fatalf("restoring %q selected %q / %q", legacyKey, state.settingsTab, state.settingsPage)
			}
		})
	}

	home := test.TempDir()
	state := &app{desktopPreferences: &desktopPreferencesState{enabled: true}}
	state.restoreDesktopPreferences(home)
	if state.settingsTab != "基础" || state.settingsPage != "basic" {
		test.Fatalf("missing preference no longer restores the basic page: %q / %q", state.settingsTab, state.settingsPage)
	}

	home = test.TempDir()
	store := NewDesktopPreferencesStore(filepath.Join(home, desktopPreferencesFile))
	state = &app{settingsTab: "系统", settingsPage: "app-data", desktopPreferences: &desktopPreferencesState{enabled: true, store: store}}
	if err := state.persistDesktopLayout(); err != nil {
		test.Fatal(err)
	}
	persisted, err := store.Load()
	if err != nil || persisted.SettingsSection != "system" {
		test.Fatalf("system page selection did not persist the system category: %+v %v", persisted, err)
	}
	restored := &app{desktopPreferences: &desktopPreferencesState{enabled: true}}
	restored.restoreDesktopPreferences(home)
	if restored.settingsTab != "系统" || restored.settingsPage != "about" {
		test.Fatalf("restoring the system category must open About without remembering a subpage: %q / %q", restored.settingsTab, restored.settingsPage)
	}
}

func TestReconcileSidebarNavigationUsesShortcutToolsCategory(test *testing.T) {
	state := &app{page: "tools", settingsTab: "基础", settingsPage: "basic", configJSON: `{"toolVisibility":{"json":false,"dateTime":false,"textDiff":false,"killPort":false}}`}
	state.reconcileSidebarNavigation()
	if state.page != "settings" || state.settingsTab != "快捷工具" || state.settingsPage != "tool-visibility" {
		test.Fatalf("all-disabled tools fallback selected %q / %q / %q", state.page, state.settingsTab, state.settingsPage)
	}

	state = &app{dark: false, configJSON: `{"toolVisibility":{"json":false,"dateTime":false,"textDiff":false,"killPort":false}}`}
	tester := ui.NewTester(func(context *ui.Context) { state.renderDeveloperTools(context) }, 640, 400)
	if !tester.HasText("设置 → 快捷工具") || tester.HasText("设置 → 工具") {
		test.Fatalf("empty-state guidance is stale: %q", strings.Join(tester.Texts(), " | "))
	}
}
