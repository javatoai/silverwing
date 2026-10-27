package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

const settingsAppearanceStackBreakpoint = 960
const settingsAppearanceSelectBreakpoint = 760

func settingsAppearanceStacked(c *ui.Context) bool {
	width, _ := c.Size()
	return width < settingsAppearanceStackBreakpoint
}

func settingsAppearanceRow(c *ui.Context, p palette, title, description string, control func(stacked bool)) {
	stacked := settingsAppearanceStacked(c)
	if stacked {
		ui.Column(c).FillWidth().MinWidth(0).Gap(7).Padding(10, 0).Children(func() {
			settingsAppearanceRowLabel(c, p, title, description)
			ui.Row(c).FillWidth().MinWidth(0).Justify(ui.End).Children(func() {
				control(true)
			})
		})
		return
	}
	ui.Row(c).FillWidth().Gap(22).Padding(10, 0).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Width(230).Shrink(0).Gap(3).Children(func() {
			settingsAppearanceRowLabel(c, p, title, description)
		})
		ui.Row(c).Grow(1).MinWidth(0).Justify(ui.End).Children(func() {
			control(false)
		})
	})
}

func settingsAppearanceRowLabel(c *ui.Context, p palette, title, description string) {
	settingsHelpLabel(c, p, title, description, 12, 600)
}

func settingsAppearanceDivider(c *ui.Context) {
	ui.Divider(c)
}

// settingsAppearancePathRow lays out the title and help separately from the
// shared path control, preserving the browse and save callbacks.
func (s *app) settingsAppearancePathRow(c *ui.Context, p palette, title, description, placeholder string, value *string, directory bool, onChoose func(string)) {
	settingsAppearanceRow(c, p, title, description, func(stacked bool) {
		s.settingsPathInputBody(c, p, title, placeholder, value, directory, onChoose, stacked)
	})
}

func (s *app) settingsAppearanceTextRow(c *ui.Context, p palette, key, title, description, placeholder string, save func(string)) {
	settingsAppearanceRow(c, p, title, description, func(stacked bool) {
		s.settingsTextInputBody(c, p, key, placeholder, false, save, stacked)
	})
}

func (s *app) settingsAppearanceLocalSwitchRow(c *ui.Context, p palette, key, title, description string, value *bool) {
	settingsAppearanceRow(c, p, title, description, func(stacked bool) {
		_ = stacked
		old := *value
		ui.Switch(c.Key("settings-switch-"+key), value).Width(52).Height(32).Radius(16).Label(title)
		s.persistLocalSwitchChange(key, old, value)
	})
}

func (s *app) settingsAppearanceNestedSwitchRow(c *ui.Context, p palette, objectKey, fieldKey, key, title, description string, fallback bool) {
	current := s.configNestedBool(objectKey, fieldKey, fallback)
	value, pending := s.settingsConfigSwitchInput(key, current)
	settingsAppearanceRow(c, p, title, description, func(stacked bool) {
		_ = stacked
		switchControl := ui.Switch(c.Key("settings-switch-"+key), value).Width(52).Height(32).Radius(16).Label(title).Disabled(pending)
		switchControl.OnChange(func() {
			s.updateConfigObjectField(objectKey, fieldKey, *value)
		})
	})
}

func (s *app) settingsAppearanceThemeRow(c *ui.Context, p palette) {
	settingsAppearanceRow(c, p, "界面主题", "选择随系统变化，或固定使用浅色和深色主题。", func(stacked bool) {
		width, _ := c.Size()
		if width < settingsAppearanceSelectBreakpoint {
			s.settingsAppearanceThemeSelect(c)
			return
		}
		ui.Row(c).Gap(8).Children(func() {
			for _, option := range []struct{ value, label string }{{"SYSTEM", "跟随系统"}, {"LIGHT", "浅色"}, {"DARK", "深色"}} {
				selected := strings.EqualFold(s.configString("theme"), option.value)
				chip := ui.ButtonBase(c.Key("choice-theme-"+option.value)).Height(32).Padding(0, 16).Radius(8).Label(option.label)
				if selected {
					background := ui.Hex("#E8EDF5")
					if s.dark {
						background = ui.Hex("#273449")
					}
					chip.Background(background)
				} else {
					chip.Border(1, p.line)
					if chip.Hovered() {
						chip.Background(p.soft)
					}
				}
				chip.Children(func() { label(c, option.label, 14, p.text).NoWrap() })
				if chip.Clicked() && !selected {
					s.saveConfigField("theme", option.value)
				}
			}
		})
	})
}

func (s *app) settingsAppearanceThemeSelect(c *ui.Context) {
	options := []string{"跟随系统", "浅色", "深色"}
	selected := "跟随系统"
	switch strings.ToUpper(s.configString("theme")) {
	case "LIGHT":
		selected = "浅色"
	case "DARK":
		selected = "深色"
	}
	if ui.Select(c.Key("settings-theme-select"), &selected, options).FillWidth().Changed() {
		value := map[string]string{"跟随系统": "SYSTEM", "浅色": "LIGHT", "深色": "DARK"}[selected]
		if value != "" && !strings.EqualFold(s.configString("theme"), value) {
			s.saveConfigField("theme", value)
		}
	}
}
