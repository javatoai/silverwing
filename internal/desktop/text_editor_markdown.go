package desktop

import (
	"fmt"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	codeeditor "github.com/egoist/mygo/ui/editor"
	"silverwing-go/internal/editor"
)

type markdownNativeFieldState struct {
	view      codeeditor.State
	source    string
	version   uint64
	key       string
	partID    uint64
	code      bool
	errorText string
}

func markdownNativeTextSelection(view *codeeditor.State) (int, int) {
	selection := view.Selection()
	source := view.Buffer.OriginalText()
	return textEditorSourceRuneOffset(source, selection.Anchor), textEditorSourceRuneOffset(source, selection.Head)
}

func markdownNativeSetSelection(view *codeeditor.State, source string, start, end int) {
	view.Select(textEditorProjectedByteOffset(source, start), textEditorProjectedByteOffset(source, end))
}

func markdownNativeProjectedOffset(source string, originalByte int) int {
	return textEditorProjectedByteOffset(source, utf8.RuneCountInString(source[:bounded(originalByte, 0, len(source))]))
}

func markdownNativePartValue(state *markdownEditorState, field *markdownNativeFieldState) (string, int, bool) {
	if field.key == "source" {
		return state.doc.Source(), 0, true
	}
	for _, part := range state.doc.Parts() {
		if part.ID != field.partID {
			continue
		}
		if field.code {
			if fence := editor.FindFence(part.Source); fence != nil {
				return fence.Code(part.Source), fence.BodyStart, true
			}
		}
		return part.Source, 0, true
	}
	return "", 0, false
}

func (s *app) flushMarkdownNativeFields(state *markdownEditorState) bool {
	if state == nil || state.doc == nil {
		return true
	}
	for _, field := range state.nativeFields {
		if !s.flushMarkdownNativeField(state, field) {
			return false
		}
	}
	return true
}

func (s *app) flushMarkdownNativeField(state *markdownEditorState, field *markdownNativeFieldState) bool {
	if field == nil || field.view.Buffer == nil || (field.key == "source") != state.sourceMode {
		return true
	}
	if field.view.Composing() {
		return false
	}
	if field.version == field.view.Buffer.Version() {
		return field.errorText == ""
	}
	if state.readOnly || state.detached || !s.markdownEditorIsCurrent(state) {
		return false
	}
	current, bodyStart, exists := markdownNativePartValue(state, field)
	if !exists || current != field.source {
		field.errorText = "文档内容已变化，当前未提交的源码输入已保留。"
		s.lastError = field.errorText
		return false
	}
	next := field.view.Buffer.OriginalText()
	if next == field.source {
		field.version, field.errorText = field.view.Buffer.Version(), ""
		return true
	}
	from, to := markdownNativeTextSelection(&field.view)
	state.doc.ClearSelection()
	var editError error
	s.applyMarkdownEdit(state, func(doc *editor.Document) error {
		if field.key == "source" {
			editError = doc.ReplaceSource(next, true)
		} else {
			prefix, oldEnd, newEnd := textEditorChangedRange(field.source, next)
			editError = doc.ReplaceRange(field.partID, bodyStart+prefix, bodyStart+oldEnd, next[prefix:newEnd], true)
		}
		return editError
	})
	if editError != nil {
		field.errorText = editError.Error()
		return false
	}
	field.source, field.version, field.errorText = next, field.view.Buffer.Version(), ""
	state.doc.SetCursor(&editor.Cursor{Part: field.partID, Field: field.key, Start: editor.RuneToByteOffset(next, from), End: editor.RuneToByteOffset(next, to)})
	return true
}

func (s *app) syncMarkdownNativeFieldsFromDocument(state *markdownEditorState) {
	if state == nil || state.doc == nil {
		return
	}
	cursor := state.doc.Snapshot().Cursor
	for _, field := range state.nativeFields {
		if field.view.Composing() || field.version != field.view.Buffer.Version() {
			continue
		}
		value, _, exists := markdownNativePartValue(state, field)
		if !exists {
			continue
		}
		if field.source != value {
			field.view.Buffer.Set(value)
			field.source, field.version, field.errorText = value, field.view.Buffer.Version(), ""
		}
		if cursor != nil && cursor.Part == field.partID && (cursor.Field == field.key || cursor.Field == "") {
			markdownNativeSetSelection(&field.view, value, editor.ByteToRuneOffset(value, cursor.Start), editor.ByteToRuneOffset(value, cursor.End))
		}
	}
}

func textEditorChangedRange(before, after string) (prefix, oldEnd, newEnd int) {
	for prefix < len(before) && prefix < len(after) && before[prefix] == after[prefix] {
		prefix++
	}
	for prefix > 0 && (prefix < len(before) && !utf8.RuneStart(before[prefix]) || prefix < len(after) && !utf8.RuneStart(after[prefix])) {
		prefix--
	}
	oldEnd, newEnd = len(before), len(after)
	for oldEnd > prefix && newEnd > prefix && before[oldEnd-1] == after[newEnd-1] {
		oldEnd--
		newEnd--
	}
	for oldEnd < len(before) && !utf8.RuneStart(before[oldEnd]) {
		oldEnd++
		newEnd++
	}
	return
}

func (s *app) markdownNativeEditor(c *ui.Context, p palette, state *markdownEditorState, key string, partID uint64, source, language string, code bool, height float32) ui.Element {
	if state.nativeFields == nil {
		state.nativeFields = make(map[string]*markdownNativeFieldState)
	}
	field := state.nativeFields[key]
	if field == nil {
		field = &markdownNativeFieldState{source: source, key: key, partID: partID, code: code}
		field.view.Buffer = codeeditor.NewBufferPreservingLineEndings(source)
		field.view.Buffer.SetMaxSourceBytes(editor.MaxDocumentBytes)
		lang := textEditorLanguage("snippet." + language)
		if lang == nil {
			lang = codeeditor.LanguageNamed(language)
		}
		field.view.Buffer.SetLanguage(lang)
		state.nativeFields[key] = field
		state.doc.Bind(field, func() { s.flushMarkdownNativeField(state, field) })
	}
	field.view.Buffer.SetMaxSourceBytes(max(0, state.doc.MaxSourceBytes()-len(state.doc.Source())+len(source)))
	if field.source != source && field.version == field.view.Buffer.Version() && !field.view.Composing() {
		field.view.Buffer.Set(source)
		field.source, field.version = source, field.view.Buffer.Version()
	}
	field.view.ReadOnly = state.readOnly || state.detached
	field.view.Label = map[bool]string{true: "Markdown 原文", false: "Markdown 源码块"}[key == "source"]
	field.view.Font.Size, field.view.LineHeight = textEditorFontSize*state.zoom, textEditorLineHeight
	field.view.NoAutoClose, field.view.NoScrollPastEnd = true, true
	field.view.OnCompositionChanged = func(active bool) {
		if s.markdownEditorIsCurrent(state) {
			s.observeMarkdownComposition(state, key, active)
			if !active {
				s.flushMarkdownNativeField(state, field)
			}
		}
	}
	field.view.OnLimitExceeded = func() {
		s.lastError = fmt.Sprintf("Markdown 输入不能超过 %d KiB", state.doc.MaxSourceBytes()/1024)
	}
	field.view.OnInput = func(event ui.InputEvent) bool {
		if !s.flushMarkdownNativeField(state, field) {
			return false
		}
		if !field.view.Composing() {
			from, to := markdownNativeTextSelection(&field.view)
			state.doc.SetCursor(&editor.Cursor{Part: field.partID, Field: key, Start: editor.RuneToByteOffset(field.source, from), End: editor.RuneToByteOffset(field.source, to)})
		}
		if event.Kind == ui.InputCommand && (event.Text == "undo" || event.Text == "redo") {
			s.moveMarkdownEditorHistory(state, event.Text == "redo")
			return true
		}
		if event.Kind == ui.InputKeyDown && (event.Mods == ui.Cmd && (event.Key == ui.KeyZ || event.Key == ui.KeyY) || event.Mods == ui.Cmd|ui.Shift && event.Key == ui.KeyZ) {
			s.moveMarkdownEditorHistory(state, event.Key == ui.KeyY || event.Mods == ui.Cmd|ui.Shift)
			return true
		}
		if key != "source" {
			return s.handleMarkdownEditorFieldInput(c, state, key, event)
		}
		return false
	}
	field.view.FindHighlights = nil
	if find := s.markdownEditorFindState(state); find != nil {
		for index, hit := range find.hits {
			if hit.block == key {
				field.view.FindHighlights = append(field.view.FindHighlights, codeeditor.FindHighlight{
					Start: markdownNativeProjectedOffset(source, hit.start), End: markdownNativeProjectedOffset(source, hit.end), Current: index == find.active})
			}
		}
	}
	services := c.Services()
	field.view.ClipboardWrite = func(text string) bool { return s.copyTextToClipboard(services, text, "") }
	field.view.ReplaceContextMenu = true
	field.view.ContextMenu = func(menu *ui.Menu) {
		if menu.Item("撤销").Chosen() {
			s.moveMarkdownEditorHistory(state, false)
		}
		if menu.Item("重做").Chosen() {
			s.moveMarkdownEditorHistory(state, true)
		}
		menu.Separator()
		for _, command := range []struct {
			label   string
			command codeeditor.Command
		}{{"剪切", codeeditor.Cut}, {"复制", codeeditor.Copy}, {"粘贴", codeeditor.Paste}, {"全选", codeeditor.SelectAll}} {
			if menu.Item(command.label).Chosen() {
				field.view.Run(command.command)
				s.flushMarkdownNativeField(state, field)
			}
		}
	}
	return TextEditor(c, p, &field.view).FillWidth().Height(height).Border(1, p.line).Radius(7)
}
