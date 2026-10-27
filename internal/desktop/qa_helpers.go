package desktop

import "silverwing-go/internal/application"

func mapBool(value map[string]any, key string, fallback bool) bool {
	v, ok := value[key].(bool)
	if !ok {
		return fallback
	}
	return v
}
func (s *app) qaGroupTaskCount(id string) int {
	count := 0
	for _, t := range s.tasks {
		if t.groupID == id {
			count++
		}
	}
	return count
}
func (s *app) saveCreatedTaskGroup(config map[string]any, id string) {
	s.settingsGroupID = id
	s.saveSettingsConfigMap(config, "groups")
}
func normalizeTaskCreationWorkspaceTools(options []taskCreationWorkspaceToolOption, configured []string) []taskCreationWorkspaceToolOption {
	for _, option := range options {
		if option.ID == "codex" {
			return []taskCreationWorkspaceToolOption{option}
		}
	}
	if containsString(configured, "codex") {
		return []taskCreationWorkspaceToolOption{{ID: "codex", DisplayName: "Codex", Reason: "尚未读取工具状态"}}
	}
	return []taskCreationWorkspaceToolOption{}
}

func (s *app) openTaskInCodex(t task) {
	scope, ok := s.taskMenuScopeFor(t)
	if ok && !s.busy {
		s.dispatchTaskContextAction(application.Action{Kind: "codex.run", TaskID: t.id, Path: t.path}, &scope)
	}
}

func (s *app) reconcileQASettingsNavigation() {
	for _, section := range settingsSections {
		if section.name == s.settingsTab {
			for _, page := range section.pages {
				if page.key == s.settingsPage {
					return
				}
			}
			s.settingsPage = section.pages[0].key
			return
		}
	}
	s.settingsTab, s.settingsPage = settingsSections[0].name, settingsSections[0].pages[0].key
}
