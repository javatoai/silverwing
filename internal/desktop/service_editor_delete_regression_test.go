package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func newServiceEditorModuleDeleteTester(state *serviceEditorState) (*app, *ui.Tester) {
	s := &app{
		dialogOpen:    true,
		inputs:        make(map[string]*string),
		settingsUnit:  settingsController{state: settingsState{settingsBoolInputs: make(map[string]*bool)}},
		workspaceUnit: workspaceController{state: workspaceState{serviceEditor: state}},
	}
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		ui.Column(c).Fill().Padding(12).Gap(8).Children(func() {
			s.renderServiceEditorModules(c, p, state)
		})
		s.renderServiceModulePopup(c, p, state)
	}, 1200, 5000)
	return s, tester
}

func newModuleDeleteRegressionState(modules ...serviceModuleDraft) *serviceEditorState {
	return &serviceEditorState{
		session:             "delete-regression",
		section:             "modules",
		service:             map[string]any{"masterBranch": "origin/master", "tagEnabled": false},
		modules:             modules,
		initialText:         make(map[string]string),
		initialBools:        make(map[string]bool),
		branchChoices:       make(map[string][]string),
		branchChoicesLoaded: make(map[string]bool),
		branchChoicesFailed: make(map[string]bool),
	}
}

func TestServiceEditorDeleteFirstModuleDoesNotPanic(t *testing.T) {
	state := newModuleDeleteRegressionState(
		newServiceModuleDraft("first-worktree", "first", "STANDARD_WORKTREE"),
		newServiceModuleDraft("second-clone", "second", "INDEPENDENT_CLONE"),
	)
	s, tester := newServiceEditorModuleDeleteTester(state)
	assertSelectedServiceEditorModule(t, state, tester, "first-worktree")
	name := "v4.1.5"
	s.inputs[s.serviceEditorKey("module:second-clone:name")] = &name

	if err := tester.Click("删除模块"); err != nil {
		t.Fatalf("click first module delete: %v", err)
	}
	if len(state.modules) != 1 || state.modules[0].id != "second-clone" {
		t.Fatalf("first module was not removed while retaining the second: %#v", state.modules)
	}
	assertSelectedServiceEditorModule(t, state, tester, "second-clone")
	if got := *s.serviceEditorModuleText(&state.modules[0], "name", state.modules[0].name); got != name {
		t.Fatalf("remaining module input was lost after deleting its sibling: got %q, want %q", got, name)
	}
}

func TestServiceEditorAddSecondThenDeleteFirstPreservesModuleDraft(t *testing.T) {
	state := newModuleDeleteRegressionState(newServiceModuleDraft("original-worktree", "original", "STANDARD_WORKTREE"))
	state.serviceID = "service"
	state.service = map[string]any{
		"id": "service", "repositoryId": "repo", "displayName": "Example",
		"masterBranch": "origin/master", "testTagBaselineRef": "origin/release/test",
		"developmentTool": "INTELLIJ_IDEA", "enabled": true, "genbuProbeEnabled": false,
	}
	s, tester := newServiceEditorModuleDeleteTester(state)
	if err := tester.Click("添加克隆模块"); err != nil {
		t.Fatalf("add second clone module: %v", err)
	}
	if len(state.modules) != 2 {
		t.Fatalf("adding a clone module produced %d modules, want 2", len(state.modules))
	}
	added := state.modules[1]
	assertSelectedServiceEditorModule(t, state, tester, added.id)
	if added.strategy != "INDEPENDENT_CLONE" {
		t.Fatalf("added module strategy = %q, want independent clone", added.strategy)
	}
	name := "v4.1.5"
	typeServiceEditorModuleName(t, tester, name)
	clickServiceModulePopupControl(t, tester, "original")
	assertSelectedServiceEditorModule(t, state, tester, "original-worktree")

	if err := tester.Click("删除模块"); err != nil {
		t.Fatalf("delete original first module: %v", err)
	}
	if len(state.modules) != 1 || state.modules[0].id != added.id || state.modules[0].strategy != "INDEPENDENT_CLONE" {
		t.Fatalf("new clone module was not retained after deleting the original: %#v", state.modules)
	}
	assertSelectedServiceEditorModule(t, state, tester, added.id)
	if got := *s.serviceEditorModuleText(&state.modules[0], "name", state.modules[0].name); got != name {
		t.Fatalf("new module name input was lost after deleting the original: got %q, want %q", got, name)
	}

	serviceName := "Edited service"
	masterBranch := "origin/feature/edited"
	s.inputs[s.serviceEditorKey("name")] = &serviceName
	s.inputs[s.serviceEditorKey("masterBranch")] = &masterBranch
	saved, err := s.buildServiceEditorConfig()
	if err != nil {
		t.Fatalf("build saved service config with dotted module name: %v", err)
	}
	if got := mapString(saved, "displayName"); got != serviceName {
		t.Fatalf("edited service name was not retained in saved config: got %q, want %q", got, serviceName)
	}
	if got := mapString(saved, "masterBranch"); got != masterBranch {
		t.Fatalf("edited master branch was not retained in saved config: got %q, want %q", got, masterBranch)
	}
	savedModules := mapsFromData(saved["modules"])
	if len(savedModules) != 1 || mapString(savedModules[0], "id") != added.id ||
		mapString(savedModules[0], "name") != name || mapString(savedModules[0], "strategy") != "INDEPENDENT_CLONE" {
		t.Fatalf("saved module does not preserve its dotted name, ID, and strategy: %#v", savedModules)
	}
	reopened := serviceModuleDrafts(saved["modules"])
	if len(reopened) != 1 || reopened[0].id != added.id || reopened[0].name != name || reopened[0].strategy != "INDEPENDENT_CLONE" {
		t.Fatalf("reopened module draft differs from the saved module: %#v", reopened)
	}
	invalidName := "v4..1"
	typeServiceEditorModuleName(t, tester, invalidName)
	if _, err := s.buildServiceEditorConfig(); err == nil || !strings.Contains(err.Error(), invalidName) || !strings.Contains(err.Error(), "点号") {
		t.Fatalf("invalid module feedback must identify the entered name and reason: %v", err)
	}
}

func TestServiceEditorDeleteMiddleAndLastModule(t *testing.T) {
	state := newModuleDeleteRegressionState(
		newServiceModuleDraft("module-one", "one", "STANDARD_WORKTREE"),
		newServiceModuleDraft("module-two", "two", "INDEPENDENT_CLONE"),
		newServiceModuleDraft("module-three", "three", "STANDARD_WORKTREE"),
		newServiceModuleDraft("module-four", "four", "INDEPENDENT_CLONE"),
	)
	_, tester := newServiceEditorModuleDeleteTester(state)
	clickServiceModulePopupControl(t, tester, "two")
	assertSelectedServiceEditorModule(t, state, tester, "module-two")
	clickServiceModulePopupControl(t, tester, "删除模块")
	if got, want := moduleIDs(state.modules), []string{"module-one", "module-three", "module-four"}; !equalStrings(got, want) {
		t.Fatalf("deleting the middle module produced IDs %v, want %v", got, want)
	}
	assertSelectedServiceEditorModule(t, state, tester, "module-three")
	clickServiceModulePopupControl(t, tester, "four")
	assertSelectedServiceEditorModule(t, state, tester, "module-four")
	clickServiceModulePopupControl(t, tester, "删除模块")
	if got, want := moduleIDs(state.modules), []string{"module-one", "module-three"}; !equalStrings(got, want) {
		t.Fatalf("deleting the last module produced IDs %v, want %v", got, want)
	}
	assertSelectedServiceEditorModule(t, state, tester, "module-three")
}

func TestServiceEditorCannotDeleteOnlyRemainingModule(t *testing.T) {
	state := newModuleDeleteRegressionState(newServiceModuleDraft("only-module", "only", "STANDARD_WORKTREE"))
	_, tester := newServiceEditorModuleDeleteTester(state)
	assertSelectedServiceEditorModule(t, state, tester, "only-module")
	if _, ok := tester.Find("删除模块"); ok {
		t.Fatal("delete button is visible when only one module remains")
	}
	if len(state.modules) != 1 || state.modules[0].id != "only-module" {
		t.Fatalf("single module changed unexpectedly: %#v", state.modules)
	}
}

func TestServiceEditorCancelLeavesPersistedModulesUntouched(t *testing.T) {
	persisted := []any{
		map[string]any{"id": "persisted-one", "name": "one", "strategy": "STANDARD_WORKTREE"},
		map[string]any{"id": "persisted-two", "name": "two", "strategy": "INDEPENDENT_CLONE"},
	}
	state := newModuleDeleteRegressionState(
		newServiceModuleDraft("persisted-one", "one", "STANDARD_WORKTREE"),
		newServiceModuleDraft("persisted-two", "two", "INDEPENDENT_CLONE"),
	)
	state.service["modules"] = persisted
	state.initialModules = moduleIDs(state.modules)
	s, tester := newServiceEditorModuleDeleteTester(state)
	if err := tester.Click("删除模块"); err != nil {
		t.Fatalf("remove module from editor draft: %v", err)
	}
	if len(state.modules) != 1 {
		t.Fatalf("draft module deletion did not apply: %#v", state.modules)
	}
	s.win = &mygo.Window{}
	s.dialogOpen = true
	s.dismissServiceEditor()
	if s.confirmationDialog == nil {
		t.Fatal("closing a modified editor should ask whether to discard changes")
	}
	s.finishConfirmationDialog(s.confirmationDialog, 0)
	if s.dialogOpen || s.workspaceUnit.state.serviceEditor != nil {
		t.Fatal("confirming discard should close the editor and clear the draft")
	}
	if got := serviceModuleDrafts(state.service["modules"]); len(got) != 2 || got[0].id != "persisted-one" || got[1].id != "persisted-two" {
		t.Fatalf("discarding changed the original service configuration: %#v", got)
	}
}

func TestServiceEditorDeleteFirstModuleShortcutDoesNotPanic(t *testing.T) {
	state := newModuleDeleteRegressionState(newServiceModuleDraft("module", "module", "STANDARD_WORKTREE"))
	state.modules[0].commands = []workspaceCommandDraft{
		{id: "shortcut-first", name: "First shortcut", executable: "go", directory: ".", timeout: "600"},
		{id: "shortcut-second", name: "Second shortcut", executable: "go", directory: ".", timeout: "600"},
	}
	_, tester := newServiceEditorModuleDeleteTester(state)
	clickServiceModulePopupControl(t, tester, "快捷命令 · 2 条")
	clickServiceModulePopupControl(t, tester, "First shortcut")
	clickServiceModulePopupControl(t, tester, "删除快捷命令")
	clickServiceModulePopupControl(t, tester, "完成")
	commands := state.modules[0].commands
	if len(commands) != 1 || commands[0].id != "shortcut-second" {
		t.Fatalf("the first shortcut was not removed while retaining the second: %#v", commands)
	}
}

func TestServiceEditorDeleteFirstSiblingEntriesDoesNotPanic(t *testing.T) {
	tests := []struct {
		name   string
		state  func() *serviceEditorState
		render func(*app, *ui.Context, palette, *serviceEditorState)
		click  string
		ids    func(*serviceEditorState) []string
		want   string
	}{
		{
			name: "copy rules",
			state: func() *serviceEditorState {
				state := newModuleDeleteRegressionState()
				state.copies = []copyRuleDraft{{id: "copy-first"}, {id: "copy-second"}}
				return state
			},
			render: func(s *app, c *ui.Context, p palette, state *serviceEditorState) {
				s.renderServiceEditorCopies(c, p, state)
			},
			click: "删除复制规则",
			ids: func(state *serviceEditorState) []string {
				ids := make([]string, 0, len(state.copies))
				for _, rule := range state.copies {
					ids = append(ids, rule.id)
				}
				return ids
			},
			want: "copy-second",
		},
		{
			name: "bootstrap commands",
			state: func() *serviceEditorState {
				state := newModuleDeleteRegressionState()
				state.commands = []bootstrapCommandDraft{{id: "bootstrap-first"}, {id: "bootstrap-second"}}
				return state
			},
			render: func(s *app, c *ui.Context, p palette, state *serviceEditorState) {
				s.renderServiceEditorCommands(c, p, state)
			},
			click: "删除初始化命令",
			ids: func(state *serviceEditorState) []string {
				ids := make([]string, 0, len(state.commands))
				for _, command := range state.commands {
					ids = append(ids, command.id)
				}
				return ids
			},
			want: "bootstrap-second",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := tt.state()
			s := &app{
				inputs:        make(map[string]*string),
				settingsUnit:  settingsController{state: settingsState{settingsBoolInputs: make(map[string]*bool)}},
				workspaceUnit: workspaceController{state: workspaceState{serviceEditor: state}},
			}
			tester := ui.NewTester(func(c *ui.Context) {
				p := setTheme(c, false)
				ui.Column(c).Fill().Padding(12).Gap(8).Children(func() { tt.render(s, c, p, state) })
			}, 1400, 6000)
			if err := tester.Click(tt.click); err != nil {
				t.Fatalf("click first %s delete: %v", tt.name, err)
			}
			got := tt.ids(state)
			if len(got) != 1 || got[0] != tt.want {
				t.Fatalf("first %s was not removed while retaining the second: got IDs %v, want [%s]", tt.name, got, tt.want)
			}
		})
	}
}
