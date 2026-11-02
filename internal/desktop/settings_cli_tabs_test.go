package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

func newSettingsCLITabsTestApp(t *testing.T) *app {
	t.Helper()
	s := newApp(nil, Options{Page: "settings", CapturePath: "cli-tabs-test"})
	s.loading = false
	s.snapshot.Home = t.TempDir()
	s.settingsUnit.state.configJSON = `{}`
	s.navigateSettingsPage("cli-tools", "")
	t.Cleanup(s.settingsUnit.stopSettingsAutosave)
	return s
}

func TestSettingsCLITabsIsolateAndRemember(t *testing.T) {
	for _, width := range []int{960, 1400} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/dark=%t", width, dark), func(t *testing.T) {
				s := newSettingsCLITabsTestApp(t)
				s.dark = dark
				v := ui.NewTester(func(c *ui.Context) { s.settingsScreen(c, setTheme(c, dark)) }, width, 854)
				if s.currentSettingsCLITool() != "git" {
					t.Fatal("first entry did not default to Git")
				}
				for _, item := range settingsCLITabs {
					if err := v.Click(item.label); err != nil {
						t.Fatal(err)
					}
					v.Frame()
					if s.currentSettingsCLITool() != item.tool {
						t.Fatal("native tab did not select command", item)
					}
					for _, other := range settingsCLITabs {
						if v.HasText(settingsCLITitle(other.tool)+" 状态") != (other.tool == item.tool) {
							t.Fatal("inactive command rendered in selected tab", item, other)
						}
					}
					if v.HasText("Meegle 项目空间") != (item.tool == "meegle") {
						t.Fatal("project settings escaped the Meegle tab")
					}
				}
				if err := v.Click("Git"); err != nil {
					t.Fatal(err)
				}
				v.Key(0, ui.KeyRight)
				if s.currentSettingsCLITool() != "codex" || !v.Focused("Codex") {
					t.Fatal("Right did not select and focus next CLI")
				}
				v.Key(0, ui.KeyEnd)
				if s.currentSettingsCLITool() != "genbu" {
					t.Fatal("End did not choose last CLI")
				}
				s.navigateSettingsPage("dev-tools", "")
				v.Frame()
				s.navigateSettingsPage("cli-tools", "")
				v.Frame()
				if s.currentSettingsCLITool() != "genbu" || !v.HasText("Genbu CLI 状态") {
					t.Fatal("returning to CLI page forgot current-session selection")
				}
			})
		}
	}
}

func TestSettingsCLITabsKeepDraftAndScroll(t *testing.T) {
	s := newSettingsCLITabsTestApp(t)
	s.navigateSettingsPage("cli-genbu", "")
	v := ui.NewTester(func(c *ui.Context) { s.settingsScreen(c, setTheme(c, false)) }, 1100, 760)
	found := false
	for range 40 {
		v.Key(0, ui.KeyTab)
		if active, _ := v.TextInputActive(); active && !v.Focused("搜索设置") {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("could not focus the visible CLI path input")
	}
	v.Type("fixture-unsaved-cli")
	entry := s.settingsUnit.state.settingsAutosave.entries["path:Genbu CLI 可执行文件"]
	if entry == nil || !entry.pending || *s.input("setting:genbuExecutablePath", "") != "fixture-unsaved-cli" {
		t.Fatal("typing did not enter normal CLI autosave queue")
	}
	if err := v.Click("Meegle"); err != nil {
		t.Fatal(err)
	}
	if !entry.pending || *s.input("setting:genbuExecutablePath", "") != "fixture-unsaved-cli" {
		t.Fatal("switching CLI dropped the pending path draft")
	}
	projects := []map[string]any{}
	for i := range 30 {
		projects = append(projects, map[string]any{"project_key": fmt.Sprint(i), "simple_name": fmt.Sprintf("fixture-project-%02d", i)})
	}
	config, _ := json.Marshal(map[string]any{"meegleProjects": projects})
	s.settingsUnit.state.configJSON = string(config)
	v.Frame()
	before, _ := v.Find("Meegle")
	v.Scroll(800, 620, 0, 500)
	meegleScroll := s.settingsUnit.state.settingsContentScrolls[s.settingsContentKey()]
	position := meegleScroll.Y
	if position <= 0 {
		t.Fatal("long Meegle content did not scroll")
	}
	after, _ := v.Find("Meegle")
	if before != after {
		t.Fatal("CLI tabs scrolled with the content")
	}
	if err := v.Click("Genbu"); err != nil {
		t.Fatal(err)
	}
	genbuScroll := s.settingsUnit.state.settingsContentScrolls[s.settingsContentKey()]
	if genbuScroll == meegleScroll || genbuScroll.Y != 0 || *s.input("setting:genbuExecutablePath", "") != "fixture-unsaved-cli" || !entry.pending {
		t.Fatal("CLI tabs shared scroll state or discarded pending input")
	}
	if err := v.Click("Meegle"); err != nil {
		t.Fatal(err)
	}
	if s.settingsUnit.state.settingsContentScrolls[s.settingsContentKey()] != meegleScroll || meegleScroll.Y != position {
		t.Fatal("returning to Meegle lost its reading position")
	}
}

func TestSettingsCLITabsSearchRoutesAndDependencies(t *testing.T) {
	for _, tool := range []string{"git", "codex", "meegle", "genbu"} {
		t.Run(tool, func(t *testing.T) {
			s := newSettingsCLITabsTestApp(t)
			query := settingsCLITitle(tool) + " 可执行文件"
			matches := settingsSearch(query)
			if len(matches) == 0 {
				t.Fatal("missing CLI path search result", query)
			}
			s.openSettingsSearchResult(matches[0])
			v := ui.NewTester(func(c *ui.Context) { s.settingsScreen(c, setTheme(c, false)) }, 1100, 760)
			v.Frame()
			if s.currentSettingsCLITool() != tool || s.settingsUnit.state.settingsSearchPending || !v.HasText(query) {
				t.Fatal("search did not select/reveal correct CLI", tool)
			}
			s.navigateSettingsPage("basic", "")
			v = ui.NewTester(func(c *ui.Context) { s.settingsCLIDependency(c, setTheme(c, false), tool, "fixture dependency") }, 800, 400)
			if err := v.Click("配置 " + settingsCLITitle(tool)); err != nil {
				t.Fatal(err)
			}
			if s.settingsUnit.state.settingsPage != "cli-tools" || s.currentSettingsCLITool() != tool {
				t.Fatal("dependency link went to a different command")
			}
		})
	}
}

func TestSettingsCLITabsLoadAndRevealIgnoreInactiveJobs(t *testing.T) {
	s := newSettingsCLITabsTestApp(t)
	s.navigateSettingsPage("cli-genbu", "")
	s.toolsUnit.state.cliJobs = &cliJobState{requests: map[string]*cliJobRequest{"status:meegle": {tool: "meegle"}}}
	s.openSettingsSearchResult(settingsSearchEntry{"cli-genbu", "Genbu CLI 状态", ""})
	v := ui.NewTester(func(c *ui.Context) { s.settingsScreen(c, setTheme(c, false)) }, 1100, 760)
	v.Frame()
	if s.settingsUnit.state.settingsSearchPending {
		t.Fatal("inactive CLI job blocked current search reveal")
	}
	loaded := s.settingsUnit.state.settingsLoaded
	s.toolsUnit.state.cliJobs.requests["status:genbu"] = &cliJobRequest{tool: "genbu"}
	s.openSettingsSearchResult(settingsSearchEntry{"cli-genbu", "Genbu CLI 状态", ""})
	v.Frame()
	if !s.settingsUnit.state.settingsSearchPending {
		t.Fatal("current CLI data was not awaited")
	}
	if err := v.Click("Meegle"); err != nil {
		t.Fatal(err)
	}
	if s.settingsUnit.state.settingsSearchPending || s.settingsUnit.state.settingsLoaded == loaded {
		t.Fatal("manual CLI switch retained stale reveal or load marker")
	}
	delete(s.toolsUnit.state.cliJobs.requests, "status:genbu")
	v.Frame()
	if s.currentSettingsCLITool() != "meegle" || s.settingsUnit.state.settingsSearchPending {
		t.Fatal("late previous-tab status revived its search")
	}
}

func TestSettingsCLITabsLateStatusCannotReplaceVisibleFeedback(t *testing.T) {
	s := newSettingsCLITabsTestApp(t)
	s.navigateSettingsPage("cli-meegle", "")
	old, err := s.beginCLIJob(application.Action{Kind: "cli.status", Payload: map[string]any{"tool": "meegle"}})
	if err != nil {
		t.Fatal(err)
	}
	s.navigateSettingsPage("cli-genbu", "")
	current, err := s.beginCLIJob(application.Action{Kind: "cli.status", Payload: map[string]any{"tool": "genbu"}})
	if err != nil {
		t.Fatal(err)
	}
	s.settingsUnit.state.settingsOutput, s.lastError = "current Genbu feedback", ""
	old.complete(nil)
	s.finishCLIJob(old, application.ActionResult{}, application.Snapshot{}, errors.New("old Meegle failure"))
	if s.settingsUnit.state.settingsOutput != "current Genbu feedback" || s.lastError != "" || s.settingsUnit.state.settingsCLIErrors["meegle"] != "old Meegle failure" || current.owner.requests[current.key] != current {
		t.Fatal("late inactive CLI error replaced visible feedback or another request")
	}
	current.complete(nil)
	s.finishCLIJob(current, application.ActionResult{Data: map[string]any{"installed": true}}, application.Snapshot{}, nil)
	if !dataBool(s.settingsUnit.state.settingsCLIStatus["genbu"], "installed") {
		t.Fatal("current CLI success was discarded")
	}
	if err := s.closeCLIJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsCLITabsLeaveMeegleClosesOnlyProjectPicker(t *testing.T) {
	s, _ := newMeeglePopoverTestApp(t)
	s.currentSettingsSection()
	state := s.settingsUnit.state.meegleProjectCatalog
	state.pickerOpen, state.loading, state.loaded, state.generation = true, true, false, 7
	s.settingsUnit.state.settingsCLISessions = map[string]map[string]any{"meegle": {"sessionId": "fixture-session", "state": "AUTHORIZATION_PENDING"}}
	action := application.Action{Kind: "meegle.projects", Payload: map[string]any{"catalogGeneration": "7"}}
	request, err := s.beginCLIJob(action)
	if err != nil {
		t.Fatal(err)
	}
	if !s.navigateSettingsPage("cli-genbu", "") || state.pickerOpen || state.loading || request.ctx.Err() == nil {
		t.Fatal("switching nested CLI tab did not close/cancel project picker")
	}
	s.finishMeegleProjectCatalog(action, application.ActionResult{Data: map[string]any{"projects": []meegleCatalogProject{{Name: "late", ProjectKey: "late"}}}}, nil)
	if state.pickerOpen || state.loaded || mapString(s.settingsUnit.state.settingsCLISessions["meegle"], "sessionId") != "fixture-session" {
		t.Fatal("late project result reopened picker or switching discarded auth session")
	}
	request.complete(nil)
	if err := s.closeCLIJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
}
