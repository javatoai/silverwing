package desktop

import "github.com/egoist/mygo/ui"

// settingsToolStatusRow keeps status details compact and readable without
// making each value look like another card.
func settingsToolStatusRow(c *ui.Context, p palette, name, value string, color ui.Color) {
	ui.Row(c).FillWidth().MinWidth(0).Gap(10).AlignItems(ui.Start).Children(func() {
		label(c, name, 10, p.muted).Width(112).Shrink(0).Wrap()
		label(c, value, 11, color).Grow(1).MinWidth(0).Wrap().Selectable()
	})
}

func settingsToolAvailability(status map[string]any, p palette) (string, ui.Color) {
	installed, checked := status["installed"].(bool)
	if !checked {
		return "未检查", p.muted
	}
	if installed {
		return "可用", p.green
	}
	return "不可用", p.amber
}
