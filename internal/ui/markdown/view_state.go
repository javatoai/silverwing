package markdownui

import (
	"crypto/sha256"
	"fmt"
	md "github.com/egoist/mygo/ui/markdown"
	"math"
)

// EditorViewState is display state, stored by hosts separately from Markdown.
type EditorViewState struct {
	DocumentID string  `json:"documentId"`
	SourceHash string  `json:"sourceHash"`
	Anchor     int     `json:"anchor"`
	Caret      int     `json:"caret"`
	ScrollX    float32 `json:"scrollX"`
	ScrollY    float32 `json:"scrollY"`
	Mode       Mode    `json:"mode"`
}

func (s *EditorState) GetViewState() EditorViewState {
	native := s.native.GetViewState()
	if s.Mode() == Preview {
		native.ScrollX, native.ScrollY = s.reader.scroll.X, s.reader.scroll.Y
	}
	return EditorViewState{DocumentID: s.document.ID, SourceHash: s.viewSourceHash(), Anchor: native.Anchor, Caret: native.Caret, ScrollX: native.ScrollX, ScrollY: native.ScrollY, Mode: s.Mode()}
}
func (s *EditorState) RestoreViewState(state EditorViewState) error {
	if s.closed {
		return ErrClosed
	}
	if s.native.Composing() {
		return ErrComposing
	}
	if state.DocumentID != s.document.ID || state.SourceHash != s.viewSourceHash() {
		return ErrStale
	}
	if state.Mode > Source {
		return fmt.Errorf("unknown Markdown mode %d", state.Mode)
	}
	if math.IsNaN(float64(state.ScrollX)) || math.IsNaN(float64(state.ScrollY)) || math.IsInf(float64(state.ScrollX), 0) || math.IsInf(float64(state.ScrollY), 0) {
		return fmt.Errorf("invalid Markdown viewport")
	}
	native := md.ViewState{Anchor: state.Anchor, Caret: state.Caret, ScrollX: state.ScrollX, ScrollY: state.ScrollY}
	if state.Mode == Preview {
		previous := s.native.GetViewState()
		native.ScrollX, native.ScrollY = previous.ScrollX, previous.ScrollY
	}
	if err := s.native.RestoreViewState(native); err != nil {
		return err
	}
	s.mode = state.Mode
	s.native.SourceMode = state.Mode == Source
	s.link, s.insert = nil, nil
	if state.Mode == Preview {
		s.reader.scroll.X, s.reader.scroll.Y = max(0, state.ScrollX), max(0, state.ScrollY)
	}
	return nil
}

func (s *EditorState) viewSourceHash() string {
	snapshot := s.native.Buffer().Snapshot()
	if s.viewHash == "" || s.viewHashVersion != snapshot.Version() {
		s.viewHash = fmt.Sprintf("%x", sha256.Sum256([]byte(snapshot.String())))
		s.viewHashVersion = snapshot.Version()
	}
	return s.viewHash
}
