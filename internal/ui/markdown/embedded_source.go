package markdownui

import (
	"github.com/egoist/mygo/ui"
	"strings"
)

// ContentOnly leaves scrolling with its host, including raw metadata navigation.
func (s *ReaderState) renderEmbeddedSource(c *ui.Context) {
	source := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(s.document.Source)
	if s.revealFragment != "" {
		if at, ok := locationOffset(source, s.revealLine, 1, s.revealFragment); ok {
			s.revealLine = strings.Count(source[:at], "\n") + 1
		}
		s.revealFragment = ""
	}
	offset := 0
	hit, hasHit := s.find.revealFor(s.document.ID)
	ui.Column(c.Key("embedded-markdown-source")).FillWidth().MinWidth(0).Gap(0).Selectable().ContextMenu(func(menu *ui.Menu) { menu.EditItems(); s.native.ContextMenu(c, menu) }).Children(func() {
		for line, text := range strings.Split(source, "\n") {
			spans := highlight([]ui.Span{{Text: text}}, 0, foldedMatches(text, s.find.Query), ui.RGBA(242, 178, 41, .35))
			el := ui.RichText(c, spans...).Font("monospace").FontSize(14 * s.zoom).MinHeight(20 * s.zoom).FillWidth().MinWidth(0).Wrap()
			if s.revealLine == line+1 {
				el.ScrollIntoView()
				s.revealLine, s.revealFragment = 0, ""
			}
			if hasHit && hit.start >= offset && hit.start <= offset+len(text) {
				el.ScrollIntoView()
				s.find.revealed(hit)
				hasHit = false
			}
			offset += len(text) + 1
		}
	})
}
