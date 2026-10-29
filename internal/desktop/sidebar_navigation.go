package desktop

type sidebarNavigationItem struct{ page, name, image string }

func (s *app) sidebarNavigationItems() []sidebarNavigationItem {
	all := []sidebarNavigationItem{{"tasks", "任务", "tasks"}, {"requirements", "需求", "requirements"}, {"tools", "实用工具", "tools"}, {"settings", "设置", "settings"}}
	items := []sidebarNavigationItem{}
	for _, item := range all {
		if item.page != "tasks" && item.page != "settings" && !s.configNestedBool("sidebarVisibility", item.page, true) {
			continue
		}
		if item.page == "tools" && len(s.visibleDeveloperToolTabs()) == 0 {
			continue
		}
		items = append(items, item)
	}
	return items
}
func (s *app) reconcileSidebarNavigation() {
	switch s.page {
	case "skills":
		s.page, s.settingsUnit.state.settingsTab, s.settingsUnit.state.settingsPage = "settings", "skill", "skill-local"
	case "tasks", "archive", "materials", "requirements", "tools", "settings":
	default:
		s.page = "tasks"
	}
	if s.tab != 1 && s.tab != 2 && s.tab != 3 {
		s.tab = 1
	}
	if s.page == "tools" && len(s.visibleDeveloperToolTabs()) == 0 {
		s.page, s.settingsUnit.state.settingsTab, s.settingsUnit.state.settingsPage = "settings", "快捷工具", "tool-visibility"
	}
}
