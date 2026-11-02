package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSettingsRegroupDeferredCLIHeightKeepsSearchTargetUsable(t *testing.T) {
	s := newApp(nil, Options{Page: "settings", CapturePath: "settings-regroup-test"})
	s.loading = false
	s.snapshot.Home = t.TempDir()
	s.settingsUnit.state.configJSON = `{}`
	s.openSettingsSearchResult(settingsSearchEntry{"cli-genbu", "Genbu CLI 状态", ""})
	s.busy = true
	v := ui.NewTester(func(c *ui.Context) { s.settingsScreen(c, setTheme(c, false)) }, 1168, 854)
	before, _ := v.Find("Genbu CLI 状态")
	s.settingsUnit.state.settingsCLIErrors = map[string]string{"genbu": strings.Repeat("synthetic delayed error ", 20)}
	s.busy = false
	v.Frame()
	v.Frame()
	v.Frame()
	for _, name := range []string{"Genbu CLI 状态", "Genbu CLI 可执行文件"} {
		r, ok := v.Find(name)
		t.Log("before", before, "after", name, r)
		if !ok || r.H <= 0 || r.Y < 100 || r.Y+r.H > 774 {
			t.Fatal("late data left target outside usable viewport", name, r)
		}
	}
}

func TestSettingsRegroupRootDraftBlocksNavigationUntilChoice(t *testing.T) {
	s := newApp(nil, Options{Page: "settings", CapturePath: "settings-regroup-test"})
	s.loading = false
	s.settingsUnit.state.settingsPage = "task-paths"
	s.settingsUnit.state.settingsTab = "任务与工作区"
	s.settingsUnit.state.configJSON = `{"taskRoot":"C:/original"}`
	*s.input("setting:taskRootTarget", "") = "C:/synthetic-other"
	if s.navigateSettingsPage("paths", "") {
		t.Fatal("navigation hid pending root change before a choice")
	}
	if s.settingsUnit.state.settingsPage != "task-paths" || s.configString("taskRoot") != "C:/original" {
		t.Fatal("pending root draft changed page or config")
	}
	*s.input("setting:taskRootTarget", "") = "C:/original"
	if !s.navigateSettingsPage("paths", "") {
		t.Fatal("unchanged root prevented normal navigation")
	}
}

func TestSettingsRegroupPendingRevealYieldsToManualScroll(t *testing.T) {
	s := newApp(nil, Options{Page: "settings", CapturePath: "settings-regroup-test"})
	s.loading = false
	s.snapshot.Home = t.TempDir()
	s.settingsUnit.state.configJSON = `{}`
	s.openSettingsSearchResult(settingsSearchEntry{"cli-genbu", "Genbu CLI 状态", ""})
	s.busy = true
	v := ui.NewTester(func(c *ui.Context) { s.settingsScreen(c, setTheme(c, false)) }, 1168, 854)
	if !s.settingsUnit.state.settingsSearchPending {
		t.Fatal("reveal consumed before data was ready")
	}
	v.Scroll(700, 400, 0, 160)
	if s.settingsUnit.state.settingsSearchPending {
		t.Fatal("manual scroll did not cancel delayed reveal")
	}
	s.busy = false
	v.Frame()
	v.Frame()
	if s.settingsUnit.state.settingsSearchPending {
		t.Fatal("late update revived cancelled reveal")
	}
}

func TestSettingsRegroupPendingRevealYieldsToKeyboardContent(t *testing.T) {
	s := newApp(nil, Options{Page: "settings", CapturePath: "settings-regroup-test"})
	s.loading = false
	s.snapshot.Home = t.TempDir()
	s.settingsUnit.state.configJSON = `{}`
	s.openSettingsSearchResult(settingsSearchEntry{"cli-genbu", "Genbu CLI 状态", ""})
	s.toolsUnit.state.cliJobs = &cliJobState{requests: map[string]*cliJobRequest{"status:genbu": {tool: "genbu"}}}
	v := ui.NewTester(func(c *ui.Context) { s.settingsScreen(c, setTheme(c, false)) }, 1168, 854)
	found := false
	for range 80 {
		v.Key(0, ui.KeyTab)
		active, _ := v.TextInputActive()
		if active && !v.Focused("搜索设置") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("no editable content field reached by keyboard")
	}
	v.Type("cu-keyboard-draft")
	if s.settingsUnit.state.settingsSearchPending {
		t.Fatal("keyboard content edit kept delayed search reveal active")
	}
	delete(s.toolsUnit.state.cliJobs.requests, "status:genbu")
	v.Frame()
	v.Frame()
	if s.settingsUnit.state.settingsSearchPending {
		t.Fatal("late CLI result revived cancelled keyboard navigation")
	}
}

func TestSettingsRegroupMaterialsEntryPoints(t *testing.T) {
	t.Run("empty-state", func(t *testing.T) {
		s := newApp(nil, Options{Page: "tasks", CapturePath: "settings-regroup-test"})
		s.loading = false
		s.settingsUnit.state.configJSON = `{}`
		v := ui.NewTester(func(c *ui.Context) {
			s.materialsUnavailable(c, setTheme(c, false), task{requirementURL: "https://project.feishu.cn/test/userstory/1"})
		}, 1000, 700)
		if err := v.Click("配置任务资料"); err != nil {
			t.Fatal(err)
		}
		if s.settingsUnit.state.settingsPage != "paths" || s.settingsUnit.state.settingsTab != "资料与说明" {
			t.Fatal("materials entry went to task root", s.settingsUnit.state.settingsPage)
		}
	})
	t.Run("task-menu", func(t *testing.T) {
		s := taskNavigationFixture()
		v := taskNavigationTester(s, 240, 500)
		navigationChooseTaskMenu(t, v, "界面体验", "配置任务资料")
		if s.settingsUnit.state.settingsPage != "paths" || s.settingsUnit.state.settingsTab != "资料与说明" {
			t.Fatal("task menu went to task root", s.settingsUnit.state.settingsPage)
		}
	})
}

func TestSettingsRegroupExactVisibleNamesAndReveal(t *testing.T) {
	for query, page := range map[string]string{"全局默认开发工具": "dev-tools", "允许临时选择开发工具": "dev-tools", "代理认证": "proxy", "同步周期": "materials-sync"} {
		t.Run(query, func(t *testing.T) {
			matches := settingsSearch(query)
			if len(matches) == 0 || matches[0].page != page {
				t.Fatalf("visible name not indexed: %s, %v", query, matches)
			}
			s := newApp(nil, Options{Page: "settings", CapturePath: "settings-regroup-test"})
			s.loading = false
			s.snapshot.Home = t.TempDir()
			s.settingsUnit.state.configJSON = `{}`
			s.openSettingsSearchResult(matches[0])
			v := ui.NewTester(func(c *ui.Context) { s.settingsScreen(c, setTheme(c, false)) }, 1100, 760)
			v.Frame()
			if s.settingsUnit.state.settingsSearchPending {
				t.Fatal("indexed setting has no real reveal target")
			}
			bounds, ok := v.Find(matches[0].title)
			if !ok || bounds.H <= 0 || bounds.Y < 0 || bounds.Y+bounds.H > 760 {
				t.Fatal("target not visible", bounds)
			}
		})
	}
}

func TestSettingsRegroupTaskRootFocusFollowsActualOwner(t *testing.T) {
	for _, page := range []string{"task-paths", "paths", "basic"} {
		t.Run(page, func(t *testing.T) {
			s := newApp(nil, Options{Page: "settings", CapturePath: "settings-regroup-test"})
			s.loading = false
			s.settingsUnit.state.settingsPage = page
			s.settingsUnit.state.configJSON = `{"taskRoot":"C:/original"}`
			*s.input("setting:taskRootTarget", "") = "C:/synthetic-other"
			s.taskUnit.state.taskRootTargetFocused = true
			s.observeTaskRootMigrationNavigation()
			if s.taskUnit.state.taskRootTargetFocused != (page == "task-paths") {
				t.Fatal("hidden task-root field kept its focus", page)
			}
			if s.configString("taskRoot") != "C:/original" {
				t.Fatal("navigation wrote task root")
			}
		})
	}
}

func TestSettingsRegroupCLIRevealIncludesEditableCard(t *testing.T) {
	s := newApp(nil, Options{Page: "settings", CapturePath: "settings-regroup-test"})
	s.loading = false
	s.settingsUnit.state.configJSON = `{}`
	s.openSettingsSearchResult(settingsSearchEntry{"cli-genbu", "Genbu CLI 状态", ""})
	v := ui.NewTester(func(c *ui.Context) { s.settingsScreen(c, setTheme(c, false)) }, 1100, 760)
	v.Frame()
	for _, name := range []string{"Genbu CLI 状态", "Genbu CLI 可执行文件"} {
		r, ok := v.Find(name)
		if !ok || r.H <= 0 || r.Y < 0 || r.Y+r.H > 760 {
			t.Fatal("CLI target card clipped", name, r)
		}
	}
}

func TestSettingsRegroupDefaultSprintSpaceSearch(t *testing.T) {
	for _, scenario := range []struct{ name, config, choice string }{
		{"empty-projects", `{"meegleProjects":[]}`, "未指定"},
		{"configured-project", `{"meegleProjects":[{"project_key":"fixture-space","simple_name":"fixture"}],"meegleDefaultSprintProjectKey":"fixture-space"}`, "fixture · fixture-space"},
	} {
		for _, query := range []string{"默认 Sprint 团队空间", "默认Sprint团队空间"} {
			t.Run(scenario.name+"/"+query, func(t *testing.T) {
				matches := settingsSearch(query)
				if len(matches) == 0 || matches[0].page != "cli-meegle" || matches[0].title != "默认 Sprint 团队空间" {
					t.Fatalf("visible Sprint setting is not searchable: %q, %v", query, matches)
				}
				s := newApp(nil, Options{Page: "settings", CapturePath: "settings-regroup-test"})
				s.loading = false
				s.snapshot.Home = t.TempDir()
				s.settingsUnit.state.configJSON = scenario.config
				s.openSettingsSearchResult(matches[0])
				v := ui.NewTester(func(c *ui.Context) { s.settingsScreen(c, setTheme(c, false)) }, 1100, 760)
				for range 4 {
					v.Frame()
				}
				if s.settingsUnit.state.settingsPage != "cli-tools" || s.currentSettingsCLITool() != "meegle" || s.settingsUnit.state.settingsSearchPending {
					t.Fatal("search did not finish navigation and real-control reveal")
				}
				for _, name := range []string{matches[0].title, scenario.choice} {
					bounds, ok := v.Find(name)
					if !ok || bounds.H <= 0 || bounds.Y < 100 || bounds.Y+bounds.H > 760 {
						t.Fatal("Sprint setting title or actual picker is outside viewport", name, bounds)
					}
				}
			})
		}
	}
}
