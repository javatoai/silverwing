package desktop

import "github.com/egoist/mygo/ui"

func settingsCard(c *ui.Context, p palette, title, description string, children func()) {
	settingsCardWithActions(c, p, title, description, nil, children)
}

func settingsCardWithActions(c *ui.Context, p palette, title, description string, headerActions, children func()) {
	settingsCardLayout(c, p, title, description, headerActions, children, false)
}

// Task Codex views also use settingsCard. Opt settings pages in explicitly so
// their compact help never changes task content or its original descriptions.
func settingsHelpCard(c *ui.Context, p palette, title, description string, children func()) {
	settingsHelpCardWithActions(c, p, title, description, nil, children)
}

func settingsHelpCardWithActions(c *ui.Context, p palette, title, description string, headerActions, children func()) {
	settingsCardLayout(c, p, title, description, headerActions, children, true)
}

func settingsCardLayout(c *ui.Context, p palette, title, description string, headerActions, children func(), compactHelp bool) {
	ui.Column(c).FillWidth().Padding(16).Gap(12).Background(p.surface).Border(1, p.line).Radius(12).Children(func() {
		hasHeading := title != "" || description != ""
		heading := func() {
			if compactHelp {
				settingsHelpLabel(c, p, title, description, 14, 600)
				return
			}
			if title != "" {
				label(c, title, 14, p.text).FontWeight(600).Wrap()
			}
			if description != "" {
				label(c, description, 11, p.muted).LineHeight(1.45).Wrap()
			}
		}
		if hasHeading && headerActions == nil {
			ui.Column(c).Gap(4).Children(heading)
		} else if hasHeading || headerActions != nil {
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Start).Children(func() {
				if hasHeading {
					ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(heading)
				} else {
					ui.Spacer(c)
				}
				ui.Box(c).Shrink(0).Children(headerActions)
			})
		}
		if hasHeading || headerActions != nil {
			ui.Divider(c)
		}
		children()
	})
}

var settingsAppearanceIcon = func() *ui.SVG {
	icon, err := ui.ParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.65" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3a9 9 0 0 0 0 18h1.3a2.2 2.2 0 0 0 1.6-3.7 2.1 2.1 0 0 1 1.5-3.6H18a3 3 0 0 0 3-3c0-4.8-4-7.7-9-7.7z"/><circle cx="7.5" cy="11.5" r="1"/><circle cx="10.5" cy="7.5" r="1"/><circle cx="15.5" cy="7.5" r="1"/></svg>`))
	if err != nil {
		panic(err)
	}
	return icon
}()

func settingsAppearanceCard(c *ui.Context, p palette, children func()) {
	ui.Column(c).FillWidth().Padding(20).Gap(12).Background(p.surface).Border(1, p.line).Radius(12).Children(func() {
		ui.Row(c).Gap(11).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Width(38).Height(38).Radius(10).Background(p.selected).Center().Children(func() {
				ui.Icon(c, settingsAppearanceIcon).Width(20).Height(20).Shrink(0).TextColor(p.blue)
			})
			label(c, "外观", 16, p.text).FontWeight(600)
		})
		ui.Divider(c)
		children()
	})
}

func settingsMuted(c *ui.Context, p palette, text string) {
	label(c, text, 11, p.muted).LineHeight(1.45)
}
