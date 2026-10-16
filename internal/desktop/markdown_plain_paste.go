package desktop

import (
	"fmt"
	"unicode/utf8"

	"silverwing-go/internal/editor"

	"github.com/egoist/mygo/ui"
)

// Paste is queued until a build owns the native field's current selection.
// The SDK may invoke OnPaste between builds, when Element queries are invalid.
func (s *app) bindMarkdownPlainRawPaste(c *ui.Context, state *markdownEditorState, partID uint64, key string, field ui.Element, code bool) {
	pending := ui.Local(field, "markdown-raw-pastes", func() []string { return nil })
	field.OnPaste(func(text string) bool {
		if text != "" && s.markdownEditorIsCurrent(state) && !state.readOnly && !state.detached {
			*pending = append(*pending, text)
			c.Invalidate()
		}
		return true // Preserve the clipboard text before native CRLF normalization.
	})
	if len(*pending) == 0 {
		return
	}
	from, to := field.TextSelection()
	for _, text := range *pending {
		if s.applyMarkdownPlainRawPaste(state, partID, key, code, from, to, text) {
			from += utf8.RuneCountInString(text)
			to = from
		}
	}
	*pending = nil
	c.Invalidate() // The next build loads the new source before restoring its caret.
}

func (s *app) applyMarkdownPlainRawPaste(state *markdownEditorState, partID uint64, key string, code bool, from, to int, text string) bool {
	if !s.markdownEditorIsCurrent(state) || state.readOnly || state.detached || state.doc.Composing() {
		return false
	}
	var part editor.Part
	for _, candidate := range state.doc.Parts() {
		if candidate.ID == partID {
			part = candidate
			break
		}
	}
	if part.ID == 0 {
		s.lastError = fmt.Sprintf("Markdown 块不存在：%d", partID)
		return false
	}
	value, bodyStart := part.Source, 0
	if code {
		if fence := editor.FindFence(part.Source); fence != nil {
			value, bodyStart = fence.Code(part.Source), fence.BodyStart
		}
	}
	start := editor.RuneToByteOffset(value, min(from, to))
	end := editor.RuneToByteOffset(value, max(from, to))
	state.doc.ClearSelection()
	state.doc.SetCursor(&editor.Cursor{Part: partID, Field: key, Start: start, End: end})
	var pasteErr error
	s.applyMarkdownEdit(state, func(doc *editor.Document) error {
		pasteErr = doc.ReplaceRange(partID, bodyStart+start, bodyStart+end, text, false)
		return pasteErr
	})
	if pasteErr != nil {
		return false // Existing document limits retain source and both history stacks.
	}
	caret := start + len(text)
	state.doc.RequestFocusCursor(&editor.Cursor{Part: partID, Field: key, Start: caret, End: caret})
	return true
}
