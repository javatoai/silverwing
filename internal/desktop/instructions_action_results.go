package desktop

import (
	"silverwing-go/internal/application"
)

// applyInstructionActionResult applies only instructions feature results on the UI thread.
func (s *app) applyInstructionActionResult(action application.Action, result application.ActionResult) bool {
	switch action.Kind {
	case "instructions.templates.list":
		if payloadString(action.Payload, "taskCreateToken") != "" {
			return false
		}
		state := s.ensureTaskNotesUI()
		generation := payloadUint64(action.Payload, "settingsTemplatesGeneration")
		if generation == 0 || generation != state.settingsTemplatesRequestGeneration ||
			payloadString(action.Payload, "settingsTemplatesPage") != "instruction-templates" ||
			payloadString(action.Payload, "settingsTemplatesHome") != s.snapshot.Home {
			return true
		}
		state.settingsTemplatesLoading = false
		state.settingsTemplatesError = ""
		s.settingsTemplates = mapsFromData(result.Data)
		s.settingsTemplatesLoaded = true
		return true
	case "instructions.templates.save", "instructions.templates.remove":
		if s.page == "settings" && s.settingsPage == "instruction-templates" {
			s.settingsOutput = result.Message
			s.requestSettingsInstructionTemplates()
		}
		return true
	case "instructions.read":
		s.applyInstructionReadResult(action, result)
		if scope := payloadString(action.Payload, "scope"); scope != "" {
			groupID := payloadString(action.Payload, "groupId")
			context := scope + ":" + groupID
			if context == s.settingsAgentContext && s.settingsPage == "agents" {
				s.settingsOutput = ""
			}
			return true
		}
	case "instructions.save":
		s.applyInstructionSaveResult(action, result)
	default:
		return false
	}
	return true
}

// applyInstructionActionFailure retains a page-local retry state for settings
// template reads. Task-creation reads are owned by the task-creation reader.
func (s *app) applyInstructionActionFailure(action application.Action, err error) bool {
	if action.Kind == "instructions.save" && dataBool(action.Payload, "localTaskRules") {
		s.applyTaskLocalNoteSave(action, application.ActionResult{}, err)
		return true
	}
	if action.Kind != "instructions.templates.list" || payloadString(action.Payload, "taskCreateToken") != "" {
		return false
	}
	state := s.ensureTaskNotesUI()
	generation := payloadUint64(action.Payload, "settingsTemplatesGeneration")
	if generation == 0 || generation != state.settingsTemplatesRequestGeneration ||
		payloadString(action.Payload, "settingsTemplatesPage") != "instruction-templates" ||
		payloadString(action.Payload, "settingsTemplatesHome") != s.snapshot.Home {
		return true
	}
	state.settingsTemplatesLoading = false
	state.settingsTemplatesError = "模板读取失败"
	if err != nil {
		state.settingsTemplatesError = err.Error()
	}
	return true
}
