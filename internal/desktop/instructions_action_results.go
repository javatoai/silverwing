package desktop

import "silverwing-go/internal/application"

// applyInstructionActionResult applies only instructions feature results on the UI thread.
func (s *app) applyInstructionActionResult(action application.Action, result application.ActionResult) bool {
	switch action.Kind {
	case "instructions.read":
		s.applyInstructionReadResult(action, result)
		if scope := payloadString(action.Payload, "scope"); scope != "" {
			groupID := payloadString(action.Payload, "groupId")
			context := scope + ":" + groupID
			if context == s.settingsUnit.state.settingsAgentContext && s.settingsUnit.state.settingsPage == "agents" {
				s.settingsUnit.state.settingsOutput = ""
			}
		}
	case "instructions.save":
		s.applyInstructionSaveResult(action, result)
	default:
		return false
	}
	return true
}
