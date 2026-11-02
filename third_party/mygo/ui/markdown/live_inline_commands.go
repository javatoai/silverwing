package markdown

import (
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown/parser"
	"slices"
	"strings"
	"unicode"
)

var inlineCommands = []CommandKind{CommandBold, CommandItalic, CommandStrike, CommandInlineCode}
var inlineMarkers = []string{"**", "*", "~~", "`"}

func inlineCommandIndex(kind CommandKind) int {
	for i, k := range inlineCommands {
		if k == kind {
			return i
		}
	}
	return -1
}

func (s *EditorState) inlineStyles(a, z int) [4]bool {
	var styles [4]bool
	for i := range styles {
		_, _, _, _, styles[i] = s.inlineContainer(i, a, z)
	}
	return styles
}

func (s *EditorState) inlineContainer(index, a, z int) (start, end, bodyStart, bodyEnd int, found bool) {
	want := []parser.InlineKind{parser.Strong, parser.Emphasis, parser.Strikethrough, parser.Code}[index]
	d := s.editingDocument()
	for b := 1; b < d.Len(); b++ {
		for _, in := range d.Inlines(b) {
			if in.Kind != want || in.End > len(s.source) {
				continue
			}
			width := []int{2, 1, 2, 0}[index]
			if index == 3 {
				for in.Start+width < in.End && s.source[in.Start+width] == '`' {
					width++
				}
			}
			left, right := in.Start+width, in.End-width
			if index == 3 && left < right && s.source[left] == ' ' && s.source[right-1] == ' ' && strings.Trim(s.source[left:right], " ") != "" {
				left++
				right--
			}
			if a >= in.Start && z <= in.End && (a >= left && z <= right || a < right && z > left) {
				start, end, bodyStart, bodyEnd, found = in.Start, in.End, left, right, true
			}
		}
	}
	return
}

func wrapInline(text, marker string) (string, int) {
	left := len(text) - len(strings.TrimLeftFunc(text, unicode.IsSpace))
	right := len(strings.TrimRightFunc(text, unicode.IsSpace))
	if left >= right {
		return text, left
	}
	if strings.HasPrefix(marker, "`") {
		encoded := parser.EncodeCodeSpan(text[left:right])
		width := 0
		for width < len(encoded) && encoded[width] == '`' {
			width++
		}
		padding := 0
		if len(encoded)-2*width > right-left {
			padding = 1
		}
		return text[:left] + encoded + text[right:], left + width + padding
	}
	return text[:left] + marker + text[left:right] + marker + text[right:], left + len(marker)
}

func (s *EditorState) formatFragment(index, start, end int) string {
	text := s.snapshot.OriginalSlice(start, end)
	if start == end {
		return text
	}
	d := s.editingDocument()
	for b := 1; b < d.Len(); b++ {
		for _, in := range d.Inlines(b) {
			if in.Text != "" && in.Start < end && in.End > start {
				wrapped, _ := wrapInline(text, inlineMarkers[index])
				return wrapped
			}
		}
	}
	return text // only another format's delimiters, not unselected text
}

func (s *EditorState) formatSelection(index int) error {
	a, z := min(s.anchor, s.caret), max(s.anchor, s.caret)
	if a == z {
		styles := s.inlineStyles(a, z)
		if s.formatPending {
			styles = s.pendingFormats
		}
		styles[index] = !styles[index]
		if !styles[index] {
			if _, end, _, bodyEnd, ok := s.inlineContainer(index, a, z); ok && a == bodyEnd {
				s.anchor, s.caret = end, end
				s.reveal = true
			}
		}
		s.pendingFormats, s.formatPending = styles, true
		s.Focus()
		return nil
	}
	marker := inlineMarkers[index]
	s.formatPending = false
	if strings.TrimSpace(s.source[a:z]) == "" {
		return nil
	}
	_, _, _, _, singleContainer := s.inlineContainer(index, a, z)
	if strings.Contains(s.source[a:z], "\n") && !singleContainer {
		return s.formatSelectedLines(index, a, z)
	}
	start, end := a, z
	text := s.snapshot.OriginalSlice(a, z)
	selectionStart := a
	selectedBytes := z - a
	if outerStart, outerEnd, left, right, ok := s.inlineContainer(index, a, z); ok {
		if index < 3 {
			edit, first, last, err := s.inlineRemoval(index, a, z)
			if err != nil {
				return err
			}
			if s.anchor > s.caret {
				first, last = last, first
			}
			return s.applyInlineStyleEdits(index, []editor.Edit{edit}, first, last)
		}
		a, z = max(a, left), min(z, right)
		selectedBytes = z - a
		before := s.formatFragment(index, left, a)
		after := s.formatFragment(index, z, right)
		start, end = outerStart, outerEnd
		selectionStart = start + len(normalizedText(before))
		text = before + s.snapshot.OriginalSlice(a, z) + after
	} else {
		if index < 3 {
			edit, first, last, err := s.inlineAddition(index, a, z)
			if err != nil {
				return err
			}
			if s.anchor > s.caret {
				first, last = last, first
			}
			return s.applyInlineStyleEdits(index, []editor.Edit{edit}, first, last)
		}
		var offset int
		text, offset = wrapInline(text, marker)
		selectionStart = a + offset
		selectedBytes = len(strings.TrimSpace(s.source[a:z]))
	}
	selectionEnd := selectionStart + selectedBytes
	if s.anchor > s.caret {
		selectionStart, selectionEnd = selectionEnd, selectionStart
	}
	return s.applyInlineStyleEdits(index, []editor.Edit{{Start: start, End: end, Text: text}}, selectionStart, selectionEnd)
}

// Formatting units follow leaf blocks, not physical lines: a single inline
// container can span several soft lines and must be edited only once.
func (s *EditorState) formatSelectedLines(index, a, z int) error {
	type selectedSpan struct {
		start, end int
		active     bool
	}
	var spans []selectedSpan
	allActive := true
	d := s.editingDocument()
	for b := 1; b < d.Len(); b++ {
		kind := d.Block(b).Kind
		if kind != parser.Paragraph && kind != parser.Heading && kind != parser.TableCell || d.LineCount(b) == 0 {
			continue
		}
		_, first := d.Line(b, 0)
		text, last := d.Line(b, d.LineCount(b)-1)
		start, end := max(a, first), min(z, last+len(text)-linePad(s.source, text, last))
		if start >= end || strings.TrimSpace(s.source[start:end]) == "" {
			continue
		}
		_, _, _, _, active := s.inlineContainer(index, start, end)
		spans = append(spans, selectedSpan{start, end, active})
		allActive = allActive && active
	}
	if len(spans) == 0 {
		return nil
	}
	var edits []editor.Edit
	selectionStart, selectionEnd, delta := 0, 0, 0
	for n, span := range spans {
		first, last := span.start+delta, span.end+delta
		if allActive {
			if index < 3 {
				edit, from, to, err := s.inlineRemoval(index, span.start, span.end)
				if err != nil {
					return err
				}
				edits = append(edits, edit)
				first, last = from+delta, to+delta
				delta += len(normalizedText(edit.Text)) - (edit.End - edit.Start)
				if n == 0 {
					selectionStart = first
				}
				selectionEnd = last
				continue
			}
			start, end, left, right, ok := s.inlineContainer(index, span.start, span.end)
			if !ok {
				continue
			}
			from, to := max(span.start, left), min(span.end, right)
			before, after := s.formatFragment(index, left, from), s.formatFragment(index, to, right)
			text := before + s.snapshot.OriginalSlice(from, to) + after
			edits = append(edits, editor.Edit{Start: start, End: end, Text: text})
			first = start + delta + len(normalizedText(before))
			last = first + to - from
			delta += len(normalizedText(text)) - (end - start)
		} else if !span.active {
			if index < 3 {
				edit, from, to, err := s.inlineAddition(index, span.start, span.end)
				if err != nil {
					return err
				}
				edits = append(edits, edit)
				first, last = from+delta, to+delta
				delta += len(normalizedText(edit.Text)) - (edit.End - edit.Start)
			} else {
				original := s.snapshot.OriginalSlice(span.start, span.end)
				text, offset := wrapInline(original, inlineMarkers[index])
				edits = append(edits, editor.Edit{Start: span.start, End: span.end, Text: text})
				first = span.start + delta + offset
				last = first + len(strings.TrimSpace(s.source[span.start:span.end]))
				delta += len(normalizedText(text)) - (span.end - span.start)
			}
		}
		if n == 0 {
			selectionStart = first
		}
		selectionEnd = last
	}
	if len(edits) == 0 {
		return nil
	}
	if s.anchor > s.caret {
		selectionStart, selectionEnd = selectionEnd, selectionStart
	}
	return s.applyInlineStyleEdits(index, edits, selectionStart, selectionEnd)
}

// Place new delimiters around non-whitespace text. Existing closing markers
// move in the same transaction when a committed batch ends with whitespace.
func (s *EditorState) formatInput(a, z int, text string, endpoint int) (string, int, []editor.Edit) {
	if !s.opts.SmartEditing || !s.formatPending || s.SourceMode || text == "" {
		return text, endpoint, nil
	}
	if _, ok := s.editingCode(a, z); ok {
		return text, endpoint, nil
	}
	current := s.inlineStyles(a, z)
	wanted := s.pendingFormats
	multiline := strings.ContainsAny(text, "\r\n")
	if multiline {
		wanted = [4]bool{}
	}
	type styleAt struct {
		index, start, end, left, right int
		open, close                    string
	}
	var active []styleAt
	for i, on := range current {
		if !on {
			continue
		}
		start, end, left, right, ok := s.inlineContainer(i, a, z)
		if ok {
			active = append(active, styleAt{i, start, end, left, right, s.source[start:left], s.source[right:end]})
		}
	}
	left := len(text) - len(strings.TrimLeftFunc(text, unicode.IsSpace))
	right := len(strings.TrimRightFunc(text, unicode.IsSpace))
	pureSpace := left >= right
	closeEnd := z
	if right < len(text) || pureSpace || multiline {
		closeEnd = s.inlineClosingPosition(z)
	}
	var extra []editor.Edit
	if closeEnd > z {
		extra = []editor.Edit{{Start: z, End: closeEnd}}
	}
	slices.SortFunc(active, func(a, b styleAt) int { return a.right - b.right })
	closeCurrent, closeWanted := "", ""
	for _, style := range active {
		if !wanted[style.index] {
			closeCurrent += style.close
		} else if closeEnd > z && style.right >= z && style.end <= closeEnd {
			closeWanted += style.close
		}
	}
	slices.SortFunc(active, func(a, b styleAt) int { return a.start - b.start })
	reopenCurrent := ""
	for _, style := range active {
		if !wanted[style.index] && !(closeEnd > z && style.right >= z && style.end <= closeEnd) {
			reopenCurrent += style.open
		}
	}
	if multiline {
		prefix, suffix := closeCurrent, reopenCurrent
		if s.blockFragment(text) {
			before, after := s.fragmentSeparators(a, max(z, closeEnd), text)
			prefix += before
			suffix = after + suffix
		} else {
			if prefix != "" && left == 0 {
				prefix += "<!-- -->"
			}
			if suffix != "" && right == len(text) {
				suffix = "<!-- -->" + suffix
			}
		}
		return prefix + text + suffix, len(prefix) + endpoint, extra
	}
	if pureSpace {
		return closeCurrent + closeWanted + text + reopenCurrent, len(closeCurrent) + len(closeWanted) + endpoint, extra
	}
	openNew, closeNew := "", ""
	for i := range current {
		if !current[i] && s.pendingFormats[i] {
			if i == 3 {
				open, _ := parser.CodeSpanDelimiters(text[left:right], 1)
				openNew += open
			} else {
				openNew += inlineMarkers[i]
			}
		}
	}
	for i := len(current) - 1; i >= 0; i-- {
		if !current[i] && s.pendingFormats[i] {
			if i == 3 {
				_, close := parser.CodeSpanDelimiters(text[left:right], 1)
				closeNew += close
			} else {
				closeNew += inlineMarkers[i]
			}
		}
	}
	result := closeCurrent + text[:left] + openNew + text[left:right] + closeNew + closeWanted + text[right:] + reopenCurrent
	mapped := len(closeCurrent) + endpoint
	if endpoint >= left {
		mapped += len(openNew)
	}
	if endpoint > right {
		mapped += len(closeNew) + len(closeWanted)
	}
	return result, mapped, extra
}

func (s *EditorState) finishInlineTyping() {
	if !s.formatPending || s.SourceMode || !s.opts.SmartEditing {
		return
	}
	at := s.inlineClosingPosition(s.caret)
	s.anchor, s.caret = at, at
	s.formatPending = false
}

func (s *EditorState) inlineClosingPosition(at int) int {
	for repeat := 0; repeat < len(inlineCommands); repeat++ {
		before := at
		for i := len(inlineCommands) - 1; i >= 0; i-- {
			if _, end, _, bodyEnd, ok := s.inlineContainer(i, at, at); ok && at == bodyEnd {
				at = end
			}
		}
		if at == before {
			break
		}
	}
	return at
}

func (s *EditorState) inlineTypingPosition(at int) int {
	if s.inlineStyles(at, at) == s.pendingFormats {
		return at
	}
	d := s.editingDocument()
	candidate := at
	for repeat := 0; repeat < len(inlineCommands); repeat++ {
		before := candidate
		for b := 1; b < d.Len(); b++ {
			for _, in := range d.Inlines(b) {
				for index, kind := range []parser.InlineKind{parser.Strong, parser.Emphasis, parser.Strikethrough, parser.Code} {
					if !s.pendingFormats[index] || in.Kind != kind || in.End != candidate {
						continue
					}
					width := []int{2, 1, 2, 0}[index]
					if index == 3 {
						for in.Start+width < in.End && s.source[in.Start+width] == '`' {
							width++
						}
					}
					candidate = in.End - width
				}
			}
		}
		if candidate == before {
			break
		}
	}
	if candidate != at && s.inlineStyles(candidate, candidate) == s.pendingFormats {
		return candidate
	}
	return at
}

func (s *EditorState) removeEmptyInlineEdit(a, z int, text string) (int, int, string) {
	for repeat := 0; repeat < len(inlineCommands); repeat++ {
		changed := false
		for index := range inlineCommands {
			start, end, left, right, ok := s.inlineContainer(index, a, z)
			if !ok || a < left || z > right {
				continue
			}
			remaining := s.snapshot.OriginalSlice(left, a) + text + s.snapshot.OriginalSlice(z, right)
			if strings.TrimSpace(remaining) != "" {
				continue
			}
			a, z, text = start, end, remaining
			changed = true
			break
		}
		if !changed {
			break
		}
	}
	return a, z, text
}
