package desktop

import "github.com/egoist/mygo/ui"

type palette struct{ bg, surface, side, soft, line, text, muted, blue, selected, green, greenBg, amber, amberBg, danger, purple, purpleBg ui.Color }

func colors(dark bool) palette {
	if dark {
		return palette{
			ui.Hex("#0C111B"), ui.Hex("#121925"), ui.Hex("#151E2B"), ui.Hex("#1A2433"), ui.Hex("#263244"), ui.Hex("#E6E1E5"), ui.Hex("#CAC4D0"), ui.Hex("#60A5FA"), ui.Hex("#1E3A8A"), ui.Hex("#86EFAC"), ui.Hex("#163C30"), ui.Hex("#FCD28A"), ui.Hex("#403426"), ui.Hex("#F87171"), ui.Hex("#D0BCFF"), ui.Hex("#302447"),
		}
	}
	return palette{
		ui.Hex("#F4F7FB"), ui.Hex("#FCFDFF"), ui.Hex("#F7F9FC"), ui.Hex("#F0F3F8"), ui.Hex("#E5EAF1"), ui.Hex("#1D1B20"), ui.Hex("#49454F"), ui.Hex("#356AE6"), ui.Hex("#E8EEFF"), ui.Hex("#24744D"), ui.Hex("#ECF7F0"), ui.Hex("#926116"), ui.Hex("#FFF5E2"), ui.Hex("#DC2626"), ui.Hex("#5D469F"), ui.Hex("#F0EBFA"),
	}
}

func setTheme(c *ui.Context, dark bool) palette {
	p := colors(dark)
	t := ui.LightTheme()
	if dark {
		t = ui.DarkTheme()
	}
	t.Background, t.Surface, t.SurfaceHover, t.SurfacePressed = p.bg, p.surface, p.soft, p.selected
	t.Text, t.TextMuted, t.Border = p.text, p.muted, p.line
	t.Danger = p.danger
	t.Accent, t.AccentHover, t.AccentPressed = p.blue, ui.Hex("#285CD3"), ui.Hex("#2454C6")
	t.AccentText = ui.Hex("#FFFFFF")
	t.FontSize, t.Radius, t.Spacing = 14, 8, 4
	c.SetTheme(t)
	return p
}

// A single line-icon vocabulary; every icon inherits the current text color.
var iconPaths = map[string]string{
	"wing":             `<path d="M3 4l18 5-9 4-4 8-5-17z"/><path d="M3 4l9 9"/>`,
	"tasks":            `<circle cx="5" cy="16" r="3"/><circle cx="19" cy="16" r="3"/><circle cx="12" cy="5" r="3"/><path d="M5 10v-3h3M19 10v-3h-3"/>`,
	"requirements":     `<path d="M6 3h9l4 4v14H6zM14 3v5h5M9 12h7M9 16h5"/>`,
	"archive":          `<rect x="3" y="3" width="18" height="5" rx="1"/><path d="M5 8v13h14V8M9 12h6"/>`,
	"tools":            `<path d="M14 6l4-4a6 6 0 0 0-8 7L3 16a3 3 0 0 0 5 5l7-7a6 6 0 0 0 7-8l-4 4z"/>`,
	"repo":             `<rect x="4" y="3" width="16" height="18" rx="2"/><path d="M8 3v18M12 8h4M12 12h4"/>`,
	"tag":              `<path d="M3 3h8l10 10-8 8L3 11z"/><circle cx="7.5" cy="7.5" r="1"/>`,
	"skills":           `<path d="M4 4h5a3 3 0 1 1 6 0h5v5a3 3 0 1 0 0 6v5h-5a3 3 0 1 0-6 0H4v-5a3 3 0 1 1 0-6z"/>`,
	"settings":         `<path d="M9 3h6l1 3 3 1 2 5-2 5-3 1-1 3H9l-1-3-3-1-2-5 2-5 3-1z"/><circle cx="12" cy="12" r="3"/>`,
	"search":           `<circle cx="10.5" cy="10.5" r="6.5"/><path d="M16 16l5 5"/>`,
	"plus":             `<path d="M12 5v14M5 12h14"/>`,
	"minus":            `<path d="M5 12h14"/>`,
	"delete":           `<path d="M3 6h18M9 6V3h6v3M5 6l1 15h12l1-15M10 10v7M14 10v7"/>`,
	"chevron":          `<path d="M6 9l6 6 6-6"/>`,
	"chevron-left":     `<path d="M15 6l-6 6 6 6"/>`,
	"chevron-right":    `<path d="M9 6l6 6-6 6"/>`,
	"arrow":            `<path d="M5 12h14M14 7l5 5-5 5"/>`,
	"refresh":          `<path d="M20 8a8 8 0 1 0 0 8M20 3v5h-5"/>`,
	"sun":              `<circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M2 12h2M20 12h2M5 5l1 1M18 18l1 1M5 19l1-1M18 6l1-1"/>`,
	"moon":             `<path d="M20 15A9 9 0 0 1 9 4a9 9 0 1 0 11 11z"/>`,
	"branch":           `<circle cx="6" cy="5" r="2"/><circle cx="6" cy="19" r="2"/><circle cx="18" cy="6" r="2"/><path d="M6 7v10M8 17c8 0 10-4 10-9"/>`,
	"folder":           `<path d="M3 7V4h6l2 3h10v13H3z"/>`,
	"folder-open":      `<path d="M3 8V4h6l2 3h9v4M3 8v12h16l3-10H8L5 20"/>`,
	"folder-plus":      `<path d="M3 7V4h6l2 3h10v13H3zM12 11v6M9 14h6"/>`,
	"file-plus":        `<path d="M5 3h9l5 5v13H5zM14 3v5h5M12 11v7M8.5 14.5h7"/>`,
	"collapse-folders": `<path d="M8 3h13v13M3 8h13v13H3zM6 14h7"/>`,
	"file":             `<path d="M5 3h9l5 5v13H5zM14 3v5h5M9 13h6M9 17h4"/>`,
	"file-markdown":    `<path d="M5 3h9l5 5v13H5zM14 3v5h5M8 17v-5l2 2 2-2v5M15 12v5m-2-2 2 2 2-2"/>`,
	"file-json":        `<path d="M5 3h9l5 5v13H5zM14 3v5h5M10 11H9v3l-1 1 1 1v3h1M14 11h1v3l1 1-1 1v3h-1"/>`,
	"file-sql":         `<path d="M5 3h9l5 5v13H5zM14 3v5h5"/><ellipse cx="12" cy="12" rx="4" ry="1.5"/><path d="M8 12v5c0 2 8 2 8 0v-5M8 14.5c0 2 8 2 8 0"/>`,
	"file-code":        `<path d="M5 3h9l5 5v13H5zM14 3v5h5M10 12l-3 3 3 3M14 12l3 3-3 3"/>`,
	"file-image":       `<path d="M5 3h9l5 5v13H5zM14 3v5h5M6 19l5-5 3 3 2-2 3 3"/><circle cx="9" cy="11" r="1"/>`,
	"file-pdf":         `<path d="M5 3h9l5 5v13H5zM14 3v5h5M8 17l3-6c0 4 2 6 5 6-4-2-7-1-8 0"/>`,
	"file-unknown":     `<path d="M5 3h9l5 5v13H5zM14 3v5h5"/>`,
	"code":             `<path d="M8 6l-6 6 6 6M16 6l6 6-6 6M14 3l-4 18"/>`,
	"copy":             `<rect x="8" y="8" width="13" height="13" rx="2"/><path d="M16 8V3H3v13h5"/>`,
	"check":            `<path d="M5 12l4 4L19 6"/>`,
	"clock":            `<circle cx="12" cy="12" r="9"/><path d="M12 7v5l3 2"/>`,
	"more":             `<circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/>`,
	"terminal":         `<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M7 9l3 3-3 3M13 15h4"/>`,
	"user":             `<circle cx="12" cy="8" r="3"/><path d="M5 21v-3a7 7 0 0 1 14 0v3"/>`,
	"pin":              `<path d="M8 3h8l-1 6 3 4v2H6v-2l3-4zM12 15v7"/>`,
	"close":            `<path d="M6 6l12 12M18 6L6 18"/>`,
	"expand":           `<path d="M8 3H3v5M16 3h5v5M21 16v5h-5M8 21H3v-5"/>`,
	"sort":             `<path d="M4 6h16M4 11h11M4 16h6"/>`,
	"commit":           `<path d="M3 12h5M16 12h5"/><circle cx="12" cy="12" r="4"/>`,
	"push":             `<path d="M12 18V6M8 10l4-4 4 4M5 3h14"/>`,
	"cloud":            `<path d="M7 18H5a4 4 0 0 1 0-8 7 7 0 0 1 13-1 4.5 4.5 0 0 1 1 9h-2M12 21V11M8 15l4-4 4 4"/>`,
	"edit":             `<path d="M4 17l1 4 4-1L21 8l-5-5zM14 5l5 5"/>`,
	"save":             `<path d="M4 3h13l4 4v14H4zM8 3v6h8V3M8 21v-7h9v7"/>`,
	"undo":             `<path d="M9 14 4 9l5-5"/><path d="M4 9h10a6 6 0 0 1 0 12h-3"/>`,
	"redo":             `<path d="m15 14 5-5-5-5"/><path d="M20 9H10a6 6 0 0 0 0 12h3"/>`,
	"git":              `<path d="M12 2l10 10-10 10L2 12zM8 8l8 8M10 10v7"/><circle cx="8" cy="8" r="1"/><circle cx="16" cy="16" r="1"/>`,
	"launch":           `<path d="M14 3h7v7M21 3l-9 9M10 4H4v17h17v-6"/>`,
}
var icons = func() map[string]*ui.SVG {
	m := make(map[string]*ui.SVG)
	for name, path := range iconPaths {
		svg, err := ui.ParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.65" stroke-linecap="round" stroke-linejoin="round">` + path + `</svg>`))
		if err != nil {
			panic(err)
		}
		m[name] = svg
	}
	return m
}()

func icon(c *ui.Context, name string, size float32, color ui.Color) {
	ui.Icon(c, icons[name]).Width(size).Height(size).Shrink(0).TextColor(color)
}

func label(c *ui.Context, text string, size float32, color ui.Color) ui.Element {
	return ui.Text(c, text).FontSize(size).TextColor(color)
}

func action(c *ui.Context, key, name, text string, p palette, primary bool) bool {
	accessibleName := text
	if accessibleName == "" {
		accessibleName = map[string]string{"moon": "切换深色主题", "sun": "切换浅色主题", "refresh": "刷新", "more": "任务操作", "copy": "复制", "plus": "添加"}[name]
	}
	b := ui.ButtonBase(c.Key(key)).Row().Gap(7).Height(34).Padding(0, 16).Radius(20).AlignItems(ui.Center).TextColor(p.text).Label(accessibleName)
	if text == "" {
		b.Tooltip(accessibleName)
	}
	bg, fg := p.surface, p.text
	if primary {
		bg, fg = ui.Hex("#356AE6"), ui.Hex("#FFFFFF")
	} else {
		b.Border(1, p.line)
	}
	if b.Hovered() {
		if primary {
			bg = ui.Hex("#2454C6")
		} else {
			bg = p.soft
		}
	}
	b.Background(bg).Children(func() {
		if name != "" {
			icon(c, name, 17, fg)
		}
		if text != "" {
			label(c, text, 13, fg).NoWrap()
		}
	})
	return b.Clicked()
}

func pill(c *ui.Context, text string, p palette) {
	bg, fg := p.soft, p.muted
	switch text {
	case "进行中":
		bg, fg = p.selected, p.blue
	case "待验收":
		bg, fg = p.amberBg, p.amber
	case "已完成", "干净", "已就绪":
		bg, fg = p.greenBg, p.green
	}
	ui.Box(c).Padding(3, 8).Radius(5).Background(bg).Children(func() { label(c, text, 11, fg).NoWrap() })
}
