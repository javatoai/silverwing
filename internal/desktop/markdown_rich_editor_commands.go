package desktop

import (
	"errors"

	"silverwing-go/internal/editor"

	"github.com/egoist/mygo/ui"
)

// Every action that hides or persists an editor captures the same committed
// source. IME preedit and failed drafts stay with their current field.
func (s *app) captureMarkdownEditorInput(state *markdownEditorState) error {
	if state == nil || state.doc == nil || state.closed || state.readOnly || state.detached {
		return nil
	}
	if !s.markdownEditorIsCurrent(state) {
		return errors.New("Markdown 编辑上下文已失效，请重新读取文件")
	}
	if state.doc.Composing() {
		return editor.ErrComposing
	}
	for _, rich := range allMarkdownRichFields(state) {
		if rich.input.composing {
			return editor.ErrComposing
		}
		if rich.input.dirty || rich.input.errorText != "" {
			return errors.New("富文本输入尚未提交，请处理编辑错误后再继续；当前草稿已保留")
		}
	}
	if !s.flushMarkdownNativeFields(state) {
		if state.doc.Composing() {
			return editor.ErrComposing
		}
		return errors.New("源码输入尚未提交，请处理编辑错误后再继续；当前草稿已保留")
	}
	if !state.doc.Flush() {
		message := state.doc.Snapshot().Error
		if message == "" {
			message = "Markdown 输入尚未提交，当前草稿已保留"
		}
		return errors.New(message)
	}
	if state.doc.Composing() {
		return editor.ErrComposing
	}
	s.commitMarkdownEditorSource(state, state.doc.Source())
	return nil
}

func (s *app) registerMarkdownEditorShortcuts(c *ui.Context, state *markdownEditorState) {
	c.OnShortcut(ui.Cmd, ui.KeyS, func() { s.saveMarkdownEditor(state) })
	c.OnShortcut(ui.Cmd, ui.KeyZ, func() { s.moveMarkdownEditorHistory(state, false) })
	c.OnShortcut(ui.Cmd|ui.Shift, ui.KeyZ, func() { s.moveMarkdownEditorHistory(state, true) })
	c.OnShortcut(ui.Cmd, ui.KeyY, func() { s.moveMarkdownEditorHistory(state, true) })
}

// A mode transition has the same input barrier as saving: hidden native drafts
// must not be left behind while a source editor mutates the older document.
func (s *app) toggleMarkdownEditorSourceMode(state *markdownEditorState) {
	if state == nil || state.doc == nil || state.readOnly || state.detached || !s.markdownEditorIsCurrent(state) {
		return
	}
	if err := s.captureMarkdownEditorInput(state); err != nil {
		s.lastError = err.Error()
		return
	}
	s.clearMarkdownComposition(state)
	state.doc.ClearSelection()
	state.sourceMode = !state.sourceMode
}

func (s *app) saveMarkdownEditor(state *markdownEditorState) {
	if state == nil || state.doc == nil || state.readOnly || state.detached {
		return
	}
	if state.binding != nil && !s.markdownEditorIsCurrent(state) {
		return
	}
	if err := s.captureMarkdownEditorInput(state); err != nil {
		s.lastError = err.Error()
		return
	}
	if state.binding != nil {
		if state.binding.save != nil {
			state.binding.save()
		}
		return
	}
	s.flushMarkdownAutosave(s.documentAutosaveKey(state.path))
}

func (s *app) moveMarkdownEditorHistory(state *markdownEditorState, redo bool) {
	if state == nil || state.doc == nil || state.readOnly || state.detached {
		return
	}
	if state.binding != nil && !s.markdownEditorIsCurrent(state) {
		return
	}
	if err := s.captureMarkdownEditorInput(state); err != nil {
		s.lastError = err.Error()
		return
	}
	changed := false
	if redo {
		changed = state.doc.Redo()
	} else {
		changed = state.doc.Undo()
	}
	if changed {
		s.commitMarkdownEditorSource(state, state.doc.Source())
		s.syncMarkdownNativeFieldsFromDocument(state)
	}
}

func (s *app) handleMarkdownRichCommand(c *ui.Context, state *markdownEditorState, rich *markdownRichFieldState, key string, event ui.InputEvent) bool {
	if !s.markdownRichFieldCurrent(state, rich) {
		return false
	}
	if event.Kind == ui.InputKeyDown && event.Mods&ui.Cmd != 0 {
		switch event.Key {
		case ui.KeyS:
			s.saveMarkdownEditor(state)
			return true
		case ui.KeyZ:
			s.moveMarkdownEditorHistory(state, event.Mods&ui.Shift != 0)
			return true
		case ui.KeyY:
			s.moveMarkdownEditorHistory(state, true)
			return true
		}
	}
	if event.Kind == ui.InputCommand {
		if event.Text == "undo" || event.Text == "redo" {
			s.moveMarkdownEditorHistory(state, event.Text == "redo")
			return true
		}
	}
	if state.doc.Composing() {
		// Candidate navigation and Escape belong to the native text client.
		return event.Kind == ui.InputCommand
	}
	if event.Kind == ui.InputKeyDown {
		if event.Key == ui.KeyEscape && state.doc.Snapshot().Selection != nil {
			state.doc.ClearSelection()
			return true
		}
		if event.Mods == ui.Shift && (event.Key == ui.KeyUp || event.Key == ui.KeyDown) {
			direction := -1
			if event.Key == ui.KeyDown {
				direction = 1
			}
			r := rich.input.getSelection().Range
			if state.doc.Snapshot().Selection != nil || r.Start == r.End && (direction < 0 && r.Start == 0 || direction > 0 && r.End == ui.UTF16Len(rich.model.Text())) {
				return extendMarkdownBlockSelection(state.doc, rich.partID, direction)
			}
		}
		if event.Key == ui.KeyTab && (event.Mods == 0 || event.Mods == ui.Shift) {
			r := rich.input.getSelection()
			delta := 1
			if event.Mods == ui.Shift {
				delta = -1
			}
			if err := s.commitMarkdownRichMutation(state, rich, false, func(model *editor.RichBlock) error { return model.IndentUTF16(r.Range.Start, r.Range.End, delta) }); err != nil {
				s.lastError = err.Error()
			} else {
				s.installMarkdownRichModel(rich, r)
			}
			return true
		}
	}
	if s.handleMarkdownEditorFieldInput(c, state, key, event) {
		return true
	}
	return s.handleMarkdownRichClipboardCommand(c, state, rich, event)
}

func extendMarkdownBlockSelection(doc *editor.Document, partID uint64, direction int) bool {
	parts := doc.Parts()
	selection := doc.Snapshot().Selection
	if selection == nil {
		selection = &editor.Selection{First: partID, Last: partID}
	}
	for i, part := range parts {
		if part.ID != selection.Last {
			continue
		}
		target := i + direction
		if target < 0 || target >= len(parts) {
			return false
		}
		selection.Last = parts[target].ID
		doc.SetSelection(selection)
		return true
	}
	return false
}
