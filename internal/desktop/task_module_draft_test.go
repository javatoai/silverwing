package desktop

import (
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func TestTaskCreationTargetEditKeepsPreflightAcrossStableFrames(t *testing.T) {
	draft := &taskCreationModuleDraft{ID: "api", Name: "API", LastName: "API", TargetBranch: "feature/default", LastTargetBranch: "feature/default"}
	state := &taskCreationState{phase: "editing", moduleData: map[string]map[string]*taskCreationModuleDraft{"service": {"api": draft}}}
	s := &app{taskCreation: state}
	draft.TargetBranch = "feature/custom"
	s.syncTaskCreationModuleDraft("service", draft)
	preview := &taskCreationPreview{signature: "current", sharedAck: true}
	state.preview, state.phase = preview, "confirm"
	for range 3 {
		s.syncTaskCreationModuleDraft("service", draft)
	}
	if state.preview != preview || state.phase != "confirm" || !preview.sharedAck {
		t.Fatal("an unchanged target branch invalidated the completed preflight")
	}
	s.retargetTaskCreationServiceModules("service", "feature/renamed")
	if draft.TargetBranch != "feature/custom" || !draft.TargetEdited {
		t.Fatal("automatic retargeting replaced the user's branch")
	}
	draft.TargetBranch = "feature/new-edit"
	s.syncTaskCreationModuleDraft("service", draft)
	if state.preview != nil || state.phase != "editing" {
		t.Fatal("a subsequent real edit must invalidate the old preflight")
	}
}

func TestTaskAppendTargetEditKeepsPreflightAcrossStableFrames(t *testing.T) {
	draft := &taskCreationModuleDraft{ID: "api", Name: "API", LastName: "API", TargetBranch: "feature/default", LastTargetBranch: "feature/default"}
	state := taskAppendNewState(task{branch: "feature/task"}, "services", domain.GroupConfig{})
	state.moduleData["service"] = map[string]*taskCreationModuleDraft{"api": draft}
	state.moduleOrder["service"] = []string{"api"}
	s := &app{workspaceAppend: state}
	draft.TargetBranch = "feature/custom"
	s.syncTaskAppendModuleDraft(state, "service", draft)
	state.previewSignature, state.sharedAck = "current", true
	state.conflicts = []workspace.BranchReuseConflict{{RequiresForceAttach: true}}
	for range 3 {
		s.syncTaskAppendModuleDraft(state, "service", draft)
	}
	if state.previewSignature != "current" || !state.sharedAck || len(state.conflicts) != 1 {
		t.Fatal("an unchanged target branch cleared the preflight confirmation")
	}
	s.retargetTaskAppendModules(state, "service")
	if draft.TargetBranch != "feature/custom" || !draft.TargetEdited {
		t.Fatal("automatic retargeting replaced the user's branch")
	}
	draft.TargetBranch = "feature/new-edit"
	s.syncTaskAppendModuleDraft(state, "service", draft)
	if state.previewSignature != "" || state.sharedAck || len(state.conflicts) != 0 {
		t.Fatal("a subsequent real edit must invalidate the old preflight")
	}
}
