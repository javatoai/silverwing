package desktop

func (s *app) taskOpenToolEnabled(id string) bool {
	return containsString(stringArray(s.configArray("defaultWorkspaceToolIds")), id)
}

func (s *app) taskOpenToolAvailable(id string) bool {
	return id == "codex"
}
