package desktop

import (
	"silverwing-go/internal/application"
)

// applyTaskActionResult applies only task feature results on the UI thread.
func (s *app) applyTaskActionResult(action application.Action, result application.ActionResult) bool {
	switch action.Kind {
	case "task.restore":
		// Navigation follows the refreshed snapshot so old document and
		// request state is still associated with its actual visible task.
	default:
		return false
	}
	return true
}
