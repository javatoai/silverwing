package desktop

import "github.com/egoist/mygo/ui"

func (s *app) settingsTaskTabs(c *ui.Context, p palette) {
	settingsCard(c, p, "", "", func() {
		for index, name := range tabs {
			if index > 0 {
				ui.Divider(c)
			}
			enabled := s.taskContentTabEnabled(index)
			lastEnabled := enabled && s.enabledTaskContentTabCount() == 1
			ui.Row(c.Key("task-tab-visibility-" + readingViewForTab(index))).FillWidth().MinWidth(0).MinHeight(38).Gap(16).AlignItems(ui.Center).Children(func() {
				label(c, name, 13, p.text).Grow(1).MinWidth(0).Wrap()
				control := ui.Switch(c.Key("task-tab-toggle"), &enabled).Label(name).Disabled(lastEnabled)
				if lastEnabled {
					control.Tooltip("至少保留一个任务页签")
				}
				control.OnChange(func() { s.setTaskContentTabEnabled(index, enabled) })
			})
		}
	})
}
