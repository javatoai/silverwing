package desktop

import (
	"strings"

	markdownui "silverwing-go/internal/ui/markdown"
)

// A successful rename changes a document's location, not its source session.
// Recreate only wrappers whose callbacks captured the old identity; the native
// Buffer, selection, composition and undo history remain with the same editor.
func (s *app) rebindMaterialsMarkdownPath(state *markdownEditorState, path, root string) {
	if state == nil || state.closed {
		return
	}
	var view *markdownui.EditorViewState
	if state.componentEditor != nil {
		captured := state.componentEditor.GetViewState()
		view = &captured
	}
	closeMarkdownPreviewAssets(state)
	state.path, state.linkRoot = path, root
	if state.live != nil {
		if scope, ok := s.materialsFileScope(state.taskID); ok {
			state.liveIdentity = materialsFileScopeKey(scope)
		}
	}
	state.viewBinding = nil
	s.bindMarkdownViewMemory(state)
	if state.live != nil && view != nil {
		state.componentEditor = markdownui.AdoptEditorState(markdownui.Document{ID: markdownEditorHostID(state), Path: path}, state.live)
		view.DocumentID = markdownEditorHostID(state)
		// Source hash and native history are unchanged by a filesystem move.
		_ = state.componentEditor.RestoreViewState(*view)
	}
	// Existing live state wins over a cached restore for the old path.
	state.viewRestored = true
}

func rebindMarkdownReadingDocument(view *markdownui.EditorViewState, previous, replacement string) *markdownui.EditorViewState {
	if view == nil || previous == replacement {
		return view
	}
	suffix := "-" + normalizeIdentityPath(previous)
	if !strings.HasSuffix(view.DocumentID, suffix) {
		return view
	}
	copy := *view
	copy.DocumentID = strings.TrimSuffix(copy.DocumentID, suffix) + "-" + normalizeIdentityPath(replacement)
	return &copy
}
