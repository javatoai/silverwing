package editor

import (
	"slices"
	"sort"

	"github.com/egoist/mygo/ui"
)

// LineStyle draws a line's text in another font, as a heading's in a
// Markdown editor: its rows are as high as the font and LineHeight make
// them, and the lines after it move down.
type LineStyle struct {
	// Line is the line it styles, from 0.
	Line int
	// Font is the line's font. Its zero fields take the editor's.
	Font ui.Font
	// LineHeight is the height of the line's rows as a multiple of its
	// font's size, as State.LineHeight; 0 takes the editor's.
	LineHeight float32
}

// appLineStyle is a style the app gave, with where its line starts, which
// edits move.
type appLineStyle struct {
	LineStyle
	at int
}

// lineStyleKey tells apart the views that lay out styled lines: one for
// each font and line height.
type lineStyleKey struct {
	font   ui.Font
	height float32
}

// setLineStyles styles the lines of styles, the last given for a line
// winning, and lays out again only the lines whose style changed.
func (ed *editor) setLineStyles(styles []LineStyle) {
	v := &ed.view
	n := v.text.LineCount()
	next := make([]appLineStyle, 0, len(styles))
	for _, s := range styles {
		if s.Line >= 0 && s.Line < n {
			next = append(next, appLineStyle{s, v.text.LineStart(s.Line)})
		}
	}
	slices.SortStableFunc(next, func(a, b appLineStyle) int { return a.Line - b.Line })
	out := next[:0]
	for _, s := range next {
		if k := len(out); k > 0 && out[k-1].Line == s.Line {
			out[k-1] = s
			continue
		}
		out = append(out, s)
	}
	if slices.Equal(v.lineStyles, out) {
		return
	}
	old := v.lineStyles
	for i, j := 0, 0; i < len(old) || j < len(out); {
		switch {
		case j >= len(out) || i < len(old) && old[i].Line < out[j].Line:
			v.forgetLines(old[i].at, old[i].at)
			i++
		case i >= len(old) || out[j].Line < old[i].Line:
			v.forgetLines(out[j].at, out[j].at)
			j++
		default:
			if old[i].LineStyle != out[j].LineStyle {
				v.forgetLines(out[j].at, out[j].at)
			}
			i++
			j++
		}
	}
	v.lineStyles = out
	v.stylesDirty = true
}

// lineStyle returns the style of line, or nil.
func (v *view) lineStyle(line int) *appLineStyle {
	if len(v.lineStyles) == 0 {
		return nil
	}
	i := sort.Search(len(v.lineStyles), func(i int) bool { return v.lineStyles[i].Line >= line })
	if i < len(v.lineStyles) && v.lineStyles[i].Line == line {
		return &v.lineStyles[i]
	}
	return nil
}

// styledView returns the view that lays out lines in style s: one of the
// editor's text, highlighter, inlays and replacements, in the font s
// makes of the editor's, with widths, shaped rows and layouts of its own.
func (v *view) styledView(s LineStyle) *view {
	f := s.Font
	if f.Family == "" {
		f.Family = v.font.Family
	}
	if f.Size <= 0 {
		f.Size = v.font.Size
	}
	if f.Weight == 0 {
		f.Weight = v.font.Weight
	}
	if !f.Italic {
		f.Italic = v.font.Italic
	}
	if f.Features == "" {
		f.Features = v.font.Features
	}
	if f.Lang == "" {
		f.Lang = v.font.Lang
	}
	if !f.Thicken {
		f.Thicken = v.font.Thicken
	}
	height := s.LineHeight
	if height <= 0 {
		height = v.lineHeight / v.font.Size
	}
	key := lineStyleKey{f, height}
	if v.styleViews == nil {
		v.styleViews = map[lineStyleKey]*view{}
	}
	child := v.styleViews[key]
	if child == nil {
		child = &view{}
		v.styleViews[key] = child
	}
	child.styleGen = v.styleGen
	if child.version != v.version || child.buf != v.buf || child.hl != v.hl {
		child.lines.m = nil
	}
	child.buf, child.text, child.version, child.hl = v.buf, v.text, v.version, v.hl
	child.repls, child.inlays = v.repls, v.inlays
	child.setStyle(f, height, v.scale, v.tabSize)
	child.setTheme(v.theme)
	child.setWrap(v.wrap, v.wrapWidth)
	return child
}

// refreshLineHeights gives the heights the rows of the styled lines, once
// the styles, the editor's font or the wraps changed, and lets go of the
// views of styles no line has any longer.
func (v *view) refreshLineHeights() {
	if !v.stylesDirty {
		return
	}
	v.stylesDirty = false
	if len(v.lineStyles) == 0 && len(v.heights.styles) == 0 && len(v.styleViews) == 0 {
		return
	}
	v.styleGen++
	hs := v.heights.styles[:0]
	for _, s := range v.lineStyles {
		child := v.styledView(s.LineStyle)
		hs = append(hs, heightEntry{s.Line, child.lineHeight})
	}
	v.heights.styles = hs
	v.heights.dirty = true
	for k, child := range v.styleViews {
		if child.styleGen != v.styleGen {
			delete(v.styleViews, k)
		}
	}
}

// moveLineStyles moves the styles through changes of the text, by where
// their lines start: settleLineStyles finds their lines once the text is
// the last of the changes'.
func (v *view) moveLineStyles(cs []change) {
	for i := range v.lineStyles {
		v.lineStyles[i].at = mapOffset(v.lineStyles[i].at, cs, biasRight)
	}
	if len(v.lineStyles) > 0 {
		v.stylesDirty = true
	}
}

// settleLineStyles finds the lines of the styles after edits moved them,
// keeping the first of those that came to one line.
func (v *view) settleLineStyles() {
	for i := range v.lineStyles {
		s := &v.lineStyles[i]
		s.Line = v.text.LineOf(s.at)
		s.at = v.text.LineStart(s.Line)
	}
	v.lineStyles = slices.CompactFunc(v.lineStyles, func(a, b appLineStyle) bool { return a.Line == b.Line })
}

// rowHeight returns the height of line's rows.
func (v *view) rowHeight(line int) float32 { return v.heights.rowHeight(line) }

// rowHeight returns the height of line's rows: its style's, or the
// view's line height.
func (h *heights) rowHeight(line int) float32 {
	if len(h.styles) == 0 {
		return h.lh
	}
	i := sort.Search(len(h.styles), func(i int) bool { return h.styles[i].line >= line })
	if i < len(h.styles) && h.styles[i].line == line {
		return h.styles[i].extra
	}
	return h.lh
}

// invalidateStyledLines has the styled lines laid out again, as the
// editor's are after the inlays changed.
func (v *view) invalidateStyledLines() {
	for _, child := range v.styleViews {
		child.lines.m = nil
	}
}
