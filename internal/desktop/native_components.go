package desktop

import (
	"github.com/egoist/mygo/ui"
)

func compactState(c *ui.Context, text string, p palette) {
	if text == "" {
		text = "未标记"
	}
	bg, fg := p.soft, p.muted
	switch statusColorCategory(text) {
	case "planning":
		bg, fg = p.selected, p.blue
	case "development":
		bg, fg = p.purpleBg, p.purple
	case "done":
		bg, fg = p.greenBg, p.green
	case "testing":
		bg, fg = p.amberBg, p.amber
	case "failed":
		fg = p.danger
	}
	ui.Box(c).Shrink(0).Padding(2, 7).Radius(20).Border(1, p.line).Background(bg).Children(func() { label(c, text, 11, fg).NoWrap() })
}

func summaryTextButton(c *ui.Context, key, text string, color ui.Color, disabled bool) bool {
	b := ui.ButtonBase(c.Key(key)).Padding(0, 2).Label(text).Tooltip(text).Disabled(disabled)
	b.Children(func() { label(c, text, 11, color).SingleLine() })
	return b.Clicked()
}

func iconButton(c *ui.Context, key, image, name string, p palette) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Width(34).Height(34).Radius(20).Center().Label(name).Tooltip(name)
	if b.Hovered() {
		b.Background(p.soft)
	}
	b.Children(func() { icon(c, image, 18, p.text) })
	return b
}

func iconButtonDisabled(c *ui.Context, key, image, name string, p palette, disabled bool) ui.Element {
	return iconButtonRunning(c, key, image, name, p, disabled, false)
}

func iconButtonRunning(c *ui.Context, key, image, name string, p palette, disabled, running bool) ui.Element {
	b := ui.ButtonBase(c.Key(key)).Width(34).Height(34).Radius(20).Center().Label(name).Tooltip(name).Disabled(disabled)
	if b.Hovered() && !disabled {
		b.Background(p.soft)
	}
	color := p.text
	if disabled {
		color = p.muted
	}
	if image == "delete" {
		color = p.danger
	}
	b.Children(func() {
		if running {
			ui.Spinner(c).Size(18, 18).TextColor(color).Label("合并中")
		} else {
			icon(c, image, 18, color)
		}
	})
	return b
}

func iconGroup(c *ui.Context, p palette, children func()) {
	ui.Row(c).Padding(2, 3).Background(p.surface).Border(1, p.line).Radius(12).Children(children)
}

func sectionHeading(c *ui.Context, p palette, image, title, hint string) {
	ui.Column(c).FillWidth().Gap(4).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			icon(c, image, 17, p.muted)
			label(c, title, 15, p.text).FontWeight(600)
		})
		if hint != "" {
			label(c, hint, 11, p.muted).Padding(0, 25)
		}
	})
}
