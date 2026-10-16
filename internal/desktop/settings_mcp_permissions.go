package desktop

func settingsMCPServerCapabilities(server map[string]any) (editable, toggle, authenticate bool) {
	plugin := mapString(server, "pluginId") != ""
	scope := mapString(server, "scope")
	editable = !plugin && (scope == "GLOBAL" || scope == "TASK") && mapString(server, "file") != ""
	toggle = editable || plugin
	raw := mapData(server["raw"])
	enabled, exists := raw["enabled"].(bool)
	if !exists {
		enabled = true
	}
	active, _ := server["active"].(bool)
	authenticate = !plugin && active && enabled && mapString(raw, "url") != ""
	return
}
