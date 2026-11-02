package markdown

import (
	"errors"
	"github.com/egoist/mygo/ui"
	"math"
)

// ViewState contains no source and is safe to persist in a host's UI cache.
type ViewState struct {
	Anchor, Caret    int
	ScrollX, ScrollY float32
}
type pendingViewRestore struct {
	state            ViewState
	version          uint64
	interaction      uint64
	beforeX, beforeY float32
}

func (s *EditorState) GetViewState() ViewState {
	s.sync()
	if s.pendingView != nil {
		if s.pendingView.version == s.snapshot.Version() && s.pendingView.interaction == s.InteractionVersion() {
			return s.pendingView.state
		}
		s.pendingView = nil
	}
	return ViewState{Anchor: s.anchor, Caret: s.caret, ScrollX: s.scroll.X, ScrollY: s.scroll.Y}
}
func (s *EditorState) RestoreViewState(state ViewState) error {
	s.sync()
	if s.composing {
		return errors.New("Markdown input composition is active")
	}
	if math.IsNaN(float64(state.ScrollX)) || math.IsInf(float64(state.ScrollX), 0) || math.IsNaN(float64(state.ScrollY)) || math.IsInf(float64(state.ScrollY), 0) {
		return errors.New("invalid Markdown viewport")
	}
	state.Anchor, state.Caret = byteBoundary(s.source, state.Anchor), byteBoundary(s.source, state.Caret)
	state.ScrollX, state.ScrollY = max(0, state.ScrollX), max(0, state.ScrollY)
	s.anchor, s.caret = state.Anchor, state.Caret
	s.pendingView = &pendingViewRestore{state: state, version: s.snapshot.Version(), interaction: s.InteractionVersion(), beforeX: s.scroll.X, beforeY: s.scroll.Y}
	s.codeGapFocus.valid = false
	s.omittedTableCell = nil
	s.formatPending = false
	s.reveal, s.revealBootstrap = false, false
	s.viewport = editorViewportAnchor{}
	s.invalidate()
	return nil
}
func (s *EditorState) applyViewRestore(scrollBeforeLayout float32) bool {
	pending := s.pendingView
	if pending == nil {
		return false
	}
	s.pendingView = nil
	if pending.version != s.buffer.Version() || pending.interaction != s.InteractionVersion() || scrollBeforeLayout != pending.beforeY {
		return false
	}
	s.scroll.X = max(0, min(s.scroll.MaxX, pending.state.ScrollX))
	s.scroll.Y = max(0, min(s.scroll.MaxY, pending.state.ScrollY))
	s.reveal = false
	s.viewport = editorViewportAnchor{}
	s.affinity = ui.TextDownstream
	s.invalidate()
	return true
}

func (s *EditorState) InteractionVersion() uint64 {
	return s.interactionEpoch + s.scroll.InteractionVersion()
}

// MarkUserInteraction lets host-owned shortcuts take precedence over a delayed
// startup/view restore even when no source edit or native input was dispatched.
func (s *EditorState) MarkUserInteraction() {
	s.interactionEpoch++
	s.pendingView = nil
}
