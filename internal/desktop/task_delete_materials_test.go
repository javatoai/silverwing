package desktop

import (
	"errors"
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
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

func TestTaskDeleteForceRequiresExplicitReviewAndCapturedGuard(t *testing.T) {
	s := taskNavigationFixture()
	s.openTaskDeleteDialog(s.taskUnit.state.tasks[1])
	state := s.taskUnit.state.taskDeleteDialog
	state.Error, state.Loading = "", false
	state.ExternalWindowsClosed = true
	statusError := "仓库配置不存在：repo-missing"
	preview := workspace.TaskDeletePreview{
		Risks: []domain.DeleteRisk{{ServiceName: "message-push", StatusCheckError: &statusError}},
		Force: workspace.TaskForceDeletePreview{
			CanForce: true, Guard: "captured-force-guard", TaskPath: state.TaskPath,
			Workspaces: []string{state.TaskPath + "/message-push-default"},
		},
	}
	action := application.Action{Kind: "task.delete-risks", TaskID: state.TaskID, Path: state.TaskPath,
		Payload: map[string]any{"taskDeleteRequestID": fmt.Sprint(state.RequestID)}}
	s.applyTaskDeleteRiskResult(action, application.ActionResult{Data: preview})
	if _, ok := s.taskDeleteConfirmationAction(); ok {
		t.Fatal("unreviewed force preview enabled confirmation")
	}
	state.ForceReview = true
	if _, ok := s.taskDeleteConfirmationAction(); ok {
		t.Fatal("missing force confirmation enabled deletion")
	}
	state.ForceConfirmed = true
	confirmation, ok := s.taskDeleteConfirmationAction()
	if !ok || confirmation.Payload["forceGuard"] != "captured-force-guard" || confirmation.Payload["forceDiscard"] != false {
		t.Fatalf("explicit force approval was not scoped to the preview guard: %+v, allowed %t", confirmation, ok)
	}
	state.ForceConfirmed = false
	if _, ok := s.taskDeleteConfirmationAction(); ok {
		t.Fatal("removing force confirmation did not disable deletion")
	}
	state.Risks = nil
	state.ForceReview = false
	if _, ok := s.taskDeleteConfirmationAction(); ok {
		t.Fatal("force-capable preview allowed the regular delete path")
	}
}

func TestTaskDeleteForceApprovalCoversMixedWorkspaceRisks(t *testing.T) {
	s := taskNavigationFixture()
	s.openTaskDeleteDialog(s.taskUnit.state.tasks[1])
	state := s.taskUnit.state.taskDeleteDialog
	state.Error, state.Loading = "", false
	state.ExternalWindowsClosed = true
	statusError := "仓库配置不存在，请使用已验证的强制回收"
	dirtyRisk := domain.DeleteRisk{ServiceName: "configured-service", Staged: true, Untracked: true}
	preview := workspace.TaskDeletePreview{
		Risks: []domain.DeleteRisk{
			{ServiceName: "missing-config-service", StatusCheckError: &statusError},
			dirtyRisk,
		},
		Force: workspace.TaskForceDeletePreview{
			CanForce: true, Guard: "mixed-workspace-guard", TaskPath: state.TaskPath,
			Workspaces: []string{state.TaskPath + "/missing-config-service", state.TaskPath + "/configured-service"},
			Risks:      []domain.DeleteRisk{dirtyRisk},
		},
	}
	action := application.Action{Kind: "task.delete-risks", TaskID: state.TaskID, Path: state.TaskPath,
		Payload: map[string]any{"taskDeleteRequestID": fmt.Sprint(state.RequestID)}}
	s.applyTaskDeleteRiskResult(action, application.ActionResult{Data: preview})
	state.ForceReview = true
	if _, ok := s.taskDeleteConfirmationAction(); ok {
		t.Fatal("force action enabled without acknowledging the mixed workspace risk snapshot")
	}
	state.ForceConfirmed = true
	confirmation, ok := s.taskDeleteConfirmationAction()
	if !ok || confirmation.Payload["forceGuard"] != "mixed-workspace-guard" || confirmation.Payload["forceDiscard"] != false {
		t.Fatalf("force review did not authorize its complete risk snapshot independently: %+v, allowed %t", confirmation, ok)
	}
}

func TestTaskDeleteForceValidationReasonBlocksOrdinaryDelete(t *testing.T) {
	s := taskNavigationFixture()
	s.openTaskDeleteDialog(s.taskUnit.state.tasks[1])
	state := s.taskUnit.state.taskDeleteDialog
	state.Error, state.Loading = "", false
	state.ExternalWindowsClosed = true
	state.Force = workspace.TaskForceDeletePreview{Reason: "工作区包含链接或无法确认身份"}
	if _, ok := s.taskDeleteConfirmationAction(); ok {
		t.Fatal("force validation failure fell through to ordinary task deletion")
	}
}

func TestTaskDeleteForceReviewKeepsConfirmationVisibleAtShortHeight(t *testing.T) {
	s := taskNavigationFixture()
	s.openTaskDeleteDialog(s.taskUnit.state.tasks[1])
	state := s.taskUnit.state.taskDeleteDialog
	state.Error, state.Loading = "", false
	state.ExternalWindowsClosed = true
	state.Force = workspace.TaskForceDeletePreview{
		CanForce: true, Guard: "short-window-guard", TaskPath: state.TaskPath,
		Workspaces: []string{state.TaskPath + "/message-push"},
		Risks:      []domain.DeleteRisk{{ServiceName: "message-push", Staged: true, Untracked: true, UnpushedCommits: 3}},
	}
	state.ForceReview, state.ForceConfirmed = true, true
	tester := ui.NewTester(func(c *ui.Context) { s.renderTaskDeleteDialog(c, setTheme(c, false)) }, 800, 480)
	confirm, ok := tester.Find("确认强制删除")
	if !ok || confirm.Y < 0 || confirm.Y+confirm.H > 480 {
		t.Fatalf("force confirmation is not visible at a short window height: %+v, found=%t", confirm, ok)
	}
}

func TestTaskDeleteForcePreviewIsClearedBeforeRecheck(t *testing.T) {
	s := taskNavigationFixture()
	s.openTaskDeleteDialog(s.taskUnit.state.tasks[1])
	state := s.taskUnit.state.taskDeleteDialog
	state.Force = workspace.TaskForceDeletePreview{CanForce: true, Guard: "old"}
	state.ForceReview, state.ForceConfirmed = true, true
	s.requestTaskDeleteRisks()
	if state.Force.CanForce || state.Force.Guard != "" || state.ForceReview || state.ForceConfirmed {
		t.Fatal("force authorization survived a new delete inspection")
	}
}

func TestTaskDeleteForceFailureRequiresFreshInspection(t *testing.T) {
	s := taskNavigationFixture()
	s.openTaskDeleteDialog(s.taskUnit.state.tasks[1])
	state := s.taskUnit.state.taskDeleteDialog
	state.Force = workspace.TaskForceDeletePreview{CanForce: true, Guard: "old-guard"}
	state.ForceReview, state.ForceConfirmed = true, true
	state.Loading, state.Submitting = true, true
	action := application.Action{Kind: "task.delete", TaskID: state.TaskID, Path: state.TaskPath,
		Payload: map[string]any{"taskDeleteRequestID": fmt.Sprint(state.RequestID), "forceGuard": "old-guard"}}
	if !s.applyTaskDeleteActionFailure(action, errors.New("工作区身份已变化，请重新检查")) {
		t.Fatal("force failure escaped the delete dialog")
	}
	if state.Error == "" || state.Loading || state.Submitting || state.Force.CanForce || state.ForceConfirmed || state.ForceReview {
		t.Fatalf("failed force approval remained reusable or lacked recovery feedback: %+v", state)
	}
	if _, ok := s.taskDeleteConfirmationAction(); ok {
		t.Fatal("stale force preview remained confirmable after failure")
	}
}
