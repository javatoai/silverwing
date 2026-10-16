package desktop

import (
	"silverwing-go/internal/application"

	"github.com/egoist/mygo/ui"
)

// Block selections copy their exact Markdown, rather than a rendered fragment.
// The same typed clipboard boundary reports Windows failures before any edit.
func (s *app) handleMarkdownBlockClipboard(c *ui.Context, state *markdownEditorState, event ui.InputEvent) bool {
	if state.doc.Snapshot().Selection == nil {
		return false
	}
	command := markdownRichClipboardCommand(event)
	if command != "copy" && command != "cut" && command != "paste" {
		return false
	}
	if state.doc.Composing() {
		return true
	}
	if command == "copy" || command == "cut" {
		if err := s.writeMarkdownRichClipboard(c, application.RichClipboardContent{Text: state.doc.SelectedSource()}); err != nil {
			s.lastError = err.Error()
			return true
		}
		if command == "cut" {
			if err := state.doc.ReplaceSelection(""); err != nil {
				s.lastError = err.Error()
			} else {
				s.commitMarkdownEditorSource(state, state.doc.Source())
			}
		}
		return true
	}
	content, err := s.readMarkdownRichClipboard(c)
	if err != nil {
		s.lastError = err.Error()
		return true
	}
	if content.Text == "" {
		return true
	}
	if err := state.doc.ReplaceSelection(content.Text); err != nil {
		s.lastError = err.Error()
	} else {
		s.commitMarkdownEditorSource(state, state.doc.Source())
	}
	return true
}
