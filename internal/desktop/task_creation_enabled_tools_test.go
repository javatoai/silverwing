package desktop

import (
	"fmt"
	"strings"
	"testing"

	"silverwing-go/internal/application"
)

func TestTaskCreationToolsRespectEnabledSettings(t *testing.T) {
	for _, tc := range []struct {
		name            string
		ids             []string
		cursorAvailable bool
	}{
		{"codex-only", []string{"codex"}, true},
		{"cursor-only", []string{"cursor"}, true},
		{"both", []string{"codex", "cursor"}, true},
		{"none", []string{}, true},
		{"cursor-unavailable", []string{"cursor"}, false},
	} {
		for _, width := range []int{960, 1440} {
			for _, dark := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s-%d-dark-%t", tc.name, width, dark), func(t *testing.T) {
					s, tester := taskCreationLeftFixture(t, width, dark)
					form := s.taskUnit.state.taskCreation
					form.config.DefaultWorkspaceToolIDs = tc.ids
					action := application.Action{Kind: "workspace.tools-options", Payload: map[string]any{"taskCreateToken": form.token}}
					options := []taskCreationWorkspaceToolOption{
						{ID: "codex", DisplayName: "Codex", Available: true},
						{ID: "cursor", DisplayName: "Cursor", Available: tc.cursorAvailable, Reason: "未安装"},
					}
					if !s.taskCreationActionResult(action, application.ActionResult{Data: options}) {
						t.Fatal("tool result not applied")
					}
					tester.Frame()
					for _, option := range options {
						text := option.DisplayName
						if !option.Available {
							text += "（不可用）"
						}
						if tester.HasText(text) != containsString(tc.ids, option.ID) {
							t.Fatalf("creation ignored enabled settings: ids=%v tool=%s visible=%t", tc.ids, option.ID, tester.HasText(text))
						}
					}
					if len(tc.ids) == 0 && tester.HasText("创建后打开工作区工具") {
						t.Fatal("disabled tool group left a heading")
					}
					// Even stale selection state must not launch a hidden or unavailable tool.
					form.selectedToolIDs = map[string]bool{"codex": true, "cursor": true}
					want := []string{}
					for _, option := range options {
						if option.Available && containsString(tc.ids, option.ID) {
							want = append(want, option.ID)
						}
					}
					if got := s.taskCreationSelectedToolIDs(); strings.Join(got, ",") != strings.Join(want, ",") {
						t.Fatalf("creation selected hidden tools: got=%v want=%v", got, want)
					}
					if containsString(want, "codex") {
						before := s.settingsUnit.state.configJSON
						if err := tester.Click("Codex"); err != nil {
							t.Fatal(err)
						}
						tester.Frame()
						if form.selectedToolIDs["codex"] || !tester.HasText("Codex") || s.settingsUnit.state.configJSON != before {
							t.Fatal("temporary deselection changed visibility or global settings")
						}
					}
					captureTaskCreationLeft(t, tester, fmt.Sprintf("enabled-tools-%s-%d-dark-%t", tc.name, width, dark))
				})
			}
		}
	}
}
