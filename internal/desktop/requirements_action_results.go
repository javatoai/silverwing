package desktop

import (
	"silverwing-go/internal/application"
)

// applyRequirementActionResult applies only requirements feature results on the UI thread.
func (s *app) applyRequirementActionResult(action application.Action, result application.ActionResult) bool {
	switch action.Kind {
	case "meegle.projects":
		s.applyMeegleProjectCatalogResult(action, result)
	case "requirements.read", "requirements.comments", "requirements.attachment":
		s.applyRequirementContentResult(action, result)
	case "requirements.list", "requirements.refresh":
		s.applyRequirementsResult(result)
	default:
		return false
	}
	return true
}
