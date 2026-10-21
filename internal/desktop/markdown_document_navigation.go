package desktop

import (
	"strings"
	"unicode/utf8"

	"silverwing-go/internal/editor"

	"github.com/egoist/mygo/ui"
	codeeditor "github.com/egoist/mygo/ui/editor"
)

type markdownEditFieldRef struct {
	key            string
	partID         uint64
	value          string
	element        ui.Element
	selectionStart int
	selectionEnd   int
	row            int
	column         int
	tableCell      bool
	rich           *markdownRichFieldState
	native         *codeeditor.State
}

type markdownTableCellKey struct {
	partID uint64
	row    int
	column int
}

type markdownTableDragState struct {
	partID      uint64
	anchorRow   int
	anchorCol   int
	startX      float32
	startY      float32
	dragging    bool
	lastRow     int
	lastColumn  int
	hasLastCell bool
}

func (s *app) registerMarkdownEditField(c *ui.Context, state *markdownEditorState, part editor.Part, key, value string, element ui.Element, row, column int, tableCell bool) ui.Element {
	if state.editFields == nil {
		state.editFields = make(map[string]markdownEditFieldRef)
	}
	var native *codeeditor.State
	if field := state.nativeFields[key]; field != nil {
		native = &field.view
	} else {
		element.HandleInput(func(event ui.InputEvent) bool { return s.handleMarkdownEditorFieldInput(c, state, key, event) })
	}

	snapshot := state.doc.Snapshot()
	if snapshot.FocusRequest != state.lastFocusRequest && snapshot.Cursor != nil && snapshot.Cursor.Part == part.ID &&
		(snapshot.Cursor.Field == "" || snapshot.Cursor.Field == key) {
		start := editor.ByteToRuneOffset(value, snapshot.Cursor.Start)
		end := editor.ByteToRuneOffset(value, snapshot.Cursor.End)
		// Model callers can omit End when requesting a field-independent caret.
		if snapshot.Cursor.Field == "" && snapshot.Cursor.End == 0 {
			end = start
		}
		element.Focus()
		if native != nil {
			markdownNativeSetSelection(native, value, start, end)
		} else {
			element.SetTextSelection(start, end)
		}
		state.lastFocusRequest = snapshot.FocusRequest
	}
	if snapshot.FocusRequest == state.lastFocusRequest && element.Focused() {
		recordMarkdownPlainFieldCursor(state, part.ID, key, value, element)
	}
	from, to := element.TextSelection()
	if native != nil {
		from, to = markdownNativeTextSelection(native)
	}
	ref := markdownEditFieldRef{key: key, partID: part.ID, value: value, element: element,
		selectionStart: from, selectionEnd: to, row: row, column: column, tableCell: tableCell, native: native}
	state.editFields[key] = ref
	return element
}

func recordMarkdownPlainFieldCursor(state *markdownEditorState, partID uint64, key, value string, element ui.Element) {
	from, to := element.TextSelection()
	if field := state.nativeFields[key]; field != nil {
		from, to = markdownNativeTextSelection(&field.view)
	}
	state.doc.SetCursor(&editor.Cursor{Part: partID, Start: editor.RuneToByteOffset(value, from), End: editor.RuneToByteOffset(value, to), Field: key})
}

func (s *app) handleMarkdownEditorFieldInput(c *ui.Context, state *markdownEditorState, key string, event ui.InputEvent) bool {
	if state == nil || state.doc == nil || state.readOnly || state.detached {
		return false
	}
	if state.binding != nil && !s.markdownEditorIsCurrent(state) {
		return true
	}
	ref, ok := state.editFields[key]
	if !ok {
		return false
	}
	if ref.native != nil && ref.native.Buffer != nil {
		ref.value = ref.native.Buffer.OriginalText()
	}
	if event.Kind == ui.InputScroll && (strings.HasPrefix(key, "code-") || strings.HasPrefix(key, "part-")) {
		return s.handleMarkdownEditorZoomInput(state, event)
	}
	snapshot := state.doc.Snapshot()
	if s.handleMarkdownBlockClipboard(c, state, event) {
		return true
	}
	if snapshot.Selection != nil && event.Kind == ui.InputKeyDown && event.Mods == 0 && (event.Key == ui.KeyBackspace || event.Key == ui.KeyDelete) {
		if !state.doc.Composing() && deleteMarkdownBlockSelection(state.doc) {
			s.commitMarkdownEditorSource(state, state.doc.Source())
		}
		return true
	}
	if event.Kind == ui.InputCommand {
		switch event.Text {
		case "undo", "redo":
			s.moveMarkdownEditorHistory(state, event.Text == "redo")
			return true
		case "selectAll":
			if !state.doc.Composing() {
				state.doc.SelectAll()
			}
			return true
		case "copy":
			if ref.tableCell && snapshot.TableSelection != nil && snapshot.TableSelection.PartID == ref.partID {
				copyMarkdownTableSelection(state.doc, c.WriteClipboard)
				return true
			}
		case "delete":
			if snapshot.Selection != nil {
				if !state.doc.Composing() && deleteMarkdownBlockSelection(state.doc) {
					s.commitMarkdownEditorSource(state, state.doc.Source())
				}
				return true
			}
		}
		return false
	}
	if event.Kind == ui.InputPointerDown && event.Button == 0 && !ref.tableCell {
		state.doc.ClearSelection()
		return false
	}
	if event.Kind != ui.InputKeyDown {
		return false
	}
	switch {
	case event.Mods == ui.Cmd && event.Key == ui.KeyA:
		if !state.doc.Composing() {
			state.doc.SelectAll()
		}
		return true
	case event.Mods == ui.Cmd && event.Key == ui.KeyC:
		if ref.tableCell && snapshot.TableSelection != nil && snapshot.TableSelection.PartID == ref.partID {
			copyMarkdownTableSelection(state.doc, c.WriteClipboard)
			return true
		}
	case event.Mods == ui.Ctrl && event.Key == ui.KeyEnter:
		if !state.doc.Composing() {
			s.insertMarkdownParagraphAfter(state, ref.partID)
		}
		return true
	case event.Mods == 0 && (event.Key == ui.KeyUp || event.Key == ui.KeyDown):
		if state.doc.Composing() {
			return false
		}
		from, to := markdownFieldTextSelection(ref)
		direction := -1
		if event.Key == ui.KeyDown {
			direction = 1
		}
		if markdownCaretAtBlockBoundary(from, to, ref.value, direction) && markdownFieldCanLeave(state, ref, direction) {
			return s.moveMarkdownFocus(state, key, direction)
		}
	}
	return false
}

func markdownCaretAtBlockBoundary(start, end int, value string, direction int) bool {
	if start != end {
		return false
	}
	if direction < 0 {
		return start == 0
	}
	return direction > 0 && end == utf8.RuneCountInString(value)
}

func markdownFieldCanLeave(state *markdownEditorState, ref markdownEditFieldRef, direction int) bool {
	if state == nil || state.doc == nil {
		return false
	}
	parts := state.doc.Parts()
	index := -1
	for i := range parts {
		if parts[i].ID == ref.partID {
			index = i
			if ref.tableCell {
				cells := editor.TableCells(parts[i].Source)
				if len(cells) == 0 {
					return false
				}
				lastRow := len(cells) - 1
				lastColumn := len(cells[lastRow]) - 1
				return direction < 0 && ref.row == 0 && ref.column == 0 ||
					direction > 0 && ref.row == lastRow && ref.column == lastColumn
			}
			break
		}
	}
	return index+direction >= 0 && index+direction < len(parts)
}

func (s *app) moveMarkdownFocus(state *markdownEditorState, key string, direction int) bool {
	if state == nil || state.doc == nil || state.doc.Composing() || state.readOnly || state.detached {
		return false
	}
	current, ok := state.editFields[key]
	if !ok {
		return false
	}
	target, ok := markdownAdjacentEditField(state.editFields, state.doc.Parts(), current.partID, direction)
	if !ok {
		return false
	}
	if target.rich != nil && !s.markdownRichFieldCurrent(state, target.rich) {
		return false
	}
	position := 0
	if direction < 0 {
		position = utf8.RuneCountInString(target.value)
	}
	localByteOffset := editor.RuneToByteOffset(target.value, position)
	cursor := &editor.Cursor{Part: target.partID, Start: localByteOffset, End: localByteOffset, Field: target.key}
	if target.rich != nil {
		structuredKey, structured := s.markdownStructuredRichFieldKey(state, target.rich)
		if structured {
			field := state.structuredRichFields[structuredKey]
			if field == nil || field.rich != target.rich || field.stale {
				return false
			}
			cursor.Start = field.start + localByteOffset
			cursor.End = cursor.Start
			cursor.Field = structuredKey
		} else {
			cursor.Field = markdownRichFieldKey(target.partID)
		}
	}
	state.doc.ClearSelection()
	state.doc.RequestFocusCursor(cursor)
	return true
}

func markdownFieldTextSelection(ref markdownEditFieldRef) (int, int) {
	if ref.native != nil {
		return markdownNativeTextSelection(ref.native)
	}
	if ref.rich == nil {
		return ref.selectionStart, ref.selectionEnd
	}
	r := ref.rich.input.getSelection().Range
	return editor.ByteToRuneOffset(ref.value, ui.UTF16ByteOffset(ref.value, r.Start)), editor.ByteToRuneOffset(ref.value, ui.UTF16ByteOffset(ref.value, r.End))
}

func markdownAdjacentEditField(fields map[string]markdownEditFieldRef, parts []editor.Part, currentPart uint64, direction int) (markdownEditFieldRef, bool) {
	if direction != -1 && direction != 1 {
		return markdownEditFieldRef{}, false
	}
	currentIndex := -1
	for index := range parts {
		if parts[index].ID == currentPart {
			currentIndex = index
			break
		}
	}
	targetIndex := currentIndex + direction
	if currentIndex < 0 || targetIndex < 0 || targetIndex >= len(parts) {
		return markdownEditFieldRef{}, false
	}
	return markdownEdgeField(fields, parts[targetIndex], direction < 0)
}

func markdownEdgeField(fields map[string]markdownEditFieldRef, part editor.Part, last bool) (markdownEditFieldRef, bool) {
	var chosen markdownEditFieldRef
	found := false
	for key, field := range fields {
		if field.partID != part.ID {
			continue
		}
		field.key = key
		if !found {
			chosen, found = field, true
			continue
		}
		if last && (field.row > chosen.row || field.row == chosen.row && field.column > chosen.column) {
			chosen = field
		} else if !last && (field.row < chosen.row || field.row == chosen.row && field.column < chosen.column) {
			chosen = field
		}
	}
	return chosen, found
}

func (s *app) insertMarkdownParagraphAfter(state *markdownEditorState, partID uint64) {
	if state == nil || state.doc == nil || state.readOnly || state.detached || state.doc.Composing() {
		return
	}
	if id := state.doc.InsertParagraph(partID); id == 0 {
		if message := state.doc.Snapshot().Error; message != "" {
			s.lastError = message
		} else {
			s.lastError = "无法插入 Markdown 段落"
		}
		return
	}
	s.commitMarkdownEditorSource(state, state.doc.Source())
}

func markdownPartInSelection(doc *editor.Document, partID uint64) bool {
	if doc == nil {
		return false
	}
	selection := doc.Snapshot().Selection
	if selection == nil {
		return false
	}
	parts := doc.Parts()
	first, last, current := -1, -1, -1
	for index, part := range parts {
		if part.ID == selection.First {
			first = index
		}
		if part.ID == selection.Last {
			last = index
		}
		if part.ID == partID {
			current = index
		}
	}
	if first < 0 || last < 0 || current < 0 {
		return false
	}
	if first > last {
		first, last = last, first
	}
	return current >= first && current <= last
}

func copyMarkdownBlockSelection(doc *editor.Document, write func(string)) bool {
	if doc == nil || write == nil || doc.Snapshot().Selection == nil {
		return false
	}
	write(doc.SelectedSource())
	return true
}

func cutMarkdownBlockSelection(doc *editor.Document, write func(string)) bool {
	if doc == nil || doc.Composing() || doc.Snapshot().Selection == nil {
		return false
	}
	if write != nil {
		write(doc.SelectedSource())
	}
	return doc.ReplaceSelection("") == nil
}

func deleteMarkdownBlockSelection(doc *editor.Document) bool {
	if doc == nil || doc.Composing() || doc.Snapshot().Selection == nil {
		return false
	}
	return doc.ReplaceSelection("") == nil
}
