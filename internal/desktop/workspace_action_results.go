package desktop

import (
	"silverwing-go/internal/application"
)

// applyWorkspaceActionResult applies only workspace feature results on the UI thread.
func (s *app) applyWorkspaceActionResult(action application.Action, result application.ActionResult) bool {
	switch action.Kind {
	case "workspace.bootstrap-clear", "workspace.bootstrap-rerun":
		s.lastMessage = result.Message
	case "workspace.remove-preview", "workspace.remove":
		s.applyWorkspaceRemovalResult(action, result)
	case "workspace.repair-preview", "workspace.repair":
		s.applyWorkspaceRepairResult(action, result)
	case "workspace.history", "workspace.file-preview", "workspace.diff-files", "workspace.diff-preview":
		s.applyWorkspaceHistoryResult(action, result)
	case "workspace.commit", "workspace.commit-push", "workspace.push", "workspace.batch":
		s.applyWorkspaceGitFlowResult(action, result)
	case "workspace.status":
		s.applyWorkspaceGitStatus(action, result)
	case "workspace.merge":
		s.applyWorkspaceMergeResult(action, result)
	case "workspace.merge-batch":
		s.applyWorkspaceMergeBatchResult(action, result)
	case "workspace.command":
		s.applyWorkspaceCommandResult(action, result)
	default:
		return false
	}
	return true
}
