package desktop

import "silverwing-go/internal/application"

// MCP controls share only the MCP operation's pending state. Other generic
// operations retain dispatch's existing one-shot busy rejection.
func (s *app) settingsMCPOperationPending() bool {
	if !s.busy {
		return false
	}
	switch s.operationKind {
	case "codex.extensions/mcp/check", "codex.extensions/mcp/add", "codex.extensions/mcp/edit",
		"codex.extensions/mcp/set", "codex.extensions/mcp/remove", "codex.ai/mcp/authenticate":
		return true
	default:
		return false
	}
}

func (s *app) settingsMCPSwitchAction(server map[string]any, enabled bool) application.Action {
	payload := s.settingsMCPPayload(map[string]any{
		"name": firstNonEmpty(mapString(server, "name"), mapString(server, "id")),
		"file": mapString(server, "file"), "enabled": enabled,
		"desktopMCPHome": s.snapshot.Home, "desktopMCPTaskScope": s.settingsMCPTaskScope,
	})
	if pluginID := mapString(server, "pluginId"); pluginID != "" {
		payload["pluginId"] = pluginID
	}
	if s.settingsMCPTaskScope {
		task := s.currentTask()
		payload["desktopMCPTaskID"], payload["desktopMCPTaskCreatedAt"] = task.id, task.createdAt
	}
	return application.Action{Kind: "codex.extensions/mcp/set", Payload: payload}
}

// A late set result belongs to the home, task scope and server selected by the
// click. It must not replace another scope's list or start a read in that scope.
func (s *app) settingsMCPSwitchResultCurrent(action application.Action) bool {
	if s.page != "settings" || s.settingsPage != "codex-mcp" ||
		payloadString(action.Payload, "desktopMCPHome") != s.snapshot.Home ||
		dataBool(action.Payload, "desktopMCPTaskScope") != s.settingsMCPTaskScope {
		return false
	}
	cwd := ""
	if s.settingsMCPTaskScope {
		task := s.currentTask()
		cwd = task.path
		if payloadString(action.Payload, "desktopMCPTaskID") != task.id ||
			payloadString(action.Payload, "desktopMCPTaskCreatedAt") != task.createdAt {
			return false
		}
	}
	if codexAIPathKey(payloadString(action.Payload, "cwd")) != codexAIPathKey(cwd) {
		return false
	}
	for _, server := range s.settingsMCPServers {
		_, toggle, _ := settingsMCPServerCapabilities(server)
		if toggle && firstNonEmpty(mapString(server, "name"), mapString(server, "id")) == payloadString(action.Payload, "name") &&
			codexAIPathKey(mapString(server, "file")) == codexAIPathKey(payloadString(action.Payload, "file")) &&
			mapString(server, "pluginId") == payloadString(action.Payload, "pluginId") {
			return true
		}
	}
	return false
}
