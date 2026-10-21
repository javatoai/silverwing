package desktop

import (
	"math"
	"unicode/utf16"

	"github.com/egoist/mygo/ui"
)

// Reveal the shaped character in a wrapped reading paragraph. First let the
// native layout bring the paragraph into view; after layout its exact width and
// viewport coordinates can place the matching line inside the scroll pane.
func revealDocumentFindRichText(c *ui.Context, element ui.Element, spans []ui.Span, font ui.Font, key, text string, baseOffset int, find *documentFindState, scroll *ui.ScrollState) {
	if find == nil || !find.shouldRevealRange(key, baseOffset, baseOffset+len(text)) {
		return
	}
	hit, ok := find.current()
	if !ok {
		return
	}
	identity, navigation := find.identity, find.navigation
	local := clampInt(hit.start-baseOffset, 0, len(text))
	position := ui.TextCaretPosition{Index: len(utf16.Encode([]rune(text[:local]))), Affinity: ui.TextDownstream}
	finished := false
	element.ScrollIntoView().DrawOver(func(_ *ui.Painter, bounds ui.Rect) {
		if finished || find.identity != identity || find.navigation != navigation || bounds.W <= 0 || find.viewport.H <= 0 {
			return
		}
		finished = true
		caret := ui.ShapeRichText(spans, font, bounds.W).CaretAt(position)
		top := bounds.Y + caret.Y
		bottom := top + caret.H
		margin := minFloat32(12, find.viewport.H/4)
		delta := float32(0)
		if top < find.viewport.Y+margin {
			delta = top - (find.viewport.Y + margin)
		} else if bottom > find.viewport.Y+find.viewport.H-margin {
			delta = bottom - (find.viewport.Y + find.viewport.H - margin)
		}
		if scroll != nil && math.Abs(float64(delta)) > .5 {
			scroll.Y = clampFloat32(scroll.Y+delta, 0, scroll.MaxY)
			c.AnimationFrame()
		}
	})
}

func clampFloat32(value, low, high float32) float32 {
	return minFloat32(maxFloat32(value, low), maxFloat32(low, high))
}
