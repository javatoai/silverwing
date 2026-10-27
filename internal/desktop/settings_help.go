package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

var settingsHelpImage = func() *ui.SVG {
	image, err := ui.ParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.65" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M12 11v6M12 7h.01"/></svg>`))
	if err != nil {
		panic(err)
	}
	return image
}()

// Settings help is deliberately separate from settingsMuted: that renderer is
// also used for empty states, progress and content outside the settings pages.
func settingsHelpLabel(c *ui.Context, p palette, title, help string, size float32, weight int) {
	ui.Row(c).MinWidth(0).Gap(4).AlignItems(ui.Center).Children(func() {
		if title != "" {
			label(c, title, size, p.text).FontWeight(weight).MinWidth(0).Wrap()
		}
		if strings.TrimSpace(help) != "" {
			settingsHelp(c, p, title, help)
		}
	})
}

func settingsHelp(c *ui.Context, p palette, title, help string) {
	button := ui.ButtonBase(c.Key("settings-help-" + title)).Width(24).Height(24).Shrink(0).Radius(6).Center().
		Label(title + "说明").Description(help)
	if button.Hovered() {
		button.Background(p.soft)
	}
	if button.Focused() {
		button.Border(2, p.blue)
	}
	button.Children(func() { ui.Icon(c, settingsHelpImage).Width(16).Height(16).TextColor(p.muted) })
	button.Tooltip(help)
}

func settingsHelpText(parts ...string) string {
	var help []string
	seen := make(map[string]bool)
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" && !seen[part] {
			help = append(help, part)
			seen[part] = true
		}
	}
	return strings.Join(help, "\n\n")
}
