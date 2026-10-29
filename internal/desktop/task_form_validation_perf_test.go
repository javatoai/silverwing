package desktop

import (
	"fmt"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func taskFormValidationFixture(n int) *app {
	config := domain.DefaultAppConfig()
	config.Groups = []domain.GroupConfig{{ID: "g", Services: []domain.GroupServiceConfig{{ID: "s", Enabled: true}}}}
	form := &taskCreationState{token: "token", config: config, configRevision: 1, groupID: "g", phase: "editing", services: map[string]bool{"s": true}, modules: map[string]map[string]bool{"s": {}}, moduleOrder: map[string][]string{"s": {}}, moduleData: map[string]map[string]*taskCreationModuleDraft{"s": {}}, moduleExpanded: map[string]map[string]bool{"s": {}}}
	form.draft.TaskName, form.draft.Branch = "fixture-task", "feature/fixture"
	form.lastName, form.lastBranch = form.draft.TaskName, form.draft.Branch
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("module-%05d", i)
		form.moduleOrder["s"] = append(form.moduleOrder["s"], id)
		form.modules["s"][id] = true
		form.moduleData["s"][id] = &taskCreationModuleDraft{taskModuleDraftValues: taskModuleDraftValues{ID: id, Name: id, Strategy: domain.WorkspaceStandardWorktree, BaseRef: "origin/main", BaseRemote: "origin", TargetBranch: "feature/fixture"}}
		form.config.Groups[0].Services[0].Modules = append(form.config.Groups[0].Services[0].Modules, domain.ServiceModuleConfig{ID: id, Name: id})
	}
	s := &app{taskUnit: taskController{state: taskState{taskCreation: form}}, dialogOpen: true, form: actionForm{mode: "task-create"}}
	s.snapshot.ConfigJSON = `{"groups":[]}`
	payload, _ := s.taskCreationPayload()
	form.preview = s.snapshotTaskCreationPreview(payload)
	return s
}

func TestTaskFormRenderSynchronizationsDeferCompleteValidation(t *testing.T) {
	s := taskFormValidationFixture(1000)
	form := s.taskUnit.state.taskCreation
	preview := form.preview
	form.renderingInputs = true
	form.notes = "changed notes"
	for i := 0; i < 1000; i++ {
		s.syncTaskCreationDraft()
	}
	if form.preview != preview {
		t.Fatal("input synchronization repeatedly validated the complete form")
	}
	s.checkTaskCreationPreviewSignature()
	form.renderingInputs = false
	if form.preview != nil || form.phase != "editing" {
		t.Fatal("single post-input validation missed changed notes")
	}
	payload, _ := s.taskCreationPayload()
	form.preview = s.snapshotTaskCreationPreview(payload)
	form.moduleData["s"][form.moduleOrder["s"][0]].TargetBranch = "feature/changed"
	s.syncTaskCreationDraft()
	if form.preview != nil {
		t.Fatal("non-render synchronization lost its complete validation contract")
	}
}

func TestTaskFormPreflightFreshBoundaryRejectsEveryScopeMutation(t *testing.T) {
	for name, mutate := range map[string]func(*app){
		"module": func(s *app) {
			f := s.taskUnit.state.taskCreation
			f.moduleData["s"][f.moduleOrder["s"][0]].TargetBranch = "changed"
		},
		"module-order": func(s *app) {
			order := s.taskUnit.state.taskCreation.moduleOrder["s"]
			order[0], order[1] = order[1], order[0]
		},
		"notes": func(s *app) { s.taskUnit.state.taskCreation.notes = "changed" },
		"requirement": func(s *app) {
			s.taskUnit.state.taskCreation.draft.RequirementLink = "https://project.feishu.cn/p/story/detail/2"
		},
		"same-config-slice": func(s *app) {
			s.taskUnit.state.taskCreation.config.Groups[0].Services[0].DisplayName = "Changed config service"
		},
		"snapshot-config": func(s *app) { s.snapshot.ConfigJSON = `{"groups":[],"changed":true}` },
		"home":            func(s *app) { s.snapshot.Home = "changed-home" },
	} {
		t.Run(name, func(t *testing.T) {
			s := taskFormValidationFixture(2)
			form := s.taskUnit.state.taskCreation
			form.phase = "preview"
			action := application.Action{Kind: "task.create-preview", Payload: map[string]any{"taskCreateToken": form.token}}
			request, err := s.prepareTaskCreationPreflight(action)
			if err != nil {
				t.Fatal(err)
			}
			defer s.completeTaskCreationPreflight(request)
			defer request.cancel()
			if !s.taskCreationPreflightRelevant(request) {
				t.Fatal("fresh preflight was not relevant")
			}
			_, _ = s.taskCreationObserverFingerprint(form) // prime observer-only cache
			mutate(s)
			if s.taskCreationPreflightRelevant(request) {
				t.Fatal("complete result boundary accepted a stale form/config")
			}
			s.finishTaskCreationPreflight(request, application.ActionResult{}, nil)
			if form.preview != nil || request.ctx.Err() == nil || form.phase != "editing" {
				t.Fatal("stale result did not retire and cancel its request")
			}
		})
	}
}

func TestTaskFormSubmissionRechecksMutableConfigAfterPreview(t *testing.T) {
	s := taskFormValidationFixture(2)
	form := s.taskUnit.state.taskCreation
	form.phase = "confirm"
	form.config.Groups[0].Services[0].Modules[0].Name = "same-slice config edit"
	s.submitTaskCreation(nil)
	if form.preview != nil || form.phase != "editing" {
		t.Fatal("submit reused observer cache instead of checking live config")
	}
}

func TestTaskFormObserverConfigSourceAndRevisionInvalidate(t *testing.T) {
	s := taskFormValidationFixture(2)
	form := s.taskUnit.state.taskCreation
	first, err := s.taskCreationObserverFingerprint(form)
	if err != nil {
		t.Fatal(err)
	}
	s.snapshot.ConfigJSON = `{"changed":true}`
	second, err := s.taskCreationObserverFingerprint(form)
	if err != nil || second == first {
		t.Fatal("snapshot source change reused observer fingerprint")
	}
	form.config.DefaultBranchPrefix = "changed"
	form.configRevision++ // owned config publication; direct test edits are checked at boundaries
	third, err := s.taskCreationObserverFingerprint(form)
	if err != nil || third == second {
		t.Fatal("form configuration publication revision was ignored")
	}
}

var taskFormBenchmark string

func BenchmarkTaskFormValidation(b *testing.B) {
	for _, n := range []int{10, 1000} {
		s := taskFormValidationFixture(n)
		form := s.taskUnit.state.taskCreation
		_, _ = s.taskCreationObserverFingerprint(form)
		b.Run(fmt.Sprintf("render-sync-and-one-signature/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				form.renderingInputs = true
				for i := 0; i < n; i++ {
					s.syncTaskCreationDraft()
				}
				s.checkTaskCreationPreviewSignature()
				form.renderingInputs = false
			}
		})
		b.Run(fmt.Sprintf("observer-config-warm/%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				taskFormBenchmark, _ = s.taskCreationObserverFingerprint(form)
			}
		})
	}
}
