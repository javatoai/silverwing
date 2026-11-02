package desktop

import "silverwing-go/internal/application"

func (s *app) settingsExtensionPreviewFileAction() (application.Action, bool) {
	state := s.settingsUnit.state.settingsExtensionRequests
	if state == nil || state.previewLoading || state.previewError != "" || state.previewPath == "" {
		return application.Action{}, false
	}
	preview := state.readActions["preview"]
	if !s.settingsExtensionActionRelevant(preview) {
		return application.Action{}, false
	}
	payload := map[string]any{}
	switch preview.Kind {
	case "skills.preview":
		fingerprint := payloadString(preview.Payload, "sourceFingerprint")
		if !s.skillSourceFingerprintCurrent(payloadString(preview.Payload, "sourceId"), fingerprint) {
			return application.Action{}, false
		}
		payload["sourceId"] = payloadString(preview.Payload, "sourceId")
		payload["name"] = payloadString(preview.Payload, "name")
		payload["sourceFingerprint"] = fingerprint
	case "codex.extensions/plugin/skill-preview":
		payload["sourceId"] = payloadString(preview.Payload, "sourceId")
		payload["pluginName"] = payloadString(preview.Payload, "pluginName")
		payload["skillName"] = payloadString(preview.Payload, "skillName")
	default:
		return application.Action{}, false
	}
	return application.Action{Kind: "skills.files/copy", Payload: payload}, true
}
