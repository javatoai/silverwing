package desktop

import (
	"encoding/json"
	"fmt"
	"image/color"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func tagBuildSelectionFixture(t *testing.T, width int, dark bool) (*app, *tagBuildViewState, *ui.Tester) {
	t.Helper()
	root := t.TempDir()
	config, err := json.Marshal(map[string]any{"tagEnabled": true, "taskRoot": filepath.Join(root, "tasks"), "materialsRoot": filepath.Join(root, "materials")})
	if err != nil {
		t.Fatal(err)
	}
	var workspaces []application.Workspace
	for _, id := range []string{"api", "job"} {
		workspaces = append(workspaces, application.Workspace{
			ID: "service:" + id, ServiceID: "service", ModuleID: id, Name: id, State: "READY", Path: filepath.Join(root, "tasks", "task", id),
			Record: domain.ServiceWorkspace{ModuleID: id, ModuleName: id, ServiceName: "fixture-service", Branch: "feature/saved", TagEnabled: true, TagMode: domain.TagBuildCurrentBranch},
		})
	}
	s := &app{
		taskUnit:     taskController{state: taskState{tasks: []task{{id: "task", path: filepath.Join(root, "tasks", "task"), createdAt: "created", groupID: "group", workspaces: workspaces}}}},
		settingsUnit: settingsController{state: settingsState{configJSON: string(config)}},
		page:         "tasks",
		inputs:       map[string]*string{},
	}
	s.snapshot.Home = filepath.Join(root, "home")
	s.snapshot.ConfigJSON = string(config)
	s.snapshot.Groups = []application.Group{{ID: "group", Name: "fixture"}}
	s.openTagBuildBatch(s.taskUnit.currentTask())
	if s.releaseUnit.state.tagBuild == nil {
		t.Fatal("fixture did not open batch selection")
	}
	tester := ui.NewTester(func(c *ui.Context) { s.renderTagBuildDialog(c, setTheme(c, dark)) }, width, 760)
	return s, s.releaseUnit.state.tagBuild, tester
}

func TestTagBuildWholeRowSelection(t *testing.T) {
	for _, width := range []int{680, 1100} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s, state, tester := tagBuildSelectionFixture(t, width, dark)
				row, found := tester.Find("选择构建 api")
				if !found || row.X < 0 || row.X+row.W > float32(width) || row.H < 50 {
					t.Fatalf("whole-row control clipped: %+v", row)
				}
				pixel := func() color.RGBA { return tester.Image().RGBAAt(int(row.X+row.W-4), int(row.Y+row.H/2)) }
				idle := pixel()
				tester.Move(row.X+row.W-4, row.Y+row.H/2)
				if pixel() == idle {
					t.Fatal("row padding lacks hover feedback")
				}
				if err := tester.Click("api"); err != nil {
					t.Fatal(err)
				}
				if !state.Selected["service:api"] || len(state.Selected) != 1 {
					t.Fatal("text click must select its stable module key exactly once")
				}
				selected := pixel()
				if selected == idle {
					t.Fatal("selected row lacks theme feedback")
				}
				// The empty right padding is part of the same native checkbox.
				tester.ClickAt(row.X+row.W-4, row.Y+row.H/2)
				if len(state.Selected) != 0 {
					t.Fatal("blank row area must cancel selection exactly once")
				}
				// Native checkbox box: 8 DIP padding, 16 DIP box.
				tester.ClickAt(row.X+16, row.Y+row.H/2)
				if !state.Selected["service:api"] || len(state.Selected) != 1 {
					t.Fatal("checkbox click double-toggled or selected another module")
				}
				if !tester.Focused("选择构建 api") {
					t.Fatal("row must retain native checkbox keyboard focus")
				}
				tester.Key(0, ui.KeySpace)
				if len(state.Selected) != 0 {
					t.Fatal("Space must cancel the focused row selection")
				}
				state.Workspaces[0], state.Workspaces[1] = state.Workspaces[1], state.Workspaces[0]
				tester.Frame()
				if !tester.Focused("选择构建 api") {
					t.Fatal("focus followed a list index instead of the stable module key")
				}
				tester.Key(0, ui.KeySpace)
				if !state.Selected["service:api"] || state.Selected["service:job"] {
					t.Fatal("reordered row keyboard input selected the wrong module")
				}
				s.busy = true // An unrelated operation does not own Tag selection.
				tester.Frame()
				if err := tester.Click("选择构建 job"); err != nil {
					t.Fatal(err)
				}
				if len(state.Selected) != 2 {
					t.Fatal("unrelated busy disabled Tag selection")
				}
				if err := tester.Click("选择构建 job"); err != nil {
					t.Fatal(err)
				}
				if state.Selected["service:job"] {
					t.Fatal("second job click did not cancel selection")
				}
				// Focus this row before it becomes disabled. Clicking a disabled
				// row must not steal keyboard focus from another editable row.
				if err := tester.Click("选择构建 api"); err != nil {
					t.Fatal(err)
				}
				tester.Key(0, ui.KeySpace)
				if len(state.Selected) != 1 || !state.Selected["service:api"] {
					t.Fatalf("selection already wrong before disabling: %v", state.Selected)
				}
				scope := s.tagJobScope()
				s.tagJobView(scope).snapshot = application.TagJobSnapshot{Scope: scope, ID: "fixture-job", Busy: true, Items: []application.TagJobItem{{ServiceID: "service", ModuleID: "api", WorkspacePath: s.taskUnit.currentTask().workspaces[0].Path, Busy: true}}}
				tester.Frame()
				if err := tester.Click("选择构建 api"); err != nil {
					t.Fatal(err)
				}
				if len(state.Selected) != 1 || !state.Selected["service:api"] {
					t.Fatalf("disabled row pointer changed selection: %v", state.Selected)
				}
				tester.Key(0, ui.KeySpace)
				if !state.Selected["service:api"] {
					t.Fatal("keyboard input toggled the disabled repository")
				}
				if err := tester.Click("全不选"); err != nil {
					t.Fatal(err)
				}
				if err := tester.Click("全选"); err != nil {
					t.Fatal(err)
				}
				if len(state.Selected) != 1 || !state.Selected["service:job"] {
					t.Fatal("select all included a running repository")
				}
			})
		}
	}
}

func TestTagBuildWholeRowSelectionPayload(t *testing.T) {
	s, state, tester := tagBuildSelectionFixture(t, 900, false)
	backend := &tagLayoutCaptureBackend{actions: make(chan application.Action, 1)}
	s.backend, s.backendGate = backend, make(chan struct{}, 1)
	for _, name := range []string{"全选", "全不选", "job"} {
		if err := tester.Click(name); err != nil {
			t.Fatal(err)
		}
		if name == "全选" && len(state.Selected) != 2 || name == "全不选" && len(state.Selected) != 0 {
			t.Fatalf("%s changed selection behavior: %+v", name, state.Selected)
		}
	}
	if err := tester.Click("开始构建"); err != nil {
		t.Fatal(err)
	}
	select {
	case request := <-backend.actions:
		if request.Kind != "tag.build" || request.TaskID != "task" || request.ServiceID != "" || request.ModuleID != "" || request.Payload["batch"] != true || !reflect.DeepEqual(request.Payload["selectionKeys"], []string{"service:job"}) {
			t.Fatalf("row selection did not preserve exact batch payload: %+v", request)
		}
	case <-time.After(time.Second):
		t.Fatal("selection did not reach memory-only capture backend")
	}
	if s.cancelOperation != nil {
		s.cancelOperation()
	}
	if s.releaseUnit.state.tagBuild != nil {
		t.Fatal("successful dispatch no longer closes the selection dialog")
	}
}
