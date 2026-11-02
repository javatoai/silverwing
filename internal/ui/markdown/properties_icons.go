package markdownui

import "github.com/egoist/mygo/ui"

var propertyIcons = func() map[string]*ui.SVG {
	paths := map[string]string{"chevron-down": `<path d="m3 5 5 5 5-5"/>`, "chevron-right": `<path d="m5 3 5 5-5 5"/>`, "plus": `<path d="M8 2v12M2 8h12"/>`, "copy": `<rect x="5" y="5" width="8" height="9" rx="1.5"/><path d="M10 5V3a1 1 0 0 0-1-1H3a1 1 0 0 0-1 1v7a1 1 0 0 0 1 1h2"/>`}
	icons := make(map[string]*ui.SVG, len(paths))
	paths["x"] = `<path d="m4 4 8 8M12 4l-8 8"/>`
	for name, path := range paths {
		icons[name] = ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">` + path + `</svg>`))
	}
	return icons
}()
