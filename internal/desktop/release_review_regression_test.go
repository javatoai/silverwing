package desktop

import (
	"encoding/json"
	"testing"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func TestReleaseReviewOperationFeedbackRetainsMessages(t *testing.T) {
	for _, kind := range []string{"task.archive", "task.restore", "task.refresh", "task.materials-retry", "codex.workspace-retry"} {
		t.Run(kind, func(t *testing.T) {
			s := &app{
				taskUnit: taskController{state: taskState{tasks: []task{{id: "task", path: "/tasks/task", createdAt: "original"}}}},
			}
			s.snapshot.Home = "/app-home"
			message := "操作已返回；警告：资料说明未更新，请刷新资料单独重试"
			s.applyActionResultFeedback(application.Action{Kind: kind, TaskID: "task"}, application.ActionResult{Message: message}, s.snapshot.Home, s.taskUnit.currentTask())
			if s.lastMessage != message {
				t.Fatalf("operation feedback = %q, want %q", s.lastMessage, message)
			}
		})
	}
}

func TestReleaseReviewOperationFeedbackKeepsReadsAndAutosavesQuiet(t *testing.T) {
	for _, action := range []application.Action{
		{Kind: "document.list", TaskID: "task"},
		{Kind: "document.read", TaskID: "task", Path: "/materials/file.txt"},
		{Kind: "instructions.read", TaskID: "task"},
		{Kind: "instructions.templates.list"},
		{Kind: "instructions.save", TaskID: "task", Payload: map[string]any{"localTaskRules": true}},
		{Kind: "config.fields.save"},
	} {
		t.Run(action.Kind, func(t *testing.T) {
			s := &app{
				taskUnit:      taskController{state: taskState{tasks: []task{{id: "task", path: "/tasks/task", createdAt: "original"}}}},
				materialsUnit: materialsController{state: materialsState{documentPath: "/materials/file.txt"}},
				lastMessage:   "retained feedback",
			}
			s.snapshot.Home = "/app-home"
			if action.Kind == "config.fields.save" {
				s.settingsUnit.state.settingsAutosave = &settingsAutosaveState{home: s.snapshot.Home, active: &settingsAutosaveAttempt{kind: action.Kind}}
			}
			s.applyActionResultFeedback(action, application.ActionResult{Message: "background result"}, s.snapshot.Home, s.taskUnit.currentTask())
			if s.lastMessage != "retained feedback" {
				t.Fatalf("quiet action replaced feedback: %q", s.lastMessage)
			}
		})
	}
}

func TestReleaseReviewOperationFeedbackRejectsStaleContext(t *testing.T) {
	for _, change := range []string{"home", "selection", "path", "incarnation", "irrelevant result"} {
		t.Run(change, func(t *testing.T) {
			s := &app{
				taskUnit:    taskController{state: taskState{tasks: []task{{id: "task", path: "/tasks/task", createdAt: "original"}, {id: "other", path: "/tasks/other", createdAt: "other"}}}},
				lastMessage: "current feedback",
			}
			s.snapshot.Home = "/app-home"
			home, selected := s.snapshot.Home, s.taskUnit.currentTask()
			action := application.Action{Kind: "task.refresh", TaskID: "task"}
			switch change {
			case "home":
				s.snapshot.Home += "-replacement"
			case "selection":
				s.taskUnit.state.selected = 1
			case "path":
				s.taskUnit.state.tasks[0].path += "-moved"
			case "incarnation":
				s.taskUnit.state.tasks[0].createdAt = "replacement"
			case "irrelevant result":
				action.Kind, action.TaskID = "requirements.refresh", "other"
			}
			s.applyActionResultFeedback(action, application.ActionResult{Message: "stale feedback"}, home, selected)
			if s.lastMessage != "current feedback" {
				t.Fatalf("late result replaced current feedback: %q", s.lastMessage)
			}
		})
	}
}

func releaseReviewTemplateFixture(t *testing.T) (*app, *taskCreationState, *ui.Tester) {
	t.Helper()
	s := newApp(nil, Options{CapturePath: "release-review"})
	raw, err := json.Marshal(domain.DefaultAppConfig())
	if err != nil {
		t.Fatal(err)
	}
	s.snapshot.ConfigJSON, s.settingsUnit.state.configJSON = string(raw), string(raw)
	s.initTaskCreation("创建任务", "模板回归", "")
	s.dialogOpen, s.form.mode = true, "task-create"
	s.win = &mygo.Window{}
	state := s.taskUnit.state.taskCreation
	state.templates = []domain.AgentTaskTemplate{
		{ID: "first", Name: "First review template", Content: "original template\r\n"},
		{ID: "second", Name: "Second review template", Content: "replacement template"},
	}
	tester := ui.NewTester(func(c *ui.Context) { s.renderTaskCreationDialog(c, setTheme(c, false)) }, 960, 900)
	return s, state, tester
}

func TestReleaseReviewTemplateTogglePreservesEditedDraft(t *testing.T) {
	s, state, tester := releaseReviewTemplateFixture(t)
	if err := tester.Click(state.templates[0].Name); err != nil {
		t.Fatal(err)
	}
	if state.notes != state.templates[0].Content || state.selectedTemplate != "first" {
		t.Fatal("template selection did not populate notes")
	}
	draft := state.notes + "manually edited draft with trailing spaces  \r\n"
	state.notes = draft
	if err := tester.Click(state.templates[0].Name); err != nil {
		t.Fatal(err)
	}
	if state.notes != draft || state.selectedTemplate != "" || s.confirmationDialog != nil {
		t.Fatalf("deselecting an edited template changed its bytes: %q", state.notes)
	}
	state.notes = ""
	if err := tester.Click(state.templates[0].Name); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click(state.templates[0].Name); err != nil {
		t.Fatal(err)
	}
	if state.notes != "" || state.selectedTemplate != "" {
		t.Fatalf("deselecting an unchanged template did not clear it: %q", state.notes)
	}
}

func TestReleaseReviewTemplateReplacementRequiresCurrentConfirmation(t *testing.T) {
	s, state, tester := releaseReviewTemplateFixture(t)
	for _, template := range state.templates {
		if err := tester.Click(template.Name); err != nil {
			t.Fatal(err)
		}
	}
	if state.notes != state.templates[1].Content || s.confirmationDialog != nil {
		t.Fatal("switching an unedited template required confirmation or failed")
	}
	draft := "user draft\r\nwith trailing spaces  "
	state.notes = draft
	if err := tester.Click(state.templates[0].Name); err != nil {
		t.Fatal(err)
	}
	confirmation := s.confirmationDialog
	if confirmation == nil || state.notes != draft {
		t.Fatal("edited notes were replaced before confirmation")
	}
	s.finishConfirmationDialog(confirmation, confirmation.CancelIndex)
	if state.notes != draft || state.selectedTemplate != "second" {
		t.Fatal("canceling template replacement changed notes or selection")
	}
	if err := tester.Click(state.templates[0].Name); err != nil {
		t.Fatal(err)
	}
	if s.confirmationDialog == nil {
		t.Fatal("retry did not request confirmation")
	}
	s.finishConfirmationDialog(s.confirmationDialog, 0)
	if state.notes != state.templates[0].Content || state.selectedTemplate != "first" {
		t.Fatal("confirmed replacement did not apply the selected template")
	}
	state.notes = draft
	if err := tester.Click(state.templates[1].Name); err != nil {
		t.Fatal(err)
	}
	stale := s.confirmationDialog
	if stale == nil {
		t.Fatal("stale-dialog fixture did not request confirmation")
	}
	replacement := &taskCreationState{phase: "editing", notes: "new dialog draft\r\n "}
	s.taskUnit.state.taskCreation = replacement
	s.finishConfirmationDialog(stale, 0)
	if state.notes != draft || replacement.notes != "new dialog draft\r\n " {
		t.Fatal("old confirmation modified an old or replacement task draft")
	}
}
