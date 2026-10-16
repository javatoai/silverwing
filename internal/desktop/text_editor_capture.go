package desktop

import codeeditor "github.com/egoist/mygo/ui/editor"

// A completed lexer job is not enough: native capture reuses the last painted
// scene. Inspect only the active owners, never cached editors on other pages.
func (s *app) captureTextEditorsReady() bool {
	if s.localSkillsViewActive() {
		state := s.skillsBrowser
		if state == nil {
			return true
		}
		if owner := state.codePreviews[state.readingKey()]; owner != nil && !captureVisibleTextEditorReady(owner.view) {
			return false
		}
		return captureMarkdownTextEditorsReady(state.markdown, false)
	}
	if s.page == "tools" && s.tools != nil {
		switch s.tools.activeTab {
		case "JSON":
			return captureVisibleTextEditorReady(&s.tools.jsonEditor.native)
		case "文本对比":
			return captureVisibleTextEditorReady(&s.tools.diffInputHistory[0].view) &&
				captureVisibleTextEditorReady(&s.tools.diffInputHistory[1].view)
		}
	}
	if (s.page == "tasks" || s.page == "archive") && s.tab == 1 {
		if view := s.currentMaterialsTextPreview(); view != nil && !captureVisibleTextEditorReady(view) {
			return false
		}
		if s.markdown != nil && s.markdown.path == s.documentPath {
			return captureMarkdownTextEditorsReady(s.markdown, s.documentEditing)
		}
	}
	return true
}

func captureVisibleTextEditorReady(view *codeeditor.State) bool {
	return view == nil || !view.SyntaxVisible() || view.SyntaxReady()
}

func captureMarkdownTextEditorsReady(state *markdownEditorState, editing bool) bool {
	if state == nil || state.closed {
		return true
	}
	if editing {
		for _, field := range state.nativeFields {
			if field != nil && (field.key == "source") == state.sourceMode && !captureVisibleTextEditorReady(&field.view) {
				return false
			}
		}
	}
	if !editing || !state.sourceMode {
		for _, diagram := range state.diagrams {
			if diagram != nil && diagram.loading {
				return false
			}
		}
		for _, image := range state.inlineImages {
			if image != nil && image.loading {
				return false
			}
		}
	}
	return true
}
