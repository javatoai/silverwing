package desktop

import (
	"silverwing-go/internal/application"
)

// applyServiceActionResult applies only service feature results on the UI thread.
func (s *app) applyServiceActionResult(action application.Action, result application.ActionResult) bool {
	switch action.Kind {
	case "service.save":
		if s.workspaceUnit.state.serviceEditor != nil && s.workspaceUnit.state.serviceEditor.saving {
			s.cancelServiceRepositoryChoiceReads()
			s.workspaceUnit.state.serviceEditor = nil
			s.dialogOpen = false
			s.clearServiceEditorInputs()
		}
	case "repository.choices":
		s.applyServiceRepositoryChoices(action, result)
		if s.workspaceUnit.state.serviceEditor != nil && s.workspaceUnit.state.serviceEditor.section == "git" && !s.workspaceUnit.state.serviceEditor.addressesLoaded {
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
