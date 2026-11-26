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
	if preview.Kind != "skills.preview" {
		return application.Action{}, false
	}
	sourceID := payloadString(preview.Payload, "sourceId")
	fingerprint := payloadString(preview.Payload, "sourceFingerprint")
	if !s.skillSourceFingerprintCurrent(sourceID, fingerprint) {
		return application.Action{}, false
	}
	return application.Action{Kind: "skills.files/copy", Payload: map[string]any{
		"sourceId":          sourceID,
		"name":              payloadString(preview.Payload, "name"),
		"sourceFingerprint": fingerprint,
	}}, true
}
