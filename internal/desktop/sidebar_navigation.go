package desktop

type sidebarNavigationItem struct{ page, name, image string }

func (s *app) showsTagNavigation() bool {
	return s.configBool("tagEnabled", true) && len(s.readConfigArray("groups")) > 0
}

func (s *app) sidebarNavigationItems() []sidebarNavigationItem {
	all := []sidebarNavigationItem{{"tasks", "任务", "tasks"}, {"requirements", "需求", "requirements"}, {"tools", "研发工具", "tools"}, {"production", "生产发布", "branch"}, {"services", "服务", "repo"}, {"settings", "设置", "settings"}}
	items := make([]sidebarNavigationItem, 0, len(all))
	showTools := len(s.visibleDeveloperToolTabs()) > 0
	for _, item := range all {
		if item.page != "tasks" && item.page != "settings" && !s.configNestedBool("sidebarVisibility", item.page, true) {
			continue
		}
		if item.page == "production" && !s.configBool("tagEnabled", true) {
			continue
		}
		if item.page == "tools" && !showTools {
			continue
		}
		items = append(items, item)
	}
	return items
}

func (s *app) reconcileSidebarNavigation() {
	if s.page == "skills" {
		s.page, s.settingsUnit.state.settingsTab, s.settingsUnit.state.settingsPage = "settings", "skill", "skill-local"
		s.settingsUnit.state.settingsLoaded = ""
	}
	if s.loading {
		return
	}
	if s.page == "production" && !s.configBool("tagEnabled", true) {
		s.page = "tasks"
	}
	if s.page == "tags" {
		s.page = "tasks"
		s.tab = taskTagTab
	}
	if s.page == "tools" && len(s.visibleDeveloperToolTabs()) == 0 {
		s.page = "settings"
		s.settingsUnit.state.settingsTab = "快捷工具"
		s.settingsUnit.state.settingsPage = "tool-visibility"
		s.settingsUnit.state.settingsLoaded = ""
	}
}
