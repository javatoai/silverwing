package markdown

import (
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown/parser"
	"slices"
	"strings"
)

func (s *EditorState) inlineCodeInputHasBlock(a, z int, text string) bool {
	if !s.editingInlineCodeBody(a, z) {
		return false
	}
	start, end, left, right, _ := s.inlineContainer(3, a, z)
	if !strings.ContainsAny(text, "\r\n") && !strings.Contains(s.source[left:right], "\n") {
		return false
	}
	if strings.ContainsAny(text, "\r\n") && s.blockFragment(text) {
		return true
	}
	body := s.source[left:a] + normalizedText(text) + s.source[z:right]
	if body == "" {
		return false
	} // normal empty-style deletion removes the span
	boundaries, _ := s.inlineCodeInputEdits(a, z, text)
	edits := append([]editor.Edit{{Start: a, End: z, Text: text}}, boundaries...)
	slices.SortStableFunc(edits, func(a, b editor.Edit) int { return a.Start - b.Start })
	var candidate strings.Builder
	at, delta := 0, 0
	for _, edit := range edits {
		if edit.Start < at {
			return true
		}
		candidate.WriteString(s.source[at:edit.Start])
		candidate.WriteString(normalizedText(edit.Text))
		at = edit.End
		delta += len(normalizedText(edit.Text)) - (edit.End - edit.Start)
	}
	candidate.WriteString(s.source[at:])
	drafts := slices.Clone(s.literalFenceLines())
	for _, edit := range slices.Backward(edits) {
		drafts = moveFenceDrafts(drafts, edit.Start, edit.End, len(normalizedText(edit.Text))-(edit.End-edit.Start))
	}
	// Actual parser context accounts for quote/list continuation prefixes as
	// well as plain empty lines. Raw text between the fences is not the body.
	d := parser.New(s.view.ext)
	d.SetWithPolicy(candidate.String(), s.sourcePrefixFor(candidate.String()), drafts)
	for b := 1; b < d.Len(); b++ {
		for _, in := range d.Inlines(b) {
			if in.Kind == parser.Code && in.Start == start && in.End == end+delta {
				return false
			}
		}
	}
	return true
}

func (s *EditorState) editingInlineCodeBody(a, z int) bool {
	if !s.opts.SmartEditing || s.SourceMode || s.formatPending && !s.pendingFormats[3] {
		return false
	}
	_, _, left, right, ok := s.inlineContainer(3, a, z)
	return ok && a >= left && z <= right
}

// Keep literal input in the same code span. Only its delimiter/padding ranges
// join the requested body edit in the native Buffer transaction.
func (s *EditorState) inlineCodeInputEdits(a, z int, text string) ([]editor.Edit, bool) {
	if !s.editingInlineCodeBody(a, z) {
		return nil, false
	}
	start, end, left, right, _ := s.inlineContainer(3, a, z)
	body := s.source[left:a] + normalizedText(text) + s.source[z:right]
	width := 0
	for start+width < end && s.source[start+width] == '`' {
		width++
	}
	open, close := parser.CodeSpanDelimiters(body, width)
	if body == "" {
		open, close = "", ""
	}
	var edits []editor.Edit
	if open != s.source[start:left] {
		edits = append(edits, editor.Edit{Start: start, End: left, Text: open})
	}
	if close != s.source[right:end] {
		edits = append(edits, editor.Edit{Start: right, End: end, Text: close})
	}
	return edits, true
}
