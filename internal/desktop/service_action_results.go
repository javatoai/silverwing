package desktop

import (
	"silverwing-go/internal/application"
)

// applyServiceActionResult applies only service feature results on the UI thread.
func (s *app) applyServiceActionResult(action application.Action, result application.ActionResult) bool {
	switch action.Kind {
	case "service.save":
		if s.serviceEditor != nil && s.serviceEditor.saving {
			s.cancelServiceRepositoryChoiceReads()
			s.serviceEditor = nil
			s.dialogOpen = false
			s.clearServiceEditorInputs()
		}
	case "repository.choices":
		s.applyServiceRepositoryChoices(action, result)
		if s.serviceEditor != nil && s.serviceEditor.section == "git" && !s.serviceEditor.addressesLoaded {
			s.requestServiceRepositoryAddresses(false)
		}
	case "repository.addresses":
		s.applyServiceRepositoryAddresses(action, result)
	case "service.add-repositories":
		s.applyRepositoryBatchResult(result.Data)
	default:
		return false
	}
	return true
}
