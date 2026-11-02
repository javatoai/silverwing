package desktop

import (
	"path/filepath"
	markdownui "silverwing-go/internal/ui/markdown"
)

func (s *app) bindMarkdownViewMemory(state *markdownEditorState) {
	if state == nil || state.binding != nil || state.viewBinding != nil {
		return
	}
	task := s.taskUnit.currentTask()
	root := task.materialsPath
	relative, ok := readingRelativePath(&root, state.path)
	if !ok {
		if filepath.IsAbs(state.path) {
			return
		}
		relative = filepath.ToSlash(state.path)
	}
	state.viewBinding = &readingScrollBinding{taskPath: task.path, root: task.materialsPath, document: relative, id: "markdown-editor"}
}

func (s *app) restoreMarkdownViewMemory(state *markdownEditorState) {
	if state == nil || state.componentEditor == nil || state.viewRestored || state.viewBinding == nil {
		return
	}
	session := s.readingSession
	if session == nil || !session.loaded || session.closed {
		return
	}
	state.viewRestored = true
	if state.componentEditor.NativeState().InteractionVersion() != 0 || state.componentEditor.Snapshot().Version != 0 {
		return
	}
	position, _ := readingPositionForBinding(session, *state.viewBinding, false)
	if position.Editor != nil {
		_ = state.componentEditor.RestoreViewState(*position.Editor)
	}
}

func (s *app) captureMarkdownViewMemory(state *markdownEditorState) {
	if state == nil || state.componentEditor == nil || !state.viewRestored || state.viewBinding == nil || state.closed {
		return
	}
	session := s.readingSession
	if session == nil || !session.loaded || session.closed {
		return
	}
	view := state.componentEditor.GetViewState()
	position, _ := readingPositionForBinding(session, *state.viewBinding, false)
	if position.Editor != nil && *position.Editor == view {
		return
	}
	position, save := readingPositionForBinding(session, *state.viewBinding, true)
	if save != nil {
		copy := view
		position.Editor = &copy
		save(position)
	}
}

func boundedMarkdownEditorView(value *markdownui.EditorViewState) *markdownui.EditorViewState {
	if value == nil {
		return nil
	}
	copy := *value
	if len(copy.SourceHash) != 64 || copy.DocumentID == "" || utf16Length(copy.DocumentID) > 4096 || copy.Mode > markdownui.Source {
		return nil
	}
	copy.Anchor = clampInt(copy.Anchor, 0, 100_000_000)
	copy.Caret = clampInt(copy.Caret, 0, 100_000_000)
	copy.ScrollX = float32(clampFinite(float64(copy.ScrollX), 0, 100_000_000, 0))
	copy.ScrollY = float32(clampFinite(float64(copy.ScrollY), 0, 100_000_000, 0))
	return &copy
}
