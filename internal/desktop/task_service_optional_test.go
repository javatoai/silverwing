package desktop

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func TestServiceOptionalCreationValidationAndLayout(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, tester := taskCreationCompactFixture(t, width, dark, 1)
				form := s.taskUnit.state.taskCreation
				s.setTaskCreationServiceSelected("service", false)
				form.draft.TaskName = "只处理资料"
				form.draft.Branch = ""
				form.draft.MetadataHint = nil
				form.config.Services = nil
				tester.Frame()
				if message, _ := s.taskCreationValidationFailure(); message != "" {
					t.Fatal(message)
				}
				if !tester.HasText("创建任务") || tester.HasText("检查并创建任务") || !tester.HasText("服务与模块") {
					t.Fatal("no-service flow still requires workspaces")
				}
				if !tester.HasText("任务分支") || tester.HasText("任务分支（可选预设）") {
					t.Fatal("task branch label still contains the optional-preset suffix")
				}
				for _, preset := range []string{"", "feature/preset", "feature/{num}"} {
					form.draft.Branch = preset
					if message, _ := s.taskCreationValidationFailure(); message != "" {
						t.Fatal(message)
					}
					payload, err := s.taskCreationPayload()
					if err != nil {
						t.Fatal(err)
					}
					want := preset
					if domain.ContainsUnresolvedBranchPlaceholder(preset) {
						want = ""
					}
					if payload["featureBranch"] != want {
						t.Fatalf("task payload did not preserve a usable branch preset: %+v", payload)
					}
				}
				form.services["removed"] = true
				if message, _ := s.taskCreationValidationFailure(); message == "" {
					t.Fatal("stale selected service silently ignored")
				}
				form.services = map[string]bool{}
				form.draft.Branch = ""
				tester.Frame()
				captureTaskCreationLeft(t, tester, fmt.Sprintf("no-services-%d-dark-%t", width, dark))
			})
		}
	}
}

func configureTaskOpenTools(t *testing.T, s *app, ids []string) {
	t.Helper()
	config := domain.DefaultAppConfig()
	config.DefaultWorkspaceToolIDs = ids
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	s.settingsUnit.state.configJSON = string(raw)
}

func TestTaskOpenToolsMenuFollowsSettings(t *testing.T) {
	for _, ids := range [][]string{{"codex", "cursor"}, {"cursor"}, {"codex"}, {}} {
		s, b := taskMenuFixture(t)
		configureTaskOpenTools(t, s, ids)
		tester := taskNavigationTester(s, 340, 620)
		if err := tester.RightClick(s.taskUnit.state.tasks[1].title); err != nil {
			t.Fatal(err)
		}
		menu := tester.Menu()
		codexEnabled := containsString(ids, "codex")
		if slices.Contains(menu, "在 Codex 中打开") != codexEnabled || slices.Contains(menu, "在 Cursor 中打开") {
			t.Fatalf("menu ignored settings: %v %v", ids, menu)
		}
		if !codexEnabled && len(menu) > 0 && menu[0] == "-" {
			t.Fatal("empty tool group left a separator")
		}
		select {
		case got := <-b.calls:
			t.Fatalf("opening a menu dispatched %+v", got)
		default:
		}
	}
}

func TestTaskOpenCodexUsesContextTaskWithoutServices(t *testing.T) {
	for _, archived := range []bool{false, true} {
		s, b := taskMenuFixture(t)
		configureTaskOpenTools(t, s, []string{"codex", "cursor"})
		s.taskUnit.state.tasks[1].workspaces = nil
		if archived {
			s.taskUnit.state.tasks[1].archived = true
			s.page = "archive"
		}
		target := s.taskUnit.state.tasks[1]
		selected := s.taskUnit.state.selected
		tester := taskNavigationTester(s, 340, 620)
		navigationChooseTaskMenu(t, tester, target.title, "在 Codex 中打开")
		select {
		case got := <-b.calls:
			if got.Kind != "codex.run" || got.TaskID != target.id || got.Path != target.path || got.Payload[applicationTaskContextPayloadKey] == nil {
				t.Fatalf("wrong launch: %+v", got)
			}
		case <-time.After(time.Second):
			t.Fatal("Codex was not dispatched")
		}
		if s.taskUnit.state.selected != selected || !s.materialsUnit.state.documentDirty || s.materialsUnit.state.documentContent != "保留草稿" {
			t.Fatal("opening another task discarded the visible draft")
		}
	}
}

func TestTaskOpenToolsDisabledBusyAndChangedConfig(t *testing.T) {
	for _, mode := range []string{"disabled", "busy", "changed-config", "changed-home", "changed-task", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s, b := taskMenuFixture(t)
			ids := []string{"codex"}
			if mode == "disabled" {
				ids = []string{"cursor"}
			}
			configureTaskOpenTools(t, s, ids)
			if mode == "busy" {
				s.busy = true
			}
			tester := taskNavigationTester(s, 340, 620)
			if err := tester.RightClick(s.taskUnit.state.tasks[1].title); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "changed-config":
				s.settingsUnit.state.configJSON = `{"defaultWorkspaceToolIds":[]}`
			case "changed-home":
				s.snapshot.Home += "-other"
			case "changed-task":
				s.taskUnit.state.tasks[1].createdAt += "-replacement"
			case "cancel":
				tester.Key(0, ui.KeyEscape)
			}
			if mode != "cancel" {
				_ = tester.ChooseMenuItem("在 Codex 中打开")
			}
			select {
			case got := <-b.calls:
				t.Fatalf("invalid menu dispatched %+v", got)
			case <-time.After(20 * time.Millisecond):
			}
		})
	}
}

func TestTaskOpenToolsSettingsCard(t *testing.T) {
	s, _ := taskMenuFixture(t)
	configureTaskOpenTools(t, s, []string{"codex", "cursor"})
	s.page, s.settingsUnit.state.settingsPage = "settings", "task-creation"
	state := s.ensureSettingsExtensionRequests()
	state.workspaceToolsScope = s.settingsExtensionCurrentScope()
	state.workspaceTools = []taskCreationWorkspaceToolOption{
		{ID: "codex", DisplayName: "Codex", Available: true},
		{ID: "cursor", DisplayName: "Cursor", Available: true},
	}
	state.workspaceToolsLoaded = true
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { s.settingsWorkspaceTools(c, setTheme(c, false)) })
	}, 960, 760)
	if !tester.HasText("任务打开工具") || !tester.HasText("启用 Codex") || tester.HasText("启用 Cursor") || tester.HasText("Cursor") {
		t.Fatalf("unexpected workspace tool settings: %v", tester.Texts())
	}
	if !reloadsSnapshot("task.tools-launch") {
		t.Fatal("manual launch results do not refresh")
	}
}

func TestServiceOptionalDetailAndToolSettingsLayout(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, _ := taskMenuFixture(t)
				configureTaskOpenTools(t, s, []string{"codex", "cursor"})
				s.taskUnit.state.tasks[0].workspaces = nil
				detail := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Children(func() { s.taskDetails(c, setTheme(c, dark)) }) }, width, 800)
				if !detail.HasText("暂未关联服务") || detail.HasText("查看分支信息") {
					t.Fatal("no-service details expose Git controls")
				}
				captureDesktopTestFrame(t, detail, fmt.Sprintf("no-service-detail-%d-dark-%t", width, dark))
				s.page, s.settingsUnit.state.settingsPage = "settings", "task-creation"
				state := s.ensureSettingsExtensionRequests()
				state.workspaceToolsScope = s.settingsExtensionCurrentScope()
				state.workspaceTools = []taskCreationWorkspaceToolOption{{ID: "codex", DisplayName: "Codex", Available: true}}
				state.workspaceToolsLoaded = true
				settings := ui.NewTester(func(c *ui.Context) {
					ui.Scroll(c).Fill().Padding(20).Gap(16).Children(func() { s.settingsTaskCreation(c, setTheme(c, dark)) })
				}, width, 800)
				for _, name := range []string{"任务打开工具", "Codex"} {
					bounds, found := settings.Find(name)
					if !found || bounds.X < 0 || bounds.X+bounds.W > float32(width) {
						t.Fatalf("settings control clipped: %s %+v", name, bounds)
					}
				}
				captureDesktopTestFrame(t, settings, fmt.Sprintf("task-tool-settings-%d-dark-%t", width, dark))
				s.page = "tasks"
				menu := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, dark)
					ui.MenuScope(c, "task-tools-layout")
					ui.Column(c).Fill().Children(func() { s.taskIndex(c, p) })
					ui.ThemedMenus(c, "task-tools-layout")
				}, width, 800)
				if err := menu.RightClick(s.taskUnit.state.tasks[1].title); err != nil {
					t.Fatal(err)
				}
				menu.Frame()
				for _, name := range []string{"在 Codex 中打开", "删除任务"} {
					bounds, found := menu.Find(name)
					if !found || bounds.X < 0 || bounds.Y < 0 || bounds.X+bounds.W > float32(width) || bounds.Y+bounds.H > 800 {
						t.Fatalf("menu control clipped: %s %+v", name, bounds)
					}
				}
				captureDesktopTestFrame(t, menu, fmt.Sprintf("task-tools-menu-%d-dark-%t", width, dark))
				menu.Key(0, ui.KeyEscape)
			})
		}
	}
}

func TestTaskDetailHidesRetiredWorkspaceToolLaunch(t *testing.T) {
	s, _ := taskMenuFixture(t)
	s.tab = 0
	current := &s.taskUnit.state.tasks[s.taskUnit.state.selected]
	current.workspaceToolLaunches = []domain.WorkspaceToolLaunch{
		{ToolID: "cursor", Status: domain.WorkspaceToolFailed},
		{ToolID: "codex", Status: domain.WorkspaceToolFailed},
	}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { s.taskWorkspaceDetail(c, setTheme(c, false)) })
	}, 960, 760)
	if tester.HasText("工作区工具 · cursor") || !tester.HasText("工作区工具 · codex") {
		t.Fatalf("unexpected launch history: %v", tester.Texts())
	}
}

var _ application.Backend = (*taskMenuBackend)(nil)
