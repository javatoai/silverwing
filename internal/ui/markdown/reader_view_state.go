package markdownui

import (
	"fmt"
	"math"

	code "github.com/egoist/mygo/ui/editor"
)

// ReaderViewState contains only presentation values. Hosts can retain it while
// releasing the reader's resources; it never grants access to a document path.
type ReaderViewState struct {
	DocumentID                   string
	Mode                         Mode
	Zoom                         float32
	ScrollX, ScrollY             float32
	SourceScrollX, SourceScrollY float32
}

func (s *ReaderState) GetViewState() ReaderViewState {
	if s == nil {
		return ReaderViewState{}
	}
	x, y := s.sourceScroll.X, s.sourceScroll.Y
	if s.sourceView != nil {
		position := s.sourceView.ScrollPosition()
		x, y = float32(position.X), float32(position.Y)
	}
	return ReaderViewState{DocumentID: s.document.ID, Mode: s.mode, Zoom: s.zoom,
		ScrollX: s.scroll.X, ScrollY: s.scroll.Y, SourceScrollX: x, SourceScrollY: y}
}

func (s *ReaderState) RestoreViewState(state ReaderViewState) error {
	if s == nil || s.closed {
		return ErrClosed
	}
	if state.DocumentID != s.document.ID {
		return ErrStale
	}
	if state.Mode != Preview && state.Mode != Source {
		return fmt.Errorf("unknown read-only Markdown mode %d", state.Mode)
	}
	for _, value := range []float32{state.Zoom, state.ScrollX, state.ScrollY, state.SourceScrollX, state.SourceScrollY} {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return fmt.Errorf("invalid Markdown reader viewport")
		}
	}
	if state.Zoom <= 0 {
		return fmt.Errorf("invalid Markdown reader zoom")
	}
	s.SetMode(state.Mode)
	s.zoom = min(3, max(.5, state.Zoom))
	s.scroll.X, s.scroll.Y = max(0, state.ScrollX), max(0, state.ScrollY)
	s.sourceScroll.X, s.sourceScroll.Y = max(0, state.SourceScrollX), max(0, state.SourceScrollY)
	if s.sourceView != nil {
		s.sourceView.SetScrollPosition(code.ScrollPosition{X: s.sourceScroll.X, Y: float64(s.sourceScroll.Y)})
	}
	return nil
}

func (s *ReaderState) ensureSourceView() {
	if s.sourceView != nil {
		return
	}
	s.sourceView = &code.State{Buffer: code.NewBufferPreservingLineEndings(s.document.Source), ReadOnly: true, Label: "Markdown 源码只读预览", NoScrollPastEnd: true}
	s.sourceView.SetScrollPosition(code.ScrollPosition{X: s.sourceScroll.X, Y: float64(s.sourceScroll.Y)})
}
