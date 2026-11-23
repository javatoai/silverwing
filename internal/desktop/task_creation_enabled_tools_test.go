package desktop

import (
	"encoding/json"
	"testing"

	"silverwing-go/internal/application"
)

func TestTaskCreationUsesSavedToolDefaults(t *testing.T) {
	for _, entry := range []struct {
		title, requirementTitle, link string
	}{
		{title: "新建任务"},
		{title: "基于需求创建任务", requirementTitle: "原始需求", link: "https://project.feishu.cn/obt/userstory/detail/1000000004"},
	} {
		for _, settings := range []struct {
			name      string
			ids       []string
			available bool
		}{
			{name: "enabled", ids: []string{"codex"}, available: true},
			{name: "disabled", ids: []string{}, available: true},
			{name: "unavailable", ids: []string{"codex"}},
		} {
			t.Run(entry.title+"/"+settings.name, func(t *testing.T) {
				s, tester := taskCreationCompactFixture(t, 960, false, 1)
				config := s.taskUnit.state.taskCreation.config
				config.DefaultWorkspaceToolIDs = settings.ids
				raw, err := json.Marshal(config)
				if err != nil {
					t.Fatal(err)
				}
				s.requestTaskCreationDismiss()
				saved := string(raw)
				s.snapshot.ConfigJSON, s.settingsUnit.state.configJSON = saved, saved
				tools := s.ensureSettingsExtensionRequests()
				tools.workspaceToolsLoaded = true
				tools.workspaceTools = []taskCreationWorkspaceToolOption{{ID: "codex", DisplayName: "Codex", Available: settings.available}}
				s.openTaskCreateForm(entry.title, entry.requirementTitle, entry.link)
				form := s.taskUnit.state.taskCreation
				if form == nil || form.readRequest != nil || s.busy {
					t.Fatal("creation form did not open directly")
				}
				t.Cleanup(s.requestTaskCreationDismiss)
				form.draft.TaskName = "示例任务"
				tester.Frame()
				if tester.HasText("创建后打开工作区工具") || tester.HasText("Codex") || tester.HasText("Codex（不可用）") {
					t.Fatalf("unexpected tool controls in creation form: %v", tester.Texts())
				}

				payload, err := s.taskCreationPayload()
				if err != nil {
					t.Fatal(err)
				}
				form.preview = s.snapshotTaskCreationPreview(payload)
				for _, action := range []application.Action{
					{Kind: "task.create-preview", Payload: taskCreationActionPayload(form.preview.payload, form.token)},
					{Kind: "task.create", Payload: taskCreationSubmissionPayload(form, nil)},
				} {
					request, err := application.TaskCreateRequestFromAction(action)
					if _, overridden := action.Payload["workspaceToolIds"]; err != nil || overridden || request.WorkspaceToolIDs != nil || request.RequirementLink != entry.link {
						t.Fatalf("%s did not leave the tool selection to settings: %+v (%v)", action.Kind, request, err)
					}
				}
				if s.settingsUnit.state.configJSON != saved {
					t.Fatal("task creation changed the saved settings")
				}
			})
		}
	}
}
