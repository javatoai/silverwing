package markdown

import (
	"math"

	"github.com/egoist/mygo/ui"
)

// Retain a visible source position when asynchronous media changes the layout.
// This is deliberately scoped to an otherwise unchanged editing viewport.
type editorViewportAnchor struct {
	valid                           bool
	pending, caretVisible           bool
	at, anchor, caret, start, end   int
	y, scroll, width, height, rootY float32
	version                         uint64
	sourceMode                      bool
	composing                       bool
	compose                         string
	composeStart, composeEnd        int
	composeSelected                 ui.TextInputRange
}

func (s *EditorState) viewportMatches(scrollBeforeLayout float32) bool {
	a := &s.viewport
	return a.valid && !s.reveal && !s.dragging && s.focused &&
		a.version == s.snapshot.Version() && a.anchor == s.anchor && a.caret == s.caret &&
		a.start == s.activeStart && a.end == s.activeEnd && a.sourceMode == s.SourceMode &&
		a.composing == s.composing && a.compose == s.compose && a.composeStart == s.composeStart &&
		a.composeEnd == s.composeEnd && a.composeSelected == s.composeSelected &&
		a.scroll == scrollBeforeLayout
}

func (s *EditorState) stabilizeViewport(scrollBeforeLayout float32) float32 {
	a := &s.viewport
	if !s.viewportMatches(scrollBeforeLayout) {
		a.pending = false
		return 0
	}
	box, ok := s.ownedCaretBounds(a.at)
	if !ok {
		if s.sourceCaretRun(a.at) != nil {
			// A resize/font/zoom change may put the previously visible owner
			// outside the painted area. Bootstrap this source anchor next frame;
			// if the user had scrolled away, it is their visible content anchor,
			// not the document caret. Scroll changes cancel viewportMatches.
			a.pending, a.scroll = true, s.scroll.Y
			s.invalidate()
		} else {
			a.pending = false
		}
		return 0
	}
	a.pending = false
	targetY := a.y
	if a.caretVisible {
		targetY = max(8, min(targetY, s.rect.H-box.H-8))
	}
	delta := box.Y - targetY
	if math.Abs(float64(delta)) < .1 {
		return 0
	}
	before := s.scroll.Y
	s.scroll.Y = max(0, min(s.scroll.MaxY, before+delta))
	if before != s.scroll.Y {
		s.invalidate()
	}
	return s.scroll.Y - before
}

func (s *EditorState) rememberViewport(correction float32) {
	if s.viewport.pending {
		return // preserve the source anchor until its actual owner is shaped
	}
	s.viewport = editorViewportAnchor{}
	if s.reveal || s.dragging || !s.focused || len(s.runs) == 0 {
		return
	}
	at := s.displayCaretOffset()
	box, ok := s.ownedCaretBounds(at)
	caretVisible := ok && box.Y-correction >= 0 && box.Y-correction+box.H <= s.rect.H
	if !caretVisible {
		// A user may scroll away from the caret. In that case retain the
		// first visible run instead of scrolling back to the selection.
		ok = false
		for _, r := range s.runs {
			if r.layout == nil || r.rect.Y+r.rect.H <= s.rect.Y || r.rect.Y >= s.rect.Y+s.rect.H {
				continue
			}
			position := r.layout.PositionAt(ui.Point{Y: max(0, s.rect.Y-r.rect.Y)})
			at = r.sourceForUnit(position.Index)
			box, ok = s.ownedCaretBounds(at)
			break
		}
	}
	if !ok {
		return
	}
	// Draw uses the frame's already-placed boxes. If this callback requested
	// a scroll correction, retain its intended Y for the following frame.
	y := box.Y - correction
	s.viewport = editorViewportAnchor{valid: true, caretVisible: caretVisible, at: at, y: y, scroll: s.scroll.Y,
		width: s.rect.W, height: s.rect.H, rootY: s.rect.Y, version: s.snapshot.Version(),
		anchor: s.anchor, caret: s.caret, start: s.activeStart, end: s.activeEnd, sourceMode: s.SourceMode,
		composing: s.composing, compose: s.compose, composeStart: s.composeStart, composeEnd: s.composeEnd, composeSelected: s.composeSelected}
}
