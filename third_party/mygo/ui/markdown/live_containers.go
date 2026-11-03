package markdown

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func isEditorContainer(kind parser.Kind) bool {
	return kind == parser.List || kind == parser.Item || kind == parser.Quote
}

// Containers retain their layout while individual leaves reveal source. Empty
// items are leaves too: their literal marker is an editable source range.
func (s *EditorState) walkEditingBlocks(parent int, visit func(int) bool) bool {
	d := s.view.doc
	for i := parent + 1; i < d.Block(parent).Next; i = d.Block(i).Next {
		b := d.Block(i)
		if isEditorContainer(b.Kind) && b.Next > i+1 {
			if !s.walkEditingBlocks(i, visit) {
				return false
			}
		} else if !visit(i) {
			return false
		}
	}
	return true
}

// The Editor uses source indentation and literal prefixes rather than the
// reader's generated numbering and item margins. Every child keeps the native
// block renderer; only simple text leaves split at physical source lines.
func (s *EditorState) containerBlock(c *ui.Context, i int, base spanStyle) {
	d := s.view.doc
	b := d.Block(i)
	start, end := s.blockRange(b)
	col := ui.Column(c).Key(start).AlignSelf(ui.Stretch)
	if b.Kind == parser.List {
		col.Role(ui.RoleList)
	} else if b.Kind == parser.Item {
		col.Role(ui.RoleListItem)
	} else if b.Kind == parser.Quote {
		base.color = s.view.th.QuoteText
		col.BorderStartWidth(s.view.th.QuoteBarWidth).BorderColor(s.view.th.QuoteBar).TextColor(base.color)
	}
	col.Children(func() {
		at, composed := start, false
		for j := i + 1; j < b.Next; j = d.Block(j).Next {
			if !s.projectionCurrent() {
				return
			}
			child := d.Block(j)
			a, z := s.blockRange(child)
			// A replacement crossing child boundaries is one virtual source
			// run, never duplicated under the still-parsed original children.
			crosses := s.composing && a < s.activeEnd && z > s.activeStart &&
				(s.activeStart < a || s.activeEnd > z)
			if crosses {
				if !composed {
					if at < s.activeStart {
						s.sourceBlock(c, at, s.activeStart, 0)
					}
					s.sourceBlock(c, s.activeStart, s.activeEnd, 0)
					composed = true
				}
				at = max(at, z, s.activeEnd)
				continue
			}
			if a > at {
				s.sourceBlock(c, at, a, 0)
			}
			if isEditorContainer(child.Kind) {
				s.containerBlock(c, j, base)
			} else if s.codeKeepsCard(j) {
				s.renderedBlock(c, j, base)
			} else if s.opts.InPlaceTables && child.Kind == parser.Table {
				if s.tableKeepsGrid(j) {
					s.tableBlock(c, j, base)
				} else {
					s.sourceBlock(c, a, z, j)
				}
			} else if s.opts.RenderedLinks && (child.Kind == parser.Paragraph || child.Kind == parser.Heading) && s.blockHasLinks(j) && !s.composing {
				s.renderedBlock(c, j, base)
			} else if child.Kind == parser.Paragraph || child.Kind == parser.Heading {
				s.containerText(c, j, base)
			} else if a == s.activeStart && z == s.activeEnd {
				s.sourceBlock(c, a, z, j)
			} else {
				s.renderedBlock(c, j, base)
			}
			at = max(at, z)
		}
		if !s.projectionCurrent() {
			return
		}
		if at < end || b.Next == i+1 {
			s.sourceBlock(c, at, end, 0)
		}
	})
}

func (s *EditorState) renderedBlock(c *ui.Context, i int, base spanStyle) {
	start, end := s.blockRange(s.view.doc.Block(i))
	wrap := ui.Column(c).Key(start).AlignSelf(ui.Stretch)
	b := &editorBlock{start: s.virtualOffset(start, false), end: s.virtualOffset(end, true), element: wrap}
	s.blocks = append(s.blocks, b)
	wrap.Draw(func(_ *ui.Painter, r ui.Rect) { b.rect = r })
	wrap.Children(func() {
		prefixEnd := s.view.doc.Block(i).Start
		if prefixEnd <= start {
			if s.imageCompositionLeaf(i) {
				s.renderImageComposition(c, i, base)
			} else {
				s.view.block(c, i, false, base)
			}
			return
		}
		// Complex native children still need the source prefix which their
		// parser content excludes. Source expansion owns that prefix itself.
		th := &s.view.th
		color := base.color
		if color == (Color{}) {
			color = th.Text
		}
		ui.Row(c).AlignItems(ui.Start).AlignSelf(ui.Stretch).Children(func() {
			s.literalRun(c, start, prefixEnd, ui.Font{Family: th.Font, Size: th.FontSize}, color, th.LineHeight).Shrink(0).NoWrap()
			ui.Column(c).Grow(1).Shrink(1).MinWidth(0).Children(func() {
				if s.imageCompositionLeaf(i) {
					s.renderImageComposition(c, i, base)
				} else {
					s.view.block(c, i, false, base)
				}
			})
		})
	})
}

func (s *EditorState) containerText(c *ui.Context, i int, base spanStyle) {
	d, th := s.view.doc, &s.view.th
	b := d.Block(i)
	start, end := s.blockRange(b)
	active := start == s.activeStart && end == s.activeEnd
	if active && s.composing && (strings.Contains(s.source[s.composeStart:s.composeEnd], "\n") || strings.Contains(s.compose, "\n")) {
		// The parser still describes the committed physical rows. A virtual
		// replacement that removes/adds rows must be shaped once as a whole.
		s.sourceBlock(c, start, end, i)
		return
	}
	if b.Kind == parser.Heading {
		h := th.Headings[b.Level-1]
		base.weight, base.size, base.color = h.Weight, th.em(h.Size), h.Color
	}
	p := s.view.para(i, base)
	// Media and custom block links keep their existing native structure.
	// Source expansion of these complex leaves is intentionally local.
	complex := imagesOnly(d.Inlines(i))
	for _, pc := range p.pieces {
		if s.view.blockPiece(i, &pc) {
			complex = true
			break
		}
	}
	if complex {
		if active && !(s.opts.InPlaceImages && s.blockHasImages(i)) {
			s.sourceBlock(c, start, end, i)
		} else {
			s.renderedBlock(c, i, base)
		}
		return
	}
	font := ui.Font{Family: th.Font, Size: th.FontSize, Weight: base.weight}
	if base.size != 0 {
		font.Size = base.size
	}
	color := base.color
	if color == (Color{}) {
		color = th.Text
	}
	var spans []ui.Span
	for _, pc := range p.pieces {
		spans = append(spans, pc.spans...)
	}
	projection := s.paragraphMap(i, p)
	text, mapped := projection.text, projection.boundaries
	// Logical byte offsets, not source byte offsets, are passed to OnText.
	runeBytes := projection.bytes
	group := ui.Column(c).Key(start).AlignSelf(ui.Stretch)
	if b.Kind == parser.Heading {
		group.Role(ui.RoleHeading).Level(b.Level)
		if th.Headings[b.Level-1].Rule {
			group.BorderWidth(0, 0, 1, 0).BorderColor(th.Rule).Padding(0, 0, th.em(.3), 0)
		}
	}
	group.Children(func() {
		line, cursor := 0, 0
		spanIndex, spanStart := 0, 0
		for at := start; at < end; line++ {
			lineEnd := end
			if n := strings.IndexByte(s.source[at:end], '\n'); n >= 0 {
				lineEnd = at + n
			}
			body := b.Start
			if line < d.LineCount(i) {
				_, body = d.Line(i, line)
			}
			if line == 0 && b.Kind == parser.Heading {
				body = b.Start // the heading's own # stays in the source body
			}
			body = max(at, min(body, lineEnd))
			from := cursor
			for from < len(mapped)-1 && (mapped[from] < body || text[runeBytes[from]:runeBytes[from+1]] == "\n") {
				from++
			}
			to := from
			for to < len(mapped)-1 && mapped[to] < lineEnd && text[runeBytes[to]:runeBytes[to+1]] != "\n" {
				to++
			}
			cursor = to
			part := sliceEditorSpans(spans, runeBytes[from], runeBytes[to], &spanIndex, &spanStart)
			s.containerTextRow(c, i, at, body, lineEnd, active, font, color, part, runeBytes[from], from, to, p)
			at = lineEnd + 1
		}
	})
}

func (s *EditorState) containerTextRow(c *ui.Context, block, start, body, end int, active bool, font ui.Font, color Color, part []ui.Span, offset, runeStart, runeEnd int, p *para) {
	lh := s.view.th.LineHeight
	row := ui.Row(c).Key(start).AlignItems(ui.Start).AlignSelf(ui.Stretch)
	b := &editorBlock{start: s.virtualOffset(start, false), end: s.virtualOffset(end, true), element: row}
	s.blocks = append(s.blocks, b)
	row.Draw(func(_ *ui.Painter, r ui.Rect) { b.rect = r })
	row.Children(func() {
		// Prefixes use the body's font so heading prefixes and their content
		// have one baseline. Mapping remains exact for every prefix byte.
		if body > start {
			s.literalRun(c, start, body, font, color, lh).Shrink(0).NoWrap()
		}
		if active {
			s.literalRun(c, body, end, font, color, lh, true).Grow(1).Shrink(1).MinWidth(0)
		} else {
			if len(part) == 0 {
				s.literalRun(c, body, body, font, color, lh).Grow(1).Shrink(1).MinWidth(0)
				return
			}
			el := s.view.text(c, block, offset, part).Grow(1).Shrink(1).MinWidth(0).FontSize(font.Size).LineHeight(lh).MinHeight(font.Size * lh)
			// Inline math's object positions are relative to this physical row.
			objects := *p
			objects.objects = nil
			for k := sort.Search(len(p.objects), func(k int) bool { return p.objects[k].rune >= runeStart }); k < len(p.objects) && p.objects[k].rune < runeEnd; k++ {
				o := p.objects[k]
				o.rune -= runeStart
				objects.objects = append(objects.objects, o)
			}
			paragraphObjects(el, &objects)
		}
	})
}

// Physical rows consume paragraph spans once, including paragraphs with many
// inline formatting changes. Re-scanning all prior spans per row is quadratic.
func sliceEditorSpans(spans []ui.Span, start, end int, index, at *int) []ui.Span {
	var out []ui.Span
	for *index < len(spans) {
		sp := spans[*index]
		n := len(sp.Text)
		if start < *at+n && end > *at {
			sp.Text = sp.Text[max(0, start-*at):min(n, end-*at)]
			out = append(out, sp)
		}
		if *at+n >= end {
			break
		}
		*at += n
		*index++
	}
	return out
}

func (s *EditorState) literalRun(c *ui.Context, start, end int, font ui.Font, color Color, lh float32, after ...bool) ui.Element {
	a, z := s.virtualOffset(start, false), s.virtualOffset(end, len(after) > 0 && after[0])
	text := s.virtualText()[a:z]
	spans := []ui.Span{{Text: text, Font: font.Family, Size: font.Size, Weight: font.Weight, Color: color}}
	el := ui.RichText(c, spans...).FontSize(font.Size).LineHeight(lh).MinHeight(font.Size * lh)
	boundaries := []int{a}
	for at, ch := range text {
		boundaries = append(boundaries, a+at+utf8.RuneLen(ch))
	}
	s.observe(el, spans, boundaries, boundaries, font, lh, false)
	return el
}
