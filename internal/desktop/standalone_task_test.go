package desktop

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
)

func TestStandaloneTaskCreateEntryAndLayouts(t *testing.T) {
	for _, width := range []int{960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s := taskNavigationFixture()
				s.taskUnit.state.tasks = nil
				s.dark = dark
				s.backend = &annualMaterialsUIBackend{}
				config := domain.DefaultAppConfig()
				taskRoot := filepath.Join(t.TempDir(), "tasks")
				config.TaskRoot = &taskRoot
				config.DefaultWorkspaceToolIDs = []string{}
				config.MeegleProjects = []domain.MeegleProjectConfig{{SimpleName: "fixture"}}
				raw, _ := json.Marshal(config)
				s.settingsUnit.state.configJSON, s.snapshot.ConfigJSON = string(raw), string(raw)
				s.snapshot.Home = t.TempDir()
				tester := taskNavigationTester(s, width, 800)
				if _, found := tester.Find("新建任务"); !found {
					t.Fatal("task list has no create entry")
				}
				if err := tester.Click("新建任务"); err != nil {
					t.Fatal(err)
				}
				form := s.taskUnit.state.taskCreation
				t.Cleanup(func() {
					s.cancelTaskCreationReads(form, false)
					s.closeTaskCreationRequirements()
					s.closeTaskCreationAINaming()
				})
				if form == nil || form.draft.RequirementLink != "" || form.draft.Branch != "" || taskCreationHasSelectedServices(form) {
					t.Fatal("standalone form inherited a requirement, branch or service")
				}
				form.toolsLoaded, form.toolsLoading = true, false
				s.busy = false // The headless fixture has completed its synthetic tool read.
				form.draft.TaskName = "临时订单分析"
				s.syncTaskCreationDraft()
				payload, err := s.taskCreationPayload()
				if err != nil || payload["requirementLink"] != "" || payload["featureBranch"] != "" {
					t.Fatalf("name-only task is invalid: %+v %v", payload, err)
				}
				if state := s.ensureTaskCreationRequirements(); state.candidatesLoading || state.candidatesLoaded || state.candidatesError != "" || state.metadataRequest != nil || form.draft.MetadataHint != nil {
					t.Fatal("opening a standalone form started Meegle reads")
				}
				token := form.token
				tester.Frame()
				_ = tester.Click("新建任务")
				if s.taskUnit.state.taskCreation.token != token {
					t.Fatal("repeated entry click replaced the active form")
				}
				modal := ui.NewTester(func(c *ui.Context) { s.renderTaskCreationDialog(c, setTheme(c, dark)) }, width, 800)
				if !modal.HasText("新建任务") || !modal.HasText("创建任务") {
					t.Fatal("standalone creation UI is missing")
				}
				captureTaskCreationLeft(t, modal, fmt.Sprintf("standalone-%d-dark-%t", width, dark))
				modal.Key(0, ui.KeyEscape)
				if s.taskUnit.state.taskCreation != nil || s.dialogOpen {
					t.Fatal("Esc did not close the standalone creation form")
				}
			})
		}
	}
}

func TestStandaloneTaskRequirementFormScopeAndMaterialPermissions(t *testing.T) {
	s := taskNavigationFixture()
	s.backend = &annualMaterialsUIBackend{}
	s.taskUnit.state.tasks[0].path = filepath.Join(t.TempDir(), "analysis")
	s.snapshot.Home = t.TempDir()
	s.settingsUnit.state.configJSON = `{"taskRoot":"C:\\isolated"}`
	s.taskUnit.state.selected = 1
	s.materialsUnit.state.documentContent, s.materialsUnit.state.documentDirty = "保留正在查看的资料草稿", true
	target := s.taskUnit.state.tasks[0]
	tester := taskNavigationTester(s, 340, 560)
	navigationChooseTaskMenu(t, tester, target.title, "关联需求")
	if s.form.taskID != target.id || s.form.taskScope == nil {
		t.Fatal("association did not capture the clicked task")
	}
	if s.taskUnit.state.selected != 1 || s.materialsUnit.state.documentContent != "保留正在查看的资料草稿" || !s.materialsUnit.state.documentDirty {
		t.Fatal("opening association changed the selected task or its draft")
	}
	s.submitTaskRequirementForm(s.form, map[string]any{"requirementLink": "invalid"})
	if !s.dialogOpen || s.form.fields[0].error == "" {
		t.Fatal("invalid link closed the form")
	}
	s.settingsUnit.state.configJSON = `{"taskRoot":"C:\\different"}`
	s.submitTaskRequirementForm(s.form, map[string]any{"requirementLink": "https://project.feishu.cn/obt/userstory/detail/123"})
	if s.busy || !s.dialogOpen || s.lastError == "" {
		t.Fatal("stale association was submitted")
	}
	s.taskUnit.state.selected = 0
	scope := documents.LocalTaskMaterials(target.path)
	s.taskUnit.state.tasks[0].materialsPath = scope.Root
	if !s.materialsWritable(filepath.Join(scope.Owned, "file.md"), false) || s.materialsWritable(filepath.Join(scope.Root, "peer", "file.md"), false) {
		t.Fatal("local materials lost their permission scope")
	}
}
