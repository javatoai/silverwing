package desktop

import (
	"math"
	"strings"

	"silverwing-go/internal/media"

	"github.com/egoist/mygo/ui"
)

type pdfTextPoint struct {
	page, offset int
}

func (p pdfTextPoint) before(other pdfTextPoint) bool {
	return p.page < other.page || p.page == other.page && p.offset < other.offset
}

// PDF text offsets are rune positions with an exclusive end, matching the
// native text index. A glyph anchor is retained when extending backwards.
type pdfTextSelection struct {
	anchorStart, anchorEnd pdfTextPoint
	from, to               pdfTextPoint
	anchored               bool
}

func (s *pdfTextSelection) begin(page int, glyph media.Glyph, extending bool) {
	if !extending || !s.anchored {
		s.anchorStart = pdfTextPoint{page, glyph.Start}
		s.anchorEnd = pdfTextPoint{page, glyph.End}
		s.anchored = true
	}
	s.extend(page, glyph)
}

func (s *pdfTextSelection) extend(page int, glyph media.Glyph) {
	if !s.anchored {
		return
	}
	start := pdfTextPoint{page, glyph.Start}
	if start.before(s.anchorStart) {
		s.from, s.to = start, s.anchorEnd
	} else {
		s.from, s.to = s.anchorStart, pdfTextPoint{page, glyph.End}
	}
}

func (s *pdfTextSelection) rangeForPage(page, length int) (int, int, bool) {
	if !s.anchored || length <= 0 || page < s.from.page || page > s.to.page {
		return 0, 0, false
	}
	start, end := 0, length
	if page == s.from.page {
		start = clampInt(s.from.offset, 0, length)
	}
	if page == s.to.page {
		end = clampInt(s.to.offset, 0, length)
	}
	return start, end, end > start
}

func (s *pdfTextSelection) text(index media.TextIndex) string {
	var parts []string
	if !s.anchored {
		return ""
	}
	for page := maxInt(0, s.from.page); page <= minInt(s.to.page, len(index.Pages)-1); page++ {
		text := []rune(index.Pages[page].Text)
		if start, end, ok := s.rangeForPage(page, len(text)); ok {
			parts = append(parts, string(text[start:end]))
		}
	}
	return strings.Join(parts, "\n")
}

func selectedPDFText(m *nativeMediaState) string {
	if m == nil || m.textIndex == nil {
		return ""
	}
	return m.pdfSelection.text(*m.textIndex)
}

func clearPDFSelection(m *nativeMediaState) {
	if m != nil {
		m.pdfSelection = pdfTextSelection{}
		m.selecting = false
	}
}

func (s *app) handlePDFPageInput(m *nativeMediaState, pageIndex int, event ui.InputEvent, width, height float32, copyText func(string)) bool {
	if m == nil || m.textIndex == nil {
		return false
	}
	if event.Kind == ui.InputKeyDown && event.Key == ui.KeyC && event.Mods&ui.Cmd != 0 || event.Kind == ui.InputCommand && event.Text == "copy" {
		if text := selectedPDFText(m); text != "" && copyText != nil {
			copyText(text)
			return true
		}
		return false
	}
	switch event.Kind {
	case ui.InputPointerDown:
		if event.Button != 0 {
			return false
		}
		glyph, ok := pdfGlyphAt(*m.textIndex, pageIndex, event.X, event.Y, width, height)
		if !ok {
			clearPDFSelection(m)
			return false
		}
		m.pdfSelection.begin(pageIndex, glyph, event.Mods&ui.Shift != 0)
		m.selecting = true
		return true
	case ui.InputPointerMove, ui.InputPointerUp:
		if !m.selecting {
			return false
		}
		// MyGO captures pointer events on the initially pressed page. Convert
		// those local coordinates to the nearest currently laid-out page.
		page, glyph, ok := pdfDragGlyphAt(m, pageIndex, event.X, event.Y, width, height)
		if ok {
			m.pdfSelection.extend(page, glyph)
		}
		if event.Kind == ui.InputPointerUp {
			m.selecting = false
		}
		return true
	}
	return false
}

func pdfDragGlyphAt(m *nativeMediaState, originPage int, x, y, width, height float32) (int, media.Glyph, bool) {
	if origin, exists := m.pdfPageBounds[originPage]; exists {
		rootX, rootY := origin.X+x, origin.Y+y
		bestPage, bestDistance := -1, float32(math.MaxFloat32)
		for page, bounds := range m.pdfPageBounds {
			if bounds.W <= 0 || bounds.H <= 0 || page < 0 || page >= len(m.textIndex.Pages) || len(m.textIndex.Pages[page].Glyphs) == 0 {
				continue
			}
			dx := maxFloat32(bounds.X-rootX, maxFloat32(rootX-(bounds.X+bounds.W), 0))
			dy := maxFloat32(bounds.Y-rootY, maxFloat32(rootY-(bounds.Y+bounds.H), 0))
			distance := dx*dx + dy*dy*4
			if distance < bestDistance || distance == bestDistance && page < bestPage {
				bestPage, bestDistance = page, distance
			}
		}
		if bounds, found := m.pdfPageBounds[bestPage]; found {
			glyph, ok := pdfGlyphAt(*m.textIndex, bestPage, rootX-bounds.X, rootY-bounds.Y, bounds.W, bounds.H)
			return bestPage, glyph, ok
		}
	}
	glyph, ok := pdfGlyphAt(*m.textIndex, originPage, x, y, width, height)
	return originPage, glyph, ok
}

func pdfGlyphAt(index media.TextIndex, pageIndex int, x, y, width, height float32) (media.Glyph, bool) {
	if pageIndex < 0 || pageIndex >= len(index.Pages) || !nativeMediaFinitePositive(width) || !nativeMediaFinitePositive(height) || math.IsNaN(float64(x)) || math.IsNaN(float64(y)) {
		return media.Glyph{}, false
	}
	nx, ny := x/width, y/height
	var nearest media.Glyph
	nearestDistance := float32(math.MaxFloat32)
	for _, glyph := range index.Pages[pageIndex].Glyphs {
		dx := maxFloat32(glyph.Left-nx, maxFloat32(nx-glyph.Right, 0))
		dy := maxFloat32(glyph.Top-ny, maxFloat32(ny-glyph.Bottom, 0))
		distance := dx*dx + dy*dy*4
		if distance < nearestDistance {
			nearest, nearestDistance = glyph, distance
		}
	}
	return nearest, nearestDistance < float32(math.MaxFloat32)
}
