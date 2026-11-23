package desktop

import (
	"silverwing-go/internal/application"
)

const regenerateTaskInstructionsLabel = "重新生成任务说明"

func (s *app) regenerateTaskInstructions(scope taskMenuScope) {
	s.dispatchTaskContextAction(application.Action{Kind: "task.refresh-agents", TaskID: scope.taskID, Path: scope.taskPath}, &scope)
}

// applyTaskActionResult applies only task feature results on the UI thread.
func (s *app) applyTaskActionResult(action application.Action, result application.ActionResult) bool {
	switch action.Kind {
	case "task.refresh-agents":
		if view := s.taskFilesUnit.current; view != nil && view.identity.id == action.TaskID && taskFilesSamePath(view.identity.taskPath, action.Path) {
			s.refreshTaskFiles(view)
		}
	case "task.restore":
		// Navigation follows the refreshed snapshot so old document and
		// request state is still associated with its actual visible task.
	default:
		return false
	}
	return true
}
