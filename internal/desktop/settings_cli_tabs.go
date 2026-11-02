package desktop

import "github.com/egoist/mygo/ui"

var settingsCLITabs = []struct{ tool, label string }{
	{"git", "Git"}, {"codex", "Codex"}, {"meegle", "Meegle"}, {"genbu", "Genbu"},
}

func settingsCLIToolFromPage(page string) string {
	switch page {
	case "cli-git":
		return "git"
	case "cli-codex":
		return "codex"
	case "cli-meegle":
		return "meegle"
	case "cli-genbu":
		return "genbu"
	}
	return ""
}

func (s *app) currentSettingsCLITool() string {
	if tool := settingsCLIToolFromPage(s.settingsUnit.state.settingsPage); tool != "" {
		return tool
	}
	if tool := s.settingsUnit.state.settingsCLITool; desktopCLIJobSupported(tool) {
		return tool
	}
	return "git"
}

// CLI tabs share the parent settings page, but never its scroll or load marker.
func (s *app) settingsContentKey() string {
	key := s.settingsUnit.state.settingsPage
	if key == "cli-tools" {
		key += "/" + s.currentSettingsCLITool()
	}
	return key
}

func (s *app) renderSettingsCLITabs(c *ui.Context, p palette) {
	previous := s.currentSettingsCLITool()
	selected := 0
	for i, item := range settingsCLITabs {
		if item.tool == previous {
			selected = i
		}
	}
	tabs := ui.TabsBase(c.Key("settings-cli-tabs"), &selected, len(settingsCLITabs))
	tabs.List.FillWidth().MinHeight(36).Wrap().Gap(4).Label("命令行工具").Children(func() {
		for i, item := range settingsCLITabs {
			tab := tabs.Tab(i).Height(34).Padding(0, 12).Radius(8).Label(item.label).FocusRing(true)
			fg := p.muted
			if i == selected {
				tab.Background(p.selected)
				fg = p.blue
				if s.dark {
					fg = p.text
				}
			} else if tab.Hovered() {
				tab.Background(p.soft)
			}
			tab.Children(func() { label(c, item.label, 12, fg).FontWeight(500) })
		}
	})
	if next := settingsCLITabs[selected].tool; next != previous {
		s.navigateSettingsPage("cli-"+next, "")
	}
}
