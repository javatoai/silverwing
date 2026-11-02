package markdown

import (
	"strings"
	"unicode/utf16"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// Keep the platform's Selection method separate from the application's
// byte-based Selection accessor. Exactly one client is installed per Editor.
type editorInputClient struct{ state *EditorState }

var _ ui.TextInputClient = (*editorInputClient)(nil)

func editorUnits(text string) int {
	n := 0
	for _, r := range text {
		n += utf16.RuneLen(r)
	}
	return n
}
func (s *EditorState) virtualText() string {
	if !s.composing {
		s.virtualCache = editorVirtualText{}
		return s.source
	}
	cached := &s.virtualCache
	if cached.source != s.source || cached.compose != s.compose || cached.start != s.composeStart || cached.end != s.composeEnd {
		*cached = editorVirtualText{source: s.source, compose: s.compose, start: s.composeStart, end: s.composeEnd,
			text: s.source[:s.composeStart] + s.compose + s.source[s.composeEnd:]}
	}
	return cached.text
}

// Prefix and body runs share this immutable projection. The full state key
// also serves native input queries between frames, so composition updates and
// source changes cannot reuse a stale frame-local string.
type editorVirtualText struct {
	source, compose, text string
	start, end            int
}

func (s *EditorState) virtualOffset(at int, after bool) int {
	if !s.composing || at < s.composeStart {
		return at
	}
	if at > s.composeEnd {
		return at + len(s.compose) - (s.composeEnd - s.composeStart)
	}
	if after || at > s.composeStart {
		return s.composeStart + len(s.compose)
	}
	return s.composeStart
}
func (s *EditorState) sourceOffset(at int, after bool) int {
	if !s.composing || at < s.composeStart {
		return at
	}
	if at > s.composeStart+len(s.compose) {
		return at - len(s.compose) + (s.composeEnd - s.composeStart)
	}
	if after {
		return s.composeEnd
	}
	return s.composeStart
}
func (s *EditorState) virtualSelection() (int, int) {
	if s.composing {
		return s.composeStart + ui.UTF16ByteOffset(s.compose, s.composeSelected.Start), s.composeStart + ui.UTF16ByteOffset(s.compose, s.composeSelected.End)
	}
	return min(s.anchor, s.caret), max(s.anchor, s.caret)
}
func (c *editorInputClient) TextForRange(r ui.TextInputRange) (string, ui.TextInputRange) {
	s := c.state
	s.sync()
	text := s.virtualText()
	a, z := ui.UTF16ByteOffset(text, max(0, min(r.Start, r.End))), ui.UTF16ByteOffset(text, max(0, max(r.Start, r.End)))
	return text[a:z], ui.TextInputRange{Start: editorUnits(text[:a]), End: editorUnits(text[:z])}
}
func (c *editorInputClient) Selection() ui.TextInputSelection {
	s := c.state
	s.sync()
	text := s.virtualText()
	a, z := s.virtualSelection()
	return ui.TextInputSelection{Range: ui.TextInputRange{Start: editorUnits(text[:a]), End: editorUnits(text[:z])}, Reversed: !s.composing && s.caret < s.anchor}
}
func (c *editorInputClient) MarkedRange() (ui.TextInputRange, bool) {
	s := c.state
	if !s.composing {
		return ui.TextInputRange{}, false
	}
	a := editorUnits(s.source[:s.composeStart])
	return ui.TextInputRange{Start: a, End: a + editorUnits(s.compose)}, true
}

func (c *editorInputClient) SetMarkedText(r *ui.TextInputRange, text string, selected ui.TextInputRange) {
	s := c.state
	s.sync()
	if s.opts.ReadOnly {
		return
	}
	text = normalizedText(text)
	virtual := s.virtualText()
	a, z := s.virtualSelection()
	if r != nil {
		a = ui.UTF16ByteOffset(virtual, max(0, min(r.Start, r.End)))
		z = ui.UTF16ByteOffset(virtual, max(0, max(r.Start, r.End)))
	}
	if s.composing && r == nil {
		a, z = s.composeStart, s.composeStart+len(s.compose)
	}
	if !s.composing && a == z {
		a, z = max(a, s.sourcePrefixEnd()), max(z, s.sourcePrefixEnd())
	}
	selected.Start = max(0, min(selected.Start, editorUnits(text)))
	selected.End = max(selected.Start, min(selected.End, editorUnits(text)))
	selected.Start = editorUnits(text[:ui.UTF16ByteOffset(text, selected.Start)])
	selected.End = editorUnits(text[:ui.UTF16ByteOffset(text, selected.End)])
	if !s.composing {
		s.closePendingIME()
		s.buffer.EndUndoGroup()
		s.composeStart, s.composeEnd = a, z
		s.freezeRange(a, z)
		s.compose = text
		s.composeSelected = selected
		s.composing = true
		if s.opts.OnCompositionChanged != nil {
			s.opts.OnCompositionChanged(true)
		}
	} else {
		start, end := min(a, s.composeStart), max(z, s.composeStart+len(s.compose))
		newStart, newEnd := s.sourceOffset(start, false), s.sourceOffset(end, true)
		prefix := virtual[start:a]
		s.compose = prefix + text + virtual[z:end]
		s.composeStart, s.composeEnd = newStart, newEnd
		s.freezeRange(newStart, newEnd)
		units := editorUnits(prefix)
		s.composeSelected = ui.TextInputRange{Start: units + selected.Start, End: units + selected.End}
	}
	s.reveal = true
	s.invalidate()
}

func (s *EditorState) freezeRange(start, end int) {
	if gap := s.focusedCodeGap(); gap != nil && start >= gap.start && end <= gap.end {
		s.frozenStart, s.frozenEnd = gap.start, gap.end
		return
	}
	// Gaps before a container's first leaf can own a literal marker. Include
	// its full physical line so a replacement crossing that gap and a child
	// keeps the unchanged prefix in the same virtual source run.
	s.frozenStart = strings.LastIndexByte(s.source[:start], '\n') + 1
	s.frozenEnd = end
	if end == start || end > 0 && s.source[end-1] != '\n' {
		if next := strings.IndexByte(s.source[end:], '\n'); next >= 0 {
			s.frozenEnd = end + next + 1
		} else {
			s.frozenEnd = len(s.source)
		}
	}
	if s.view.doc == nil {
		return
	}
	s.walkEditingBlocks(0, func(i int) bool {
		b := s.view.doc.Block(i)
		a, z := s.blockRange(b)
		if a < end && z > start || start == end && start >= a && (start < z || start == len(s.source) && start == z) {
			s.frozenStart = min(s.frozenStart, a)
			s.frozenEnd = max(s.frozenEnd, z)
		}
		return true
	})
}

func (c *editorInputClient) ReplaceText(r *ui.TextInputRange, text string) {
	s := c.state
	s.sync()
	if s.opts.ReadOnly {
		return
	}
	if !s.composing {
		a, z := s.virtualSelection()
		if a == z && a < s.sourcePrefixEnd() {
			a, z = s.sourcePrefixEnd(), s.sourcePrefixEnd()
		}
		if r != nil {
			a = ui.UTF16ByteOffset(s.source, max(0, min(r.Start, r.End)))
			z = ui.UTF16ByteOffset(s.source, max(0, max(r.Start, r.End)))
		}
		kind := editor.EditTyping
		pending := s.pendingIME && r != nil && s.pendingIMEVersion == s.snapshot.Version() &&
			min(r.Start, r.End) == s.pendingIMERange.Start && max(r.Start, r.End) == s.pendingIMERange.End
		if pending {
			kind = editor.EditComposition
			a = ui.UTF16ByteOffset(s.source, s.pendingIMEActualRange.Start)
			z = ui.UTF16ByteOffset(s.source, s.pendingIMEActualRange.End)
		} else {
			s.closePendingIME()
		}
		if s.replace(a, z, text, kind) && pending {
			s.closePendingIME()
		}
		return
	}
	if r == nil {
		s.compose = text
		s.EndComposition()
		return
	}
	virtual := s.virtualText()
	a := ui.UTF16ByteOffset(virtual, max(0, min(r.Start, r.End)))
	z := ui.UTF16ByteOffset(virtual, max(0, max(r.Start, r.End)))
	start, end := min(a, s.composeStart), max(z, s.composeStart+len(s.compose))
	newStart, newEnd := s.sourceOffset(start, false), s.sourceOffset(end, true)
	replacement := virtual[start:a] + text + virtual[z:end]
	s.composeStart, s.composeEnd, s.compose = newStart, newEnd, replacement
	s.endComposition(len(normalizedText(virtual[z:end])), true)
}
func (c *editorInputClient) UnmarkText() { c.state.endComposition(0, false) }
func (c *editorInputClient) BoundsForRange(r ui.TextInputRange) (ui.Rect, ui.TextInputRange, bool) {
	s := c.state
	s.sync()
	text := s.virtualText()
	at := ui.UTF16ByteOffset(text, max(0, min(r.Start, r.End)))
	box, ok := s.caretBounds(at)
	u := editorUnits(text[:at])
	return box, ui.TextInputRange{Start: u, End: u}, ok && box.Y+box.H > 0 && box.Y < s.rect.H
}
func (c *editorInputClient) IndexForPoint(p ui.Point) (int, bool) {
	s := c.state
	s.sync()
	if len(s.runs) == 0 {
		return 0, false
	}
	at, _ := s.positionAt(p.X, p.Y)
	text := s.virtualText()
	at = byteBoundary(text, at)
	return editorUnits(text[:at]), true
}
