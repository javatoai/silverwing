package desktop

import (
	"encoding/json"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/domain"
)

func TestTaskCreationCodexOptionFollowsEnabledToolPreference(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			s := newApp(nil, Options{CapturePath: "qa-test"})
			config := domain.DefaultAppConfig()
			if !enabled {
				config.DefaultWorkspaceToolIDs = []string{}
			}
			raw, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			s.snapshot.ConfigJSON, s.configJSON = string(raw), string(raw)
			s.initTaskCreation("创建任务", "测试任务", "")
			s.dialogOpen, s.form.mode = true, "task-create"
			if s.taskCreation.openCodex != enabled {
				t.Fatalf("default Codex selection = %t, want %t", s.taskCreation.openCodex, enabled)
			}
			tester := ui.NewTester(func(c *ui.Context) {
				s.renderTaskCreationDialog(c, setTheme(c, false))
			}, 960, 760)
			if visible := tester.HasText("创建后在 Codex 中打开"); visible != enabled {
				t.Fatalf("Codex switch visible = %t, want %t", visible, enabled)
			}
		})
	}
}

func TestTaskCreationPayloadIncludesOnlyEnabledAndSelectedCodex(t *testing.T) {
	for _, test := range []struct {
		name    string
		enabled bool
		checked bool
		want    []string
	}{
		{name: "disabled despite stale checked state", enabled: false, checked: true, want: []string{}},
		{name: "enabled but unchecked", enabled: true, checked: false, want: []string{}},
		{name: "enabled and checked", enabled: true, checked: true, want: []string{"codex"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			tools := []string{}
			if test.enabled {
				tools = []string{"codex"}
			}
			state := &taskCreationState{
				config:    domain.AppConfig{DefaultWorkspaceToolIDs: tools},
				draft:     domain.RequirementDraftState{TaskName: "payload-proof"},
				groupID:   "qa",
				openCodex: test.checked,
			}
			app := &app{taskCreation: state}
			payload, err := app.taskCreationPayload()
			if err != nil {
				t.Fatal(err)
			}
			got, ok := payload["workspaceToolIds"].([]string)
			if !ok || len(got) != len(test.want) {
				t.Fatalf("workspaceToolIds = %#v, want %#v", payload["workspaceToolIds"], test.want)
			}
			for i := range got {
				if got[i] != test.want[i] {
					t.Fatalf("workspaceToolIds = %#v, want %#v", got, test.want)
				}
			}
		})
	}
}
