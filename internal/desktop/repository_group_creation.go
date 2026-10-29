package desktop

import (
	"silverwing-go/internal/application"
)

// The created group travels with its own save action. A failed save cannot
// leave a selection request for an unrelated later configuration write.
func (s *app) saveCreatedRepositoryGroup(config map[string]any, groupID string) {
	if s.busy {
		s.lastError = "当前有操作正在执行，请等待完成后重试。"
		return
	}
	baseline, err := s.settingsConfigMap()
	if err != nil {
		s.lastError = err.Error()
		return
	}
	update, err := settingsConfigFieldsUpdate(baseline, config, []string{"groups"})
	if err != nil {
		s.lastError = err.Error()
		return
	}
	s.act("config.fields.save", "", "", "", "", "", map[string]any{
		"update": update, "selectCreatedGroup": groupID,
		"selectionHome": s.snapshot.Home, "selectionFromGroup": s.selectedServiceGroupID(),
	})
}

func (s *app) applyCreatedRepositoryGroupSelection(action application.Action) {
	if action.Kind != "config.fields.save" || s.page != "repositories" && s.page != "services" ||
		payloadString(action.Payload, "selectionHome") != s.snapshot.Home {
		return
	}
	id := payloadString(action.Payload, "selectCreatedGroup")
	if id == "" || payloadString(action.Payload, "selectionFromGroup") != s.selectedServiceGroupID() {
		return
	}
	for _, group := range s.snapshot.Groups {
		if group.ID == id {
			s.workspaceUnit.state.serviceGroupID = id
			return
		}
	}
}
