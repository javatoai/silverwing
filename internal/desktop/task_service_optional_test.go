package desktop

import (
	"encoding/json"
	"fmt"
	"strings"
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
				form.config.Groups[0].Services = nil
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
					if payload["featureBranch"] != preset {
						t.Fatalf("branch preset changed: %+v", payload)
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

func configureTaskOpenTools(t *testing.T, s *app, ids []string, cursorAvailable bool) {
	t.Helper()
	config := domain.DefaultAppConfig()
	config.DefaultWorkspaceToolIDs = ids
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	s.settingsUnit.state.configJSON = string(raw)
	s.taskUnit.state.taskOpenTools = &taskOpenToolsState{home: s.snapshot.Home, config: s.settingsUnit.state.configJSON, loaded: true, options: []taskCreationWorkspaceToolOption{{ID: "codex", DisplayName: "Codex", Available: true}, {ID: "cursor", DisplayName: "Cursor", Available: cursorAvailable}}}
}

func TestTaskOpenToolsMenuFollowsSettings(t *testing.T) {
	for _, ids := range [][]string{{"codex", "cursor"}, {"cursor"}, {"codex"}, {}} {
		s, b := taskMenuFixture(t)
		configureTaskOpenTools(t, s, ids, true)
		tester := taskNavigationTester(s, 340, 620)
		if err := tester.RightClick(s.taskUnit.state.tasks[1].title); err != nil {
			t.Fatal(err)
		}
		menu := tester.Menu()
		joined := strings.Join(menu, "|")
		for _, tool := range []struct{ id, name string }{{"codex", "Codex"}, {"cursor", "Cursor"}} {
			if strings.Contains(joined, "在 "+tool.name+" 中打开") != containsString(ids, tool.id) {
				t.Fatalf("menu ignored settings: %v %v", ids, menu)
			}
		}
		if len(ids) == 0 && len(menu) > 0 && menu[0] == "-" {
			t.Fatal("empty tool group left a separator")
		}
		if len(ids) == 2 && (menu[0] != "在 Codex 中打开" || menu[1] != "在 Cursor 中打开") {
			t.Fatal("tool order changed")
		}
		select {
		case got := <-b.calls:
			t.Fatalf("opening a menu dispatched %+v", got)
		default:
		}
	}
}

func TestTaskOpenToolsCursorUsesContextTask(t *testing.T) {
	for _, archived := range []bool{false, true} {
		s, b := taskMenuFixture(t)
		configureTaskOpenTools(t, s, []string{"codex", "cursor"}, true)
		s.taskUnit.state.tasks[1].workspaces = nil
		if archived {
			s.taskUnit.state.tasks[1].archived = true
			s.page = "archive"
		}
		target := s.taskUnit.state.tasks[1]
		selected := s.taskUnit.state.selected
		tester := taskNavigationTester(s, 340, 620)
		navigationChooseTaskMenu(t, tester, target.title, "在 Cursor 中打开")
		select {
		case got := <-b.calls:
			ids, _ := got.Payload["toolIds"].([]string)
			if got.Kind != "task.tools-launch" || got.TaskID != target.id || got.Path != target.path || len(ids) != 1 || ids[0] != "cursor" {
				t.Fatalf("wrong launch: %+v", got)
			}
		case <-time.After(time.Second):
			t.Fatal("Cursor was not dispatched")
		}
		if s.taskUnit.state.selected != selected || !s.materialsUnit.state.documentDirty || s.materialsUnit.state.documentContent != "保留草稿" {
			t.Fatal("opening another task discarded the visible draft")
		}
	}
}

func TestTaskOpenToolsDisabledBusyAndChangedConfig(t *testing.T) {
	for _, mode := range []string{"unavailable", "busy", "changed-config", "changed-home", "changed-task", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s, b := taskMenuFixture(t)
			configureTaskOpenTools(t, s, []string{"cursor"}, mode != "unavailable")
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
				_ = tester.ChooseMenuItem("在 Cursor 中打开")
			}
			deadline := time.NewTimer(20 * time.Millisecond)
			defer deadline.Stop()
			for {
				select {
				case got := <-b.calls:
					if got.Kind == "workspace.tools-options" && got.TaskID == "" && got.Path == "" &&
						got.ServiceID == "" && got.ModuleID == "" && got.Text == "" && len(got.Payload) == 1 &&
						payloadString(got.Payload, "taskCreateToken") == "task-menu-tools" {
						continue
					}
					t.Fatalf("invalid menu dispatched %+v", got)
				case <-deadline.C:
					return
				}
			}
		})
	}
}

func TestTaskOpenToolsSettingsCardAndTemporaryChoices(t *testing.T) {
	s, _ := taskMenuFixture(t)
	configureTaskOpenTools(t, s, []string{"codex", "cursor"}, false)
	s.page, s.settingsUnit.state.settingsPage = "settings", "task-creation"
	state := s.ensureSettingsExtensionRequests()
	state.workspaceToolsScope = s.settingsExtensionCurrentScope()
	state.workspaceTools, state.workspaceToolsLoaded = s.taskUnit.state.taskOpenTools.options, true
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { s.settingsWorkspaceTools(c, setTheme(c, false)) })
	}, 960, 760)
	if !tester.HasText("任务打开工具") || !tester.HasText("启用 Cursor") {
		t.Fatal("settings card is missing")
	}
	create, _ := taskCreationLeftFixture(t, 960, false)
	before := create.settingsUnit.state.configJSON
	create.taskUnit.state.taskCreation.selectedToolIDs = map[string]bool{"codex": false, "cursor": true}
	if create.settingsUnit.state.configJSON != before {
		t.Fatal("temporary selection modified global defaults")
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
				configureTaskOpenTools(t, s, []string{"codex", "cursor"}, true)
				s.taskUnit.state.tasks[0].workspaces = nil
				detail := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Children(func() { s.taskDetails(c, setTheme(c, dark)) }) }, width, 800)
				if !detail.HasText("暂未关联服务") || detail.HasText("查看分支信息") {
					t.Fatal("no-service details expose Git controls")
				}
				captureDesktopTestFrame(t, detail, fmt.Sprintf("no-service-detail-%d-dark-%t", width, dark))
				s.page, s.settingsUnit.state.settingsPage = "settings", "task-creation"
				state := s.ensureSettingsExtensionRequests()
				state.workspaceToolsScope = s.settingsExtensionCurrentScope()
				state.workspaceTools, state.workspaceToolsLoaded = s.taskUnit.state.taskOpenTools.options, true
				settings := ui.NewTester(func(c *ui.Context) {
					ui.Scroll(c).Fill().Padding(20).Gap(16).Children(func() { s.settingsTaskCreation(c, setTheme(c, dark)) })
				}, width, 800)
				for _, name := range []string{"任务打开工具", "Codex", "Cursor"} {
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
				for _, name := range []string{"在 Codex 中打开", "在 Cursor 中打开", "删除任务"} {
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

var _ application.Backend = (*taskMenuBackend)(nil)
