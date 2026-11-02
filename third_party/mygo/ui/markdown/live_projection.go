package markdown

import (
	"math"
	"sort"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

// A run is display geometry and a bidirectional map, never editable text.
// boundaries map each shaped rune boundary to a UTF-8 byte of virtualText.
type editorRun struct {
	text                       string
	spans                      []ui.Span
	boundaries, ends, units    []int
	font                       ui.Font
	lineHeight                 float32
	noWrap                     bool
	layout                     *ui.TextLayout
	width                      float32
	align, shapedAlign         ui.Align
	direction, shapedDirection ui.Direction
	rect                       ui.Rect
	element                    ui.Element
	caretStart, caretEnd       int
	tableCellBlock             int
}
type editorBlock struct {
	start, end int
	rect       ui.Rect
	element    ui.Element
}

func (s *EditorState) chooseActive() {
	if s.composing || s.dragging {
		s.activeStart, s.activeEnd = s.frozenStart, s.frozenEnd
		return
	}
	s.activeStart, s.activeEnd = -1, -1
	if s.anchor == s.caret {
		s.ensureCodeTail(s.caret)
	}
	if gap := s.focusedCodeGap(); gap != nil {
		s.activeStart, s.activeEnd = gap.start, gap.end
		return
	}
	if s.SourceMode || !s.focused || s.opts.ReadOnly {
		return
	}
	at := s.caret
	s.walkEditingBlocks(0, func(i int) bool {
		b := s.view.doc.Block(i)
		if b.Kind == parser.SourcePrefix {
			return true
		}
		start, end := s.blockRange(b)
		if at >= start && (at < end || at == len(s.source) && at == end) {
			s.activeStart, s.activeEnd = start, end
			return false
		}
		return true
	})
}

func (s *EditorState) buildBlocks(c *ui.Context) {
	if s.SourceMode {
		s.sourceBlock(c, 0, len(s.source), 0)
		return
	}
	d := s.view.doc
	at := 0
	previous := 0
	composed := false
	for i := 1; i < d.Len(); i = d.Block(i).Next {
		if !s.projectionCurrent() {
			return
		}
		b := d.Block(i)
		start, end := s.blockRange(b)
		if b.Kind == parser.SourcePrefix {
			s.renderedBlock(c, i, spanStyle{})
			at = end
			continue
		}
		if gap := s.codeGapBetween(d, previous, i); gap != nil {
			s.codeGapBlock(c, gap)
			at = gap.end
		}
		previous = i
		if s.codeKeepsCard(i) {
			if start > at {
				s.sourceBlock(c, at, start, 0)
			}
			s.renderedBlock(c, i, spanStyle{})
			at = max(at, end)
			continue
		}
		if s.opts.InPlaceTables && b.Kind == parser.Table && s.tableKeepsGrid(i) {
			if start > at {
				s.sourceBlock(c, at, start, 0)
			}
			s.tableBlock(c, i, spanStyle{})
			at = max(at, end)
			continue
		}
		if isEditorContainer(b.Kind) && (!s.composing || s.activeStart >= start && s.activeEnd <= end) {
			if start > at {
				s.sourceBlock(c, at, start, 0)
			}
			s.containerBlock(c, i, spanStyle{})
			at = max(at, end)
			continue
		}
		if s.composing && start < s.activeEnd && end > s.activeStart {
			if !composed {
				if at < s.activeStart {
					s.sourceBlock(c, at, s.activeStart, 0)
				}
				styleBlock := 0
				if start == s.activeStart && end == s.activeEnd {
					styleBlock = i
				}
				s.sourceBlock(c, s.activeStart, s.activeEnd, styleBlock)
				at = s.activeEnd
				composed = true
			}
			continue
		}
		if start > at {
			s.sourceBlock(c, at, start, 0)
		}
		if s.opts.InPlaceTables && b.Kind == parser.Table && s.tableKeepsGrid(i) {
			s.tableBlock(c, i, spanStyle{})
			at = max(at, end)
			continue
		}
		if s.opts.RenderedLinks && (b.Kind == parser.Paragraph || b.Kind == parser.Heading) && s.blockHasLinks(i) && !s.composing {
			s.renderedBlock(c, i, spanStyle{})
			at = max(at, end)
			continue
		}
		active := start == s.activeStart && end == s.activeEnd
		if active {
			s.sourceBlock(c, start, end, i)
		} else {
			block := &editorBlock{start: s.virtualOffset(start, false), end: s.virtualOffset(end, true)}
			wrap := ui.Column(c).Key(start).AlignSelf(ui.Stretch)
			block.element = wrap
			wrap.Draw(func(_ *ui.Painter, r ui.Rect) { block.rect = r })
			s.blocks = append(s.blocks, block)
			wrap.Children(func() { s.view.block(c, i, false, spanStyle{}) })
			if s.reveal && s.caret >= start && s.caret <= end {
				wrap.ScrollIntoView()
			}
		}
		at = max(at, end)
	}
	if !s.projectionCurrent() {
		return
	}
	if gap := s.codeGapAfter(d, previous); gap != nil {
		s.codeGapBlock(c, gap)
		return // reuse the tail span instead of drawing a second EOF row
	}
	codeOwnsTail := false
	if code, ok := s.editingCode(len(s.source), len(s.source)); ok && !code.closed && s.codeKeepsCard(code.block) {
		codeOwnsTail = true
	}
	if at < len(s.source) || len(s.blocks) == 0 || len(s.runs) == 0 && s.sourcePrefixEnd() > 0 || strings.HasSuffix(s.virtualText(), "\n") && !codeOwnsTail {
		s.sourceBlock(c, at, len(s.source), 0, true)
	}
}

// Container ends can be the final cell's end rather than the final pipe.
// Editing a block reveals its complete physical source lines, including
// closing delimiters and indentation the parser's content range omits.
func (s *EditorState) blockRange(b *parser.Block) (int, int) {
	start := strings.LastIndexByte(s.source[:b.Start], '\n') + 1
	end := min(b.End, len(s.source))
	if end > start && s.source[end-1] == '\n' {
		return start, end
	}
	if next := strings.IndexByte(s.source[end:], '\n'); next >= 0 {
		end += next + 1
	} else {
		end = len(s.source)
	}
	return start, end
}

// sourceBlock is shaped display text served by the root's input client.
// Splitting large source-mode documents into display chunks bounds shaping;
// no chunk installs focus, selection, keyboard or input-method handlers.
func (s *EditorState) sourceBlock(c *ui.Context, start, end, block int, tail ...bool) {
	vs, ve := s.virtualOffset(start, false), s.virtualOffset(end, true)
	if start == end && len(tail) > 0 && tail[0] && len(s.blocks) > 0 && !(len(s.blocks) == 1 && s.sourcePrefixEnd() > 0) {
		// The EOF row belongs after virtual preedit as well. Mapping both
		// sides of an insertion at EOF would display the preedit twice.
		// With no prior block (an empty document), this is the preedit's
		// only source run and must still display the virtual insertion.
		vs, ve = len(s.virtualText()), len(s.virtualText())
	}
	text := s.virtualText()[vs:ve]
	if !s.SourceMode && !(len(tail) > 0 && tail[0]) || end < len(s.source) {
		text = strings.TrimSuffix(text, "\n")
	}
	th := &s.view.th
	font := ui.Font{Family: th.Font, Size: th.FontSize}
	color := th.Text
	lh := th.LineHeight
	if s.SourceMode {
		font.Family = th.CodeFont
		lh = th.CodeLineHeight
	}
	if block > 0 {
		b := s.view.doc.Block(block)
		if b.Kind == parser.Heading {
			h := th.Headings[b.Level-1]
			font.Size = th.em(h.Size)
			font.Weight = h.Weight
			color = h.Color
		}
		if b.Kind == parser.CodeBlock || b.Kind == parser.Table || b.Kind == parser.HTMLBlock || b.Kind == parser.MathBlock {
			font.Family = th.CodeFont
			font.Size = th.em(th.CodeSize)
			lh = th.CodeLineHeight
		}
	}
	group := ui.Column(c).Key(start).AlignSelf(ui.Stretch)
	if block > 0 && s.view.doc.Block(block).Kind == parser.Heading {
		h := th.Headings[s.view.doc.Block(block).Level-1]
		if h.Rule {
			group.BorderWidth(0, 0, 1, 0).BorderColor(th.Rule).Padding(0, 0, th.em(0.3), 0)
		}
	}
	b := &editorBlock{start: vs, end: ve, element: group}
	s.blocks = append(s.blocks, b)
	group.Draw(func(_ *ui.Painter, r ui.Rect) { b.rect = r })
	group.Children(func() {
		offset := vs
		for {
			chunk := text
			{
				n, p := 0, 0
				for n < 64 && p < len(text) {
					q := strings.IndexByte(text[p:], '\n')
					if q < 0 {
						break
					}
					p += q + 1
					n++
				}
				if n == 64 && p < len(text) {
					chunk = text[:p-1]
				}
			}
			spans := []ui.Span{{Text: chunk, Font: font.Family, Size: font.Size, Weight: font.Weight, Color: color}}
			el := ui.RichText(c, spans...).AlignSelf(ui.Stretch).FontSize(font.Size).LineHeight(lh).MinHeight(font.Size * lh)
			boundaries := []int{offset}
			for k, r := range chunk {
				boundaries = append(boundaries, offset+k+utf8.RuneLen(r))
			}
			s.observe(el, spans, boundaries, boundaries, font, lh, false)
			caret := s.caret
			if s.composing {
				caret = s.composeStart + ui.UTF16ByteOffset(s.compose, s.composeSelected.End)
			}
			if s.reveal && caret >= offset && caret <= offset+len(chunk) {
				el.ScrollIntoView()
			}
			if len(chunk) == len(text) {
				break
			}
			offset += len(chunk) + 1
			text = text[len(chunk)+1:]
		}
	})
}

// observeText is called only by the shared Markdown renderer. Paragraphs
// use the parser's original source marks, including escapes and entities.
func (s *EditorState) observeText(el ui.Element, block, offset int, spans []ui.Span) {
	if !s.projectionCurrent() {
		return
	}
	d := s.view.doc
	b := d.Block(block)
	text := spanText(spans)
	n := utf8.RuneCountInString(text)
	boundaries := make([]int, n+1)
	var ends []int
	font := ui.Font{Family: s.view.th.Font, Size: s.view.th.FontSize}
	lh := s.view.th.LineHeight
	noWrap := false
	if b.Kind == parser.Heading {
		h := s.view.th.Headings[b.Level-1]
		font.Size, font.Weight = s.view.th.em(h.Size), h.Weight
	}
	switch b.Kind {
	case parser.CodeBlock, parser.HTMLBlock, parser.MathBlock:
		font.Family = s.view.th.CodeFont
		font.Size = s.view.th.em(s.view.th.CodeSize)
		lh = s.view.th.CodeLineHeight
		lines := d.Lines(block)
		prefix := utf8.RuneCountInString(lines[:min(offset, len(lines))])
		if s.lineMaps == nil {
			s.lineMaps = make(map[int][]int)
		}
		mapped, ok := s.lineMaps[block]
		if !ok {
			mapped = codeSourceBoundaries(d, block)
			s.lineMaps[block] = mapped
		}
		for i := range boundaries {
			boundaries[i] = mapped[min(prefix+i, len(mapped)-1)]
		}
		noWrap = b.Kind == parser.CodeBlock && !s.view.o.WrapCode
	default:
		var p *para
		if block < len(s.view.paras) && s.view.paras[block].ok {
			p = &s.view.paras[block].p
		} else {
			p = s.view.para(block, spanStyle{})
		}
		mapped := s.paragraphMap(block, p)
		prefix := sort.SearchInts(mapped.bytes, offset)
		ends = make([]int, n+1)
		for i := range boundaries {
			k := min(prefix+i, len(mapped.boundaries)-1)
			boundaries[i], ends[i] = mapped.boundaries[k], mapped.ends[k]
		}
	}
	if ends == nil {
		ends = append([]int(nil), boundaries...)
	}
	for i := range boundaries {
		boundaries[i] = s.virtualOffset(boundaries[i], false)
		ends[i] = s.virtualOffset(ends[i], false)
	}
	s.observe(el, spans, boundaries, ends, font, lh, noWrap)
}

type editorParagraphMap struct {
	text                    string
	bytes, boundaries, ends []int
}

// A container can show many physical rows from one paragraph. Build its full
// logical mapping once per frame instead of re-scanning all text for each row.
func (s *EditorState) paragraphMap(block int, p *para) *editorParagraphMap {
	if mapped := s.paragraphMaps[block]; mapped != nil {
		return mapped
	}
	var full strings.Builder
	for _, pc := range p.pieces {
		for _, sp := range pc.spans {
			full.WriteString(sp.Text)
		}
	}
	mapped := &editorParagraphMap{text: full.String(), bytes: []int{0}}
	for at, ch := range mapped.text {
		mapped.bytes = append(mapped.bytes, at+utf8.RuneLen(ch))
	}
	mapped.boundaries = paragraphSourceBoundaries(p, s.source, 0, len(mapped.bytes)-1, s.view.doc.Block(block).Start)
	mapped.ends = append([]int(nil), mapped.boundaries...)
	for k, m := range p.marks {
		if k > 0 && m.rune < len(mapped.ends) {
			mapped.ends[m.rune] = p.marks[k-1].end
		}
	}
	if s.paragraphMaps == nil {
		s.paragraphMaps = make(map[int]*editorParagraphMap)
	}
	s.paragraphMaps[block] = mapped
	return mapped
}

func codeSourceBoundaries(d *parser.Document, block int) []int {
	var out []int
	source := d.Source()
	for line := 0; line < d.LineCount(block); line++ {
		text, at := d.Line(block, line)
		pad := linePad(source, text, at)
		r := 0
		out = append(out, at)
		for range text {
			if r >= pad && at < len(source) {
				_, n := utf8.DecodeRuneInString(source[at:])
				at += n
			}
			out = append(out, at)
			r++
		}
	}
	if len(out) == 0 {
		out = append(out, d.Block(block).Start)
	}
	return out
}

// Walk exact source runs once. Calling para.sourceAt for every character
// repeatedly scans each run's prefix and makes long paragraphs quadratic.
func paragraphSourceBoundaries(p *para, src string, prefix, count, fallback int) []int {
	out := make([]int, count+1)
	if len(p.marks) == 0 {
		for i := range out {
			out[i] = fallback
		}
		return out
	}
	k, at, runeAt := -1, 0, 0
	for i := range out {
		want := prefix + i
		for k+1 < len(p.marks) && p.marks[k+1].rune <= want {
			k++
			at = p.marks[k].src
			runeAt = p.marks[k].rune
		}
		if k < 0 {
			out[i] = p.marks[0].src
			continue
		}
		m := p.marks[k]
		if !m.exact {
			if want == m.rune {
				out[i] = m.src
			} else {
				out[i] = m.end
			}
			continue
		}
		for runeAt < want && at < m.end {
			_, n := utf8.DecodeRuneInString(src[at:m.end])
			at += n
			runeAt++
		}
		out[i] = at
	}
	return out
}

func spanText(spans []ui.Span) string {
	var b strings.Builder
	for _, sp := range spans {
		b.WriteString(sp.Text)
	}
	return b.String()
}

func (s *EditorState) observe(el ui.Element, spans []ui.Span, boundaries, ends []int, font ui.Font, lh float32, noWrap bool) {
	r := &editorRun{text: spanText(spans), spans: spans, boundaries: boundaries, ends: ends, font: font, lineHeight: lh, noWrap: noWrap, element: el,
		caretStart: boundaries[0], caretEnd: boundaries[len(boundaries)-1]}
	r.units = append(r.units, 0)
	for _, ch := range r.text {
		r.units = append(r.units, r.units[len(r.units)-1]+utf16.RuneLen(ch))
	}
	s.runs = append(s.runs, r)
	// Draw runs before glyphs, keeping selected text fully legible.
	el.Draw(func(p *ui.Painter, box ui.Rect) {
		r.rect = box
		width := box.W
		if r.noWrap {
			width = 0
		}
		if r.layout == nil || r.width != width || r.align != r.shapedAlign || r.direction != r.shapedDirection {
			r.layout = ui.ShapeRichTextWith(r.spans, r.font, ui.TextShape{Width: width, LineHeight: r.lineHeight, Align: r.align, Direction: r.direction})
			r.width = width
			r.shapedAlign, r.shapedDirection = r.align, r.direction
		}
		if s.SourceMode && !s.composing {
			for _, hit := range s.opts.SourceHighlights {
				if hit.Start >= hit.End || hit.Start > r.boundaries[len(r.boundaries)-1] || hit.End < r.boundaries[0] {
					continue
				}
				alpha := float32(.28)
				if hit.Current {
					alpha = .48
				}
				for _, rect := range r.layout.SelectionRects(ui.TextInputRange{Start: r.unitForSource(hit.Start), End: r.unitForSource(hit.End)}) {
					rect.X, rect.Y = rect.X+box.X, rect.Y+box.Y
					p.Fill(rect, ui.RGBA(242, 178, 41, alpha), 0)
				}
			}
		}
		a, z := s.virtualSelection()
		if a != z && len(r.boundaries) > 0 && a <= r.boundaries[len(r.boundaries)-1] && z >= r.boundaries[0] {
			for _, sel := range r.layout.SelectionRects(ui.TextInputRange{Start: r.unitForSource(a), End: r.unitForSource(z)}) {
				sel.X += box.X
				sel.Y += box.Y
				p.Fill(sel, s.view.uiTh.Accent.Alpha(.23), 0)
			}
		}
	})
	el.DrawOver(func(p *ui.Painter, box ui.Rect) {
		if r.layout == nil {
			return
		}
		if s.composing {
			a, z := s.composeStart, s.composeStart+len(s.compose)
			if a <= r.boundaries[len(r.boundaries)-1] && z >= r.boundaries[0] {
				for _, sel := range r.layout.SelectionRects(ui.TextInputRange{Start: r.unitForSource(a), End: r.unitForSource(z)}) {
					p.Fill(ui.Rect{X: box.X + sel.X, Y: box.Y + sel.Y + sel.H - 1, W: sel.W, H: 1}, s.view.th.Text, 0)
				}
			}
		}
		if !s.focused || s.opts.ReadOnly {
			return
		}
		a, z := s.virtualSelection()
		if a != z {
			return
		}
		// Runs after this one may not have shaped yet in the first paint.
		// Never use the nearest-run geometry fallback to draw a caret.
		if a < r.caretStart || a > r.caretEnd {
			return
		}
		if s.caretRun(a) != r {
			return
		}
		if (p.Now().Sub(s.blink)/(530*time.Millisecond))%2 != 0 {
			return
		}
		caret := r.layout.CaretAt(ui.TextCaretPosition{Index: r.unitForSource(a), Affinity: s.affinity})
		caret.X += box.X
		caret.Y += box.Y
		caret.W = max(1, 1/p.Scale())
		p.Fill(caret, s.view.uiTh.Accent, 0)
	})
}

func (r *editorRun) unitForSource(at int) int {
	i := sort.SearchInts(r.boundaries, at)
	return r.units[min(i, len(r.units)-1)]
}
func (r *editorRun) sourceForUnit(at int) int {
	a, _ := r.sourceEdgesForUnit(at)
	return a
}
func (r *editorRun) sourceEdgesForUnit(at int) (int, int) {
	i := sort.SearchInts(r.units, at)
	if i >= len(r.units) {
		i = len(r.units) - 1
	} else if r.units[i] > at && i > 0 {
		i--
	}
	return r.boundaries[i], r.ends[i]
}
func (s *EditorState) caretRun(at int) *editorRun {
	if run, owned := s.omittedCellRun(at, true); owned {
		return run
	}
	var best *editorRun
	distance := int(^uint(0) >> 1)
	for _, r := range s.runs {
		if r.layout == nil || len(r.boundaries) == 0 {
			continue
		}
		a, z := r.caretStart, r.caretEnd
		d := max(a-at, at-z, 0)
		if d < distance {
			best, distance = r, d
		}
		if d == 0 {
			return r
		}
	}
	return best
}
func (s *EditorState) caretBounds(at int) (ui.Rect, bool) {
	r := s.caretRun(at)
	if r == nil {
		return ui.Rect{}, false
	}
	box := r.layout.CaretAt(ui.TextCaretPosition{Index: r.unitForSource(at), Affinity: s.affinity})
	box.X += r.rect.X - s.rect.X
	box.Y += r.rect.Y - s.rect.Y
	return box, true
}

// caretRun deliberately has a nearest-run fallback for navigation. Viewport
// anchoring/reveal must instead establish that geometry belongs to this source.
func (s *EditorState) ownedCaretBounds(at int) (ui.Rect, bool) {
	run := s.caretRun(at)
	if run == nil || at < run.caretStart || at > run.caretEnd {
		return ui.Rect{}, false
	}
	return s.caretBounds(at)
}

func (s *EditorState) sourceCaretRun(at int) *editorRun {
	if run, owned := s.omittedCellRun(at, false); owned {
		return run
	}
	for _, run := range s.runs {
		if at >= run.caretStart && at <= run.caretEnd {
			return run // includes an owning span not painted/shaped yet
		}
	}
	return nil
}

func (s *EditorState) displayCaretOffset() int {
	if s.composing {
		return s.composeStart + ui.UTF16ByteOffset(s.compose, s.composeSelected.End)
	}
	return s.caret
}

// A previous paint requested bootstrap only because the real source owner was
// unshaped. Already visible long runs go directly to caret-level adjustment.
func (s *EditorState) shapePendingReveal(scrollBeforeLayout float32) {
	at := -1
	if s.reveal && s.revealBootstrap {
		at = s.displayCaretOffset()
	} else if s.viewport.pending && s.viewportMatches(scrollBeforeLayout) {
		at = s.viewport.at
	}
	if at >= 0 {
		if run := s.sourceCaretRun(at); run != nil {
			run.element.ScrollIntoView()
		}
	}
}
func (s *EditorState) positionAt(x, y float32) (int, ui.TextAffinity) {
	start, end, affinity := s.positionEdges(x, y)
	if affinity == ui.TextUpstream {
		return end, affinity
	}
	return start, affinity
}
func (s *EditorState) positionEdges(x, y float32) (int, int, ui.TextAffinity) {
	cell := s.tableCellAt(x, y)
	x += s.rect.X
	y += s.rect.Y
	var inBlock *editorBlock
	for _, b := range s.blocks {
		if b.rect.Contains(x, y) {
			inBlock = b
			break
		}
	}
	var best *editorRun
	distance := float32(math.MaxFloat32)
	for _, r := range s.runs {
		if r.layout == nil {
			continue
		}
		if cell != nil && (r.tableCellBlock != cell.cell.block || r.caretStart < s.virtualOffset(cell.cell.sourceStart, false) || r.caretEnd > s.virtualOffset(cell.cell.sourceEnd, true)) {
			continue
		}
		if inBlock != nil {
			first, last := r.boundaries[0], r.boundaries[len(r.boundaries)-1]
			// A run starting at a nonempty block's end belongs to the next
			// block, including a blank line after a custom-rendered fence.
			if first < inBlock.start || last > inBlock.end || first == inBlock.end && inBlock.start < inBlock.end {
				continue
			}
		}
		dx := max(r.rect.X-x, x-r.rect.X-r.rect.W, 0)
		dy := max(r.rect.Y-y, y-r.rect.Y-r.rect.H, 0)
		d := dx*dx + dy*dy
		if d < distance {
			best, distance = r, d
		}
	}
	if best == nil {
		if cell != nil {
			at := s.virtualOffset(cell.cell.start, false)
			return at, at, ui.TextDownstream
		}
		if inBlock != nil {
			return inBlock.start, inBlock.start, ui.TextDownstream
		}
		return len(s.virtualText()), len(s.virtualText()), ui.TextUpstream
	}
	pos := best.layout.PositionAt(ui.Point{X: x - best.rect.X, Y: y - best.rect.Y})
	start, end := best.sourceEdgesForUnit(pos.Index)
	return start, end, pos.Affinity
}
func (s *EditorState) revealCaret() {
	if !s.reveal {
		s.revealBootstrap = false
		return
	}
	at := s.displayCaretOffset()
	box, ok := s.ownedCaretBounds(at)
	if !ok {
		if s.sourceCaretRun(at) != nil {
			s.revealBootstrap = true
			s.invalidate()
		}
		return
	}
	s.revealBootstrap = false
	s.reveal = false
	before := s.scroll.Y
	if box.Y < 8 {
		s.scroll.Y = max(0, s.scroll.Y+box.Y-8)
	} else if box.Y+box.H > s.rect.H-8 {
		s.scroll.Y = min(s.scroll.MaxY, s.scroll.Y+box.Y+box.H-s.rect.H+8)
	}
	if s.scroll.Y != before {
		s.invalidate()
	}
}
