package desktop

import (
	"fmt"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/workspace"
)

func TestTaskDeleteMaterialsDefaultsAndCapturedApproval(t *testing.T) {
	s := taskNavigationFixture()
	s.openTaskDeleteDialog(s.taskUnit.state.tasks[1])
	state := s.taskUnit.state.taskDeleteDialog
	if state == nil || state.DeleteMaterials {
		t.Fatal("materials selected by default")
	}
	state.Error, state.Loading = "", false
	state.ExternalWindowsClosed = true
	action := application.Action{Kind: "task.delete-risks", TaskID: state.TaskID, Path: state.TaskPath, Payload: map[string]any{"taskDeleteRequestID": fmt.Sprint(state.RequestID)}}
	s.applyTaskDeleteRiskResult(action, application.ActionResult{Data: workspace.TaskDeletePreview{Materials: workspace.TaskMaterialsDeletePreview{Root: "materials/2026/123-task", CanDelete: true, Guard: "captured"}}})
	state.DeleteMaterials = true
	confirmation, ok := s.taskDeleteConfirmationAction()
	if !ok || confirmation.Payload["deleteMaterials"] != true || confirmation.Payload["materialsGuard"] != "captured" {
		t.Fatalf("approval not captured: %+v %v", confirmation, ok)
	}
	s.applyTaskDeleteRiskResult(action, application.ActionResult{Data: workspace.TaskDeletePreview{Materials: workspace.TaskMaterialsDeletePreview{Reason: "shared"}}})
	if state.DeleteMaterials || state.Materials.CanDelete {
		t.Fatal("shared result retained destructive approval")
	}
}
