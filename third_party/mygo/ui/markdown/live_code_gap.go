package markdown

import (
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

// A gap is a source-owned projection, not another editor. Equal start/end
// represents a writing row before an adjacent opener without storing bytes.
type editorCodeGap struct {
	paragraph                 bool
	table                     bool
	image                     bool
	imageStart, imageEnd      int
	start, end, before, after int
	version                   uint64
	rect                      ui.Rect
}

func (g *editorCodeGap) contains(at int) bool {
	return at >= g.start && (at < g.end || at == g.end && (g.start == g.end || g.after == 0))
}

type editorCodeGapFocus struct {
	gap   editorCodeGap
	valid bool
}

// Only gap-originated edits need this extra history intent. Ordinary Markdown
// source selections retain their existing history representation.
type editorCodeGapHistory struct {
	selection            editorSelection
	imageParagraph       bool
	imageStart, imageEnd int
	after                bool
}

func (s *EditorState) captureCodeGapHistory(selection editorSelection) editorCodeGapHistory {
	history := editorCodeGapHistory{selection: selection}
	if gap := s.focusedCodeGap(); gap != nil && gap.image && gap.paragraph {
		history.imageParagraph = true
		history.imageStart, history.imageEnd = gap.imageStart, gap.imageEnd
		history.after = gap.before != 0
	}
	return history
}

func (s *EditorState) restoreCodeGapHistory(history editorCodeGapHistory) {
	if history.imageParagraph && s.opts.InPlaceImages && !s.SourceMode && !s.opts.ReadOnly {
		for _, info := range s.Images() {
			if info.Start != history.imageStart || info.End != history.imageEnd {
				continue
			}
			gap := s.imageGap(info, history.after)
			if !gap.contains(s.anchor) || !gap.contains(s.caret) {
				break
			}
			gap.paragraph = true
			s.focusCodeGap(gap)
			return
		}
	}
	s.restoreCodeGap(s.caret)
}

func (s *EditorState) codeGapBetween(d *parser.Document, previous, next int) *editorCodeGap {
	if !s.opts.InPlaceCode || s.SourceMode || previous <= 0 || next <= 0 {
		return nil
	}
	a, z := d.Block(previous), d.Block(next)
	if a.Kind != parser.CodeBlock || z.Kind != parser.CodeBlock || !a.Fenced || !z.Fenced || s.view.o.Code[a.Language()] != nil || s.view.o.Code[z.Language()] != nil {
		return nil
	}
	left, right := s.readCode(d, previous), s.readCode(d, next)
	if !left.closed || !right.closed || left.end > right.start || strings.Trim(s.source[left.end:right.start], " \t\n") != "" {
		return nil
	}
	if s.composing && !s.codeGapComposition() && s.composeStart <= right.start && s.composeEnd >= left.end {
		return nil // a cross-block composition already owns this source span
	}
	return &editorCodeGap{start: left.end, end: right.start, before: previous, after: next, version: s.snapshot.Version()}
}

func (s *EditorState) codeGapComposition() bool {
	gap := s.focusedCodeGap()
	return s.composing && gap != nil && s.composeStart >= gap.start && s.composeEnd <= gap.end
}

func (s *EditorState) codeGapAfter(d *parser.Document, previous int) *editorCodeGap {
	if !s.opts.InPlaceCode || s.SourceMode || previous <= 0 {
		return nil
	}
	b := d.Block(previous)
	if s.opts.SmartEditing && s.opts.InPlaceTables && b.Kind == parser.Table {
		_, end := s.blockRange(b)
		if strings.Trim(s.source[end:], " \t\n") == "" {
			return &editorCodeGap{table: true, start: end, end: len(s.source), before: previous, version: s.snapshot.Version()}
		}
		return nil
	}
	if b.Kind != parser.CodeBlock || !b.Fenced || s.view.o.Code[b.Language()] != nil {
		return nil
	}
	code := s.readCode(d, previous)
	if !code.closed || strings.Trim(s.source[code.end:], " \t\n") != "" {
		return nil
	}
	if s.composing && !s.codeGapComposition() && s.composeStart <= len(s.source) && s.composeEnd >= code.end {
		return nil
	}
	return &editorCodeGap{start: code.end, end: len(s.source), before: previous, version: s.snapshot.Version()}
}

func (s *EditorState) focusedCodeGap() *editorCodeGap {
	focus := &s.codeGapFocus
	if !focus.valid || !(s.opts.InPlaceCode || s.opts.InPlaceImages) || s.SourceMode || s.opts.ReadOnly || focus.gap.version != s.snapshot.Version() {
		return nil
	}
	if s.focusedOmittedTableCell() != nil {
		return nil
	}
	g := &focus.gap
	if !g.contains(s.anchor) || !g.contains(s.caret) {
		return nil
	}
	return g
}

func (s *EditorState) focusCodeGap(gap *editorCodeGap) {
	s.codeGapFocus = editorCodeGapFocus{gap: *gap, valid: true}
}

func (s *EditorState) ensureCodeTail(at int) {
	if s.focusedOmittedTableCell() != nil {
		return
	}
	if !s.opts.InPlaceCode || s.SourceMode || s.opts.ReadOnly || at != len(s.source) {
		return
	}
	if focus := &s.codeGapFocus; focus.valid && focus.gap.version == s.snapshot.Version() && focus.gap.after == 0 && focus.gap.contains(at) {
		return
	}
	s.restoreCodeGap(at)
}

func (s *EditorState) restoreCodeGap(at int) {
	s.codeGapFocus.valid = false
	if s.opts.InPlaceImages && !s.SourceMode && !s.opts.ReadOnly {
		for _, info := range s.Images() {
			if _, _, ok := s.imageBlock(info); ok {
				for _, after := range []bool{false, true} {
					if gap := s.imageGap(info, after); gap.start == at {
						s.focusCodeGap(gap)
						return
					}
				}
			}
		}
	}
	if !s.opts.InPlaceCode || s.SourceMode || s.opts.ReadOnly {
		return
	}
	d := s.editingDocument()
	previous := 0
	for next := 1; next < d.Len(); next = d.Block(next).Next {
		if gap := s.codeGapBetween(d, previous, next); gap != nil && gap.contains(at) {
			s.focusCodeGap(gap)
			return
		}
		previous = next
	}
	if gap := s.codeGapAfter(d, previous); gap != nil && gap.contains(at) {
		s.focusCodeGap(gap)
	}
}

func (s *EditorState) codeGapAt(x, y float32) *editorCodeGap {
	if !(s.opts.InPlaceCode || s.opts.InPlaceImages) || s.SourceMode {
		return nil
	}
	for _, gap := range s.codeGaps {
		if gap.version == s.snapshot.Version() && gap.rect.Contains(x+s.rect.X, y+s.rect.Y) {
			return gap
		}
	}
	return nil
}

func (s *EditorState) codeGapBlock(c *ui.Context, gap *editorCodeGap) {
	s.codeGaps = append(s.codeGaps, gap)
	th := &s.view.th
	start, end := s.virtualOffset(gap.start, false), s.virtualOffset(gap.end, true)
	if gap.image && s.composing && !s.codeGapComposition() {
		start = end
	}
	text := s.virtualText()[start:end]
	if gap.start != gap.end && gap.after != 0 {
		text = strings.TrimSuffix(text, "\n")
	}
	boundaries := []int{start}
	for at, ch := range text {
		boundaries = append(boundaries, start+at+utf8.RuneLen(ch))
	}
	if text == "" {
		text, boundaries = " ", []int{start, start}
	}
	label := "在代码块之间插入内容"
	if gap.after == 0 {
		label = "在代码块后插入内容"
	}
	if gap.table {
		label = "在表格后插入内容"
	}
	if gap.image {
		label = "在图片前插入内容"
		if gap.before != 0 {
			label = "在图片后插入内容"
		}
	}
	box := ui.Column(c).Key("code-gap-" + strconv.Itoa(gap.start)).AlignSelf(ui.Stretch).MinHeight(max(th.em(th.BlockSpacing), th.FontSize*th.LineHeight)).
		Role(ui.RoleGroup).Label(label)
	block := &editorBlock{start: start, end: end, element: box}
	s.blocks = append(s.blocks, block)
	box.Draw(func(_ *ui.Painter, r ui.Rect) { gap.rect, block.rect = r, r })
	box.Children(func() {
		font := ui.Font{Family: th.Font, Size: th.FontSize}
		spans := []ui.Span{{Text: text, Font: font.Family, Size: font.Size, Color: th.Text}}
		el := ui.RichText(c, spans...).AlignSelf(ui.Stretch).FontSize(font.Size).LineHeight(th.LineHeight).MinHeight(font.Size * th.LineHeight)
		s.observe(el, spans, boundaries, boundaries, font, th.LineHeight, false)
	})
}

func (s *EditorState) codeGapInput(a, z int, text string, endpoint int) (int, int, string, int, bool) {
	gap := s.focusedCodeGap()
	if gap == nil || a < gap.start || z > gap.end || text == "" && a == z {
		return a, z, text, endpoint, false
	}
	if gap.image && gap.paragraph {
		return s.imageParagraphInput(a, z, text, endpoint, gap)
	}
	if gap.image {
		block := gap.before
		if block == 0 {
			block = gap.after
		}
		continuation := s.imageContinuation(block)
		ending := s.buffer.LineEnding()
		separator := ending + strings.TrimRight(continuation, " \t") + ending + continuation
		if gap.after != 0 {
			return a, z, text + separator, endpoint, true
		}
		suffix := ""
		if z == len(s.source) && !strings.HasSuffix(normalizedText(text), "\n") {
			suffix = ending
		}
		return a, z, separator + text + suffix, len(normalizedText(separator)) + endpoint, true
	}
	if gap.after == 0 {
		if gap.table {
			count := 0
			for at := a - 1; at >= 0 && s.source[at] == '\n' && count < 2; at-- {
				count++
			}
			prefix := strings.Repeat(s.buffer.LineEnding(), 2-count)
			return a, z, prefix + text, len(prefix) + endpoint, true
		}
		// At EOF a closer may have no physical newline yet. Only explicit
		// editing adds that prefix, before the requested outside-code text.
		if gap.start == gap.end && gap.start > 0 && s.source[gap.start-1] != '\n' {
			prefix := s.buffer.LineEnding()
			text, endpoint = prefix+text, len(prefix)+endpoint
		}
	} else if gap.start == gap.end {
		// The previous closer already owns its physical newline. Materialize
		// only this row and the separator before the next opener. Keep the
		// source caret before that separator, even for Enter/paste/IME.
		text += s.buffer.LineEnding()
	}
	return a, z, text, endpoint, true
}

func (s *EditorState) codeGapKey(key ui.Key, mods ui.Modifiers) bool {
	if !(s.opts.InPlaceCode || s.opts.InPlaceImages) || s.SourceMode || s.opts.ReadOnly || s.composing || mods != 0 || s.anchor != s.caret {
		return false
	}
	if gap := s.focusedCodeGap(); gap != nil {
		if gap.image {
			enter := gap.before != 0 && (key == ui.KeyUp || key == ui.KeyLeft || key == ui.KeyBackspace) || gap.after != 0 && (key == ui.KeyDown || key == ui.KeyRight || key == ui.KeyDelete)
			if enter {
				for _, info := range s.Images() {
					if info.Start == gap.imageStart && info.End == gap.imageEnd {
						s.report(s.SelectImage(info))
						return true
					}
				}
			}
			if (key == ui.KeyBackspace || key == ui.KeyDelete) && gap.start == gap.end {
				return true
			}
			return false
		}
		if gap.start == gap.end && (key == ui.KeyBackspace || key == ui.KeyDelete) {
			return true // a virtual writing row cannot delete either fence
		}
		if key != ui.KeyUp && key != ui.KeyDown && key != ui.KeyLeft && key != ui.KeyRight {
			return false
		}
		if gap.start != gap.end {
			if key == ui.KeyLeft || key == ui.KeyRight {
				return false
			}
			current, ok := s.ownedCaretBounds(s.caret)
			edge := gap.start
			if key == ui.KeyDown {
				edge = gap.end
				if gap.after != 0 {
					edge--
				}
			}
			last, owned := s.ownedCaretBounds(edge)
			if !ok || !owned || key == ui.KeyUp && current.Y > last.Y+.1 || key == ui.KeyDown && current.Y < last.Y-.1 {
				return false
			}
		}
		at, affinity := 0, ui.TextDownstream
		if key == ui.KeyUp || key == ui.KeyLeft {
			if gap.table {
				table := s.readTable(s.editingDocument(), gap.before)
				cell := table.rows[len(table.rows)-1][0]
				s.codeGapFocus.valid = false
				s.moveTo(cell.end, ui.TextUpstream, false)
				if cell.omitted {
					s.omittedTableCell = &editorTableHit{table: table, cell: cell}
				}
				return true
			} else {
				code := s.readCode(s.view.doc, gap.before)
				at, affinity = code.bodyEnd, ui.TextUpstream
			}
		} else if gap.after != 0 {
			code := s.readCode(s.view.doc, gap.after)
			at = code.bodyStart
		} else {
			return true // the last writing row ends at document EOF
		}
		s.codeGapFocus.valid = false
		s.moveTo(at, affinity, false)
		s.hasDesired = false
		return true
	}
	if key != ui.KeyUp && key != ui.KeyDown {
		return false
	}
	code, ok := s.editingCode(s.caret, s.caret)
	if !ok {
		return false
	}
	current, ok := s.ownedCaretBounds(s.caret)
	if !ok {
		return false
	}
	for _, gap := range s.codeGaps {
		edge, at := code.bodyEnd, gap.start
		if key == ui.KeyUp {
			if gap.after != code.block {
				continue
			}
			edge = code.bodyStart
			if gap.end > gap.start {
				at = gap.end - 1
			}
		} else if gap.before != code.block {
			continue
		}
		box, owned := s.ownedCaretBounds(edge)
		if !owned || key == ui.KeyUp && current.Y > box.Y+.1 || key == ui.KeyDown && current.Y < box.Y-.1 {
			continue
		}
		s.focusCodeGap(gap)
		s.moveTo(at, ui.TextDownstream, false)
		s.hasDesired = false
		return true
	}
	return false
}
