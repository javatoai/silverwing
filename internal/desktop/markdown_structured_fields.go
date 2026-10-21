package desktop

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"silverwing-go/internal/editor"

	"github.com/egoist/mygo/ui"
)

type markdownStructuredRichFieldState struct {
	key       string
	partID    uint64
	partKind  editor.PartKind
	row       int
	column    int
	taskIndex int
	start     int
	end       int
	source    string
	stale     bool
	rich      *markdownRichFieldState
}

func markdownStructuredTableFieldKey(partID uint64, row, column int) string {
	return fmt.Sprintf("cell-%d-%d-%d", partID, row, column)
}

func markdownStructuredTaskFieldKey(partID uint64, index int) string {
	return fmt.Sprintf("task-%d-%d", partID, index)
}

func allMarkdownStructuredRichFields(state *markdownEditorState) []*markdownRichFieldState {
	if state == nil || len(state.structuredRichFields) == 0 {
		return nil
	}
	keys := make([]string, 0, len(state.structuredRichFields))
	for key := range state.structuredRichFields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	fields := make([]*markdownRichFieldState, 0, len(keys))
	for _, key := range keys {
		if field := state.structuredRichFields[key]; field != nil && field.rich != nil && field.rich.input != nil {
			fields = append(fields, field.rich)
		}
	}
	return fields
}

func (s *app) markdownStructuredRichFieldKey(state *markdownEditorState, rich *markdownRichFieldState) (string, bool) {
	if state == nil || rich == nil {
		return "", false
	}
	for key, field := range state.structuredRichFields {
		if field != nil && field.rich == rich {
			return key, true
		}
	}
	return "", false
}

func (s *app) selectedMarkdownStructuredRich(state *markdownEditorState) *markdownRichFieldState {
	if state == nil || state.selectedStructuredRich == nil || state.selectedStructuredRich.rich == nil {
		return nil
	}
	selected := state.selectedStructuredRich
	rich := selected.rich
	if !s.markdownStructuredRichFieldCurrent(state, rich) {
		state.selectedStructuredRich = nil
		return nil
	}
	return rich
}

func (s *app) markdownStructuredRichFieldCurrent(state *markdownEditorState, rich *markdownRichFieldState) bool {
	if state == nil || rich == nil || state.closed || state.readOnly || state.detached || state.doc == nil || !s.markdownEditorIsCurrent(state) {
		return false
	}
	key, ok := s.markdownStructuredRichFieldKey(state, rich)
	if !ok {
		return false
	}
	field := state.structuredRichFields[key]
	if field == nil || field.rich != rich || field.stale || rich.input == nil || rich.model == nil || rich.source != field.source {
		return false
	}
	part, ok := markdownEditorPart(state, field.partID)
	if !ok || part.Kind != field.partKind {
		return false
	}
	rangeOfText, ok := markdownStructuredRichRange(part, field)
	if !ok || rangeOfText.Start != field.start || rangeOfText.End != field.end ||
		rangeOfText.Start < 0 || rangeOfText.End < rangeOfText.Start || rangeOfText.End > len(part.Source) {
		return false
	}
	return part.Source[rangeOfText.Start:rangeOfText.End] == field.source
}

func (s *app) pruneMarkdownStructuredRichFields(state *markdownEditorState) {
	if state == nil {
		return
	}
	if state.doc == nil {
		state.selectedStructuredRich = nil
		return
	}
	if len(state.structuredRichFields) == 0 {
		state.selectedStructuredRich = nil
		return
	}
	parts := state.doc.Parts()
	for key, field := range state.structuredRichFields {
		if field == nil {
			delete(state.structuredRichFields, key)
			continue
		}
		part, exists := markdownPartByID(parts, field.partID)
		_, rangeExists := markdownStructuredRichRange(part, field)
		if !exists || part.Kind != field.partKind || !rangeExists {
			if field.rich != nil && field.rich.input != nil {
				field.rich.input.cancelComposition()
			}
			s.observeMarkdownComposition(state, key, false)
			delete(state.structuredRichFields, key)
			if state.selectedStructuredRich == field {
				state.selectedStructuredRich = nil
			}
		}
	}
	if selected := state.selectedStructuredRich; selected != nil && (selected.rich == nil || !s.markdownStructuredRichFieldCurrent(state, selected.rich)) {
		state.selectedStructuredRich = nil
	}
}

func (s *app) commitMarkdownStructuredRichMutation(state *markdownEditorState, rich *markdownRichFieldState, coalesce bool, mutation func(*editor.RichBlock) error) (bool, error) {
	key, associated := s.markdownStructuredRichFieldKey(state, rich)
	if !associated {
		return false, nil
	}
	field := state.structuredRichFields[key]
	if !s.markdownStructuredRichFieldCurrent(state, rich) {
		return true, errors.New("Markdown 结构化编辑字段已失效")
	}
	if mutation == nil {
		return true, errors.New("Markdown 富文本变更为空")
	}
	part, ok := markdownEditorPart(state, field.partID)
	if !ok {
		return true, errors.New("Markdown 编辑块已移除")
	}
	currentRange, ok := markdownStructuredRichRange(part, field)
	if !ok || part.Source[currentRange.Start:currentRange.End] != field.source {
		return true, errors.New("文档内容已变化，请保留当前输入并重新确认后再编辑")
	}
	before := rich.model.Clone()
	if err := mutation(rich.model); err != nil {
		rich.model = before
		return true, err
	}
	serialized := rich.model.Source()
	if markdownStructuredIntroducedBlockSyntax(before.Source(), serialized) {
		rich.model = before
		return true, errors.New("表格单元格和任务项只支持行内格式，不能插入标题或列表结构")
	}
	serialized = markdownStructuredFieldSource(field.partKind, serialized)
	previous := state.doc.Snapshot()
	state.doc.ClearSelection()
	if err := state.doc.ReplaceRange(field.partID, currentRange.Start, currentRange.End, serialized, coalesce); err != nil {
		rich.model = before
		if previous.Selection != nil {
			state.doc.SetSelection(previous.Selection)
		} else if previous.TableSelection != nil {
			selection := previous.TableSelection
			_ = state.doc.SelectTableCells(selection.PartID, selection.FirstRow, selection.LastRow, selection.FirstColumn, selection.LastColumn)
		}
		return true, err
	}
	updated, ok := markdownEditorPart(state, field.partID)
	if !ok {
		return true, errors.New("Markdown 编辑块已移除")
	}
	field.source = serialized
	field.stale = false
	rich.source = serialized
	if !refreshMarkdownStructuredRichRanges(state, updated) {
		return true, errors.New("Markdown 结构化字段范围已变化，请重新选择后编辑")
	}
	s.commitMarkdownEditorSource(state, state.doc.Source())
	return true, nil
}

func (s *app) renderMarkdownStructuredTableEditor(c *ui.Context, p palette, state *markdownEditorState, part editor.Part) bool {
	if state == nil || state.doc == nil || part.Kind != editor.PartTable || state.readOnly {
		return false
	}
	cells := editor.TableCells(part.Source)
	if len(cells) == 0 {
		return false
	}
	type tableField struct {
		cell  editor.TableCell
		field *markdownStructuredRichFieldState
	}
	fields := make([][]tableField, len(cells))
	for rowIndex, row := range cells {
		fields[rowIndex] = make([]tableField, 0, len(row))
		for _, cell := range row {
			key := markdownStructuredTableFieldKey(part.ID, cell.Row, cell.Column)
			field, err := s.ensureMarkdownStructuredRichField(state, part, key, editor.PartTable, cell.Row, cell.Column, -1, cell.Range, p)
			if err != nil {
				return false
			}
			fields[rowIndex] = append(fields[rowIndex], tableField{cell: cell, field: field})
		}
	}
	if state.tableCellBounds == nil {
		state.tableCellBounds = make(map[markdownTableCellKey]ui.Rect)
	}
	ui.Column(c).FillWidth().Gap(4).Children(func() {
		if selected := state.doc.Snapshot().TableSelection; selected != nil && selected.PartID == part.ID {
			ui.Row(c.Key(fmt.Sprintf("markdown-table-selection-bar-%d", part.ID))).FillWidth().Height(30).AlignItems(ui.Center).Gap(8).Children(func() {
				label(c, fmt.Sprintf("已选 %d × %d 个单元格", selected.LastRow-selected.FirstRow+1, selected.LastColumn-selected.FirstColumn+1), 11, p.muted)
				ui.Spacer(c)
				if action(c, markdownEditorControlKey(state, "markdown-table-copy-selection-"+fmt.Sprint(part.ID)), "copy", "复制选区 Markdown", p, false) {
					copyMarkdownTableSelection(state.doc, c.WriteClipboard)
				}
			})
		}
		for rowIndex, row := range fields {
			line := ui.Row(c.Key(fmt.Sprintf("markdown-table-row-%d-%d", part.ID, rowIndex))).FillWidth().Gap(5)
			if rowIndex == 0 {
				line.Background(p.soft)
			}
			line.Children(func() {
				for _, item := range row {
					selected := markdownTableCellSelected(state.doc, part.ID, item.cell.Row, item.cell.Column)
					cellPalette := p
					cellPalette.surface = p.bg
					cellPalette.line = p.line
					if selected {
						cellPalette.surface = p.soft
						cellPalette.line = p.blue
					}
					ui.Column(c.Key(item.field.key + "-container")).Grow(1).Gap(2).Children(func() {
						element := s.renderMarkdownStructuredRichField(c, cellPalette, state, part, item.field, item.cell.Row, item.cell.Column, true)
						state.tableCellBounds[markdownTableCellKey{partID: part.ID, row: item.cell.Row, column: item.cell.Column}] = element.Bounds()
						s.renderMarkdownStructuredRichFieldError(c, p, state, item.field)
					})
				}
			})
		}
	})
	return true
}

func (s *app) renderMarkdownTaskItemsEditor(c *ui.Context, p palette, state *markdownEditorState, part editor.Part) bool {
	if state == nil || state.doc == nil || part.Kind != editor.PartTasks || state.readOnly {
		return false
	}
	tasks := editor.TaskItems(part.Source)
	if len(tasks) == 0 || markdownNonblankLineCount(part.Source) != len(tasks) {
		return false
	}
	fields := make([]*markdownStructuredRichFieldState, len(tasks))
	for index, task := range tasks {
		key := markdownStructuredTaskFieldKey(part.ID, task.Index)
		field, err := s.ensureMarkdownStructuredRichField(state, part, key, editor.PartTasks, -1, -1, task.Index, task.TextRange, p)
		if err != nil {
			return false
		}
		fields[index] = field
	}
	ui.Column(c).FillWidth().Padding(8, 0).Gap(5).Children(func() {
		for index, task := range tasks {
			field := fields[index]
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				checked := task.Checked
				before := checked
				checkboxKey := markdownEditorControlKey(state, fmt.Sprintf("markdown-tasklist-%d-%d", part.ID, task.Index))
				ui.Checkbox(c.Key(checkboxKey), &checked, "")
				if checked != before {
					if err := s.setMarkdownStructuredTaskChecked(state, part, task, checked); err != nil {
						s.lastError = err.Error()
					}
				}
				ui.Column(c).Grow(1).Gap(2).Children(func() {
					s.renderMarkdownStructuredRichField(c, p, state, part, field, task.Index, 0, false)
					s.renderMarkdownStructuredRichFieldError(c, p, state, field)
				})
			})
		}
	})
	return true
}

func (s *app) ensureMarkdownStructuredRichField(state *markdownEditorState, part editor.Part, key string, kind editor.PartKind, row, column, taskIndex int, textRange editor.SourceRange, p palette) (*markdownStructuredRichFieldState, error) {
	if state == nil || state.doc == nil || textRange.Start < 0 || textRange.End < textRange.Start || textRange.End > len(part.Source) {
		return nil, errors.New("Markdown 结构化字段范围无效")
	}
	if state.structuredRichFields == nil {
		state.structuredRichFields = make(map[string]*markdownStructuredRichFieldState)
	}
	source := part.Source[textRange.Start:textRange.End]
	field := state.structuredRichFields[key]
	if field == nil {
		field = &markdownStructuredRichFieldState{key: key, partID: part.ID, partKind: kind, row: row, column: column, taskIndex: taskIndex,
			start: textRange.Start, end: textRange.End, source: source}
		state.structuredRichFields[key] = field
		if err := s.initializeMarkdownStructuredRichField(state, field, part, source, p); err != nil {
			delete(state.structuredRichFields, key)
			return nil, err
		}
	} else {
		field.partID, field.partKind = part.ID, kind
		field.row, field.column, field.taskIndex = row, column, taskIndex
		field.start, field.end = textRange.Start, textRange.End
		if field.rich == nil || field.rich.input == nil || field.rich.model == nil {
			if err := s.initializeMarkdownStructuredRichField(state, field, part, source, p); err != nil {
				return nil, err
			}
		} else if field.source != source {
			if field.rich.input.composing || field.rich.input.dirty || field.rich.input.errorText != "" {
				field.stale = true
				s.lastError = "文档内容已变化，当前未提交的富文本输入已保留。"
			} else {
				if err := s.resetMarkdownStructuredRichField(field, part, source, p); err != nil {
					return nil, err
				}
			}
		} else {
			field.stale = false
		}
	}
	field.rich.palette = p
	field.rich.synchronizing = true
	field.rich.input.setContent(field.rich.model.Text(), markdownRichSpans(field.rich.model, p))
	field.rich.synchronizing = false
	return field, nil
}

func (s *app) initializeMarkdownStructuredRichField(state *markdownEditorState, field *markdownStructuredRichFieldState, part editor.Part, source string, p palette) error {
	model, err := editor.NewRichBlock(editor.Part{ID: part.ID, Kind: editor.PartRich,
		Range: editor.SourceRange{Start: field.start, End: field.end}, Source: source})
	if err != nil {
		return err
	}
	rich := &markdownRichFieldState{partID: part.ID, source: source, model: model, palette: p}
	rich.input = newNativeMarkdownRichTextField(model.Text(), markdownRichSpans(model, p))
	rich.selection = rich.input.getSelection()
	field.source, field.stale, field.rich = source, false, rich
	rich.input.onReplace = func(start, end int, value string) (string, []ui.Span, error) {
		handled, err := s.commitMarkdownStructuredRichMutation(state, rich, true, func(model *editor.RichBlock) error {
			return model.ReplaceUTF16(start, end, value)
		})
		if err != nil {
			return "", nil, err
		}
		if !handled {
			return "", nil, errors.New("Markdown 结构化编辑字段已失效")
		}
		return rich.model.Text(), markdownRichSpans(rich.model, rich.palette), nil
	}
	rich.input.onReplacementCaret = func() int { return rich.model.ReplacementCaretUTF16() }
	rich.input.onPointerDown = func() {
		if s.markdownStructuredRichFieldCurrent(state, rich) && !state.doc.Composing() {
			state.doc.ClearSelection()
		}
	}
	rich.input.onSelection = func(start, end int) {
		if rich.synchronizing || !s.markdownStructuredRichFieldCurrent(state, rich) || rich.input.composing || rich.input.dirty {
			return
		}
		rich.selection = rich.input.getSelection()
		currentField := state.structuredRichFields[field.key]
		if currentField == nil || currentField.rich != rich {
			return
		}
		state.selectedStructuredRich = currentField
		state.selectionPart = rich.partID
		localStart := ui.UTF16ByteOffset(rich.model.Text(), start)
		localEnd := ui.UTF16ByteOffset(rich.model.Text(), end)
		state.selectionFrom, state.selectionTo = currentField.start+localStart, currentField.start+localEnd
		state.doc.SetCursor(&editor.Cursor{Part: rich.partID, Start: currentField.start + localStart, End: currentField.start + localEnd, Field: currentField.key})
	}
	rich.input.onComposition = func(active bool) {
		if s.markdownEditorIsCurrent(state) && state.structuredRichFields[field.key] == field {
			s.observeMarkdownComposition(state, field.key, active)
		}
	}
	rich.input.onError = func(err error) {
		if s.markdownStructuredRichFieldCurrent(state, rich) {
			s.lastError = err.Error()
		}
	}
	return nil
}

func (s *app) resetMarkdownStructuredRichField(field *markdownStructuredRichFieldState, part editor.Part, source string, p palette) error {
	model, err := editor.NewRichBlock(editor.Part{ID: part.ID, Kind: editor.PartRich,
		Range: editor.SourceRange{Start: field.start, End: field.end}, Source: source})
	if err != nil {
		return err
	}
	field.source, field.stale = source, false
	field.rich.source, field.rich.model, field.rich.palette = source, model, p
	field.rich.synchronizing = true
	field.rich.input.setContent(model.Text(), markdownRichSpans(model, p))
	field.rich.synchronizing = false
	return nil
}

func (s *app) renderMarkdownStructuredRichField(c *ui.Context, p palette, state *markdownEditorState, part editor.Part, field *markdownStructuredRichFieldState, row, column int, tableCell bool) ui.Element {
	rich := field.rich
	key := field.key
	s.observeMarkdownComposition(state, key, rich.input.composing)
	rich.input.setZoom(state.zoom)
	rich.input.onScroll = func(event ui.InputEvent) bool {
		return s.handleMarkdownEditorZoomInput(state, event)
	}
	element := renderNativeRichTextField(c, p, markdownEditorInputKey(state, key), rich.input)
	element = s.decorateMarkdownEditorFind(c, p, state, key, rich.model.Text(), element, rich)
	if tableCell {
		fieldRow, fieldColumn := row, column
		element.HandleInput(func(event ui.InputEvent) bool {
			if event.Kind == ui.InputScroll && rich.input.onScroll != nil && rich.input.onScroll(event) {
				return true
			}
			if s.handleMarkdownTableCellInput(c, state, field.partID, fieldRow, fieldColumn, rich.input.handle, event) {
				return true
			}
			return rich.input.handleInput(c, event)
		})
	}
	state.editFields[key] = markdownEditFieldRef{key: key, partID: part.ID, value: rich.model.Text(), element: element, row: row, column: column, tableCell: tableCell, rich: rich}
	rich.input.onCommand = func(c *ui.Context, event ui.InputEvent) bool {
		return s.handleMarkdownRichCommand(c, state, rich, key, event)
	}
	snapshot := state.doc.Snapshot()
	if snapshot.FocusRequest != state.lastFocusRequest && snapshot.Cursor != nil && snapshot.Cursor.Part == part.ID && snapshot.Cursor.Field == key {
		localStart := max(0, snapshot.Cursor.Start-field.start)
		localEnd := max(localStart, snapshot.Cursor.End-field.start)
		text := rich.model.Text()
		start := editor.ByteToUTF16Offset(text, min(localStart, len(text)))
		end := editor.ByteToUTF16Offset(text, min(localEnd, len(text)))
		rich.input.setSelection(ui.TextInputSelection{Range: ui.TextInputRange{Start: start, End: end}})
		element.Focus()
		state.lastFocusRequest = snapshot.FocusRequest
	}
	return element
}

func (s *app) renderMarkdownStructuredRichFieldError(c *ui.Context, p palette, state *markdownEditorState, field *markdownStructuredRichFieldState) {
	if field == nil || field.rich == nil || field.rich.input == nil {
		return
	}
	message := field.rich.input.errorText
	if message == "" && field.stale {
		message = "文档内容已变化，当前未提交的富文本输入已保留。"
	}
	if message == "" {
		return
	}
	label(c, message, 11, p.danger).Wrap()
	if action(c, markdownEditorControlKey(state, field.key+"-discard-draft"), "undo", "丢弃未提交输入", p, false) {
		field.rich.input.discardDraft()
	}
}

func (s *app) setMarkdownStructuredTaskChecked(state *markdownEditorState, renderedPart editor.Part, renderedTask editor.TaskItem, checked bool) error {
	if state == nil || state.doc == nil || state.readOnly || state.detached || !s.markdownEditorIsCurrent(state) {
		return errors.New("Markdown 任务列表已失效")
	}
	if state.doc.Composing() {
		return editor.ErrComposing
	}
	part, ok := markdownEditorPart(state, renderedPart.ID)
	if !ok || part.Kind != editor.PartTasks || part.Source != renderedPart.Source {
		return errors.New("文档内容已变化，请重新确认任务状态")
	}
	tasks := editor.TaskItems(part.Source)
	if renderedTask.Index < 0 || renderedTask.Index >= len(tasks) {
		return errors.New("任务复选框不存在")
	}
	task := tasks[renderedTask.Index]
	if task.CheckRange != renderedTask.CheckRange || task.Checked != renderedTask.Checked {
		return errors.New("文档内容已变化，请重新确认任务状态")
	}
	value := " "
	if checked {
		value = "x"
	}
	if err := state.doc.ReplaceRange(part.ID, task.CheckRange.Start, task.CheckRange.End, value, false); err != nil {
		return err
	}
	updated, ok := markdownEditorPart(state, part.ID)
	if ok {
		refreshMarkdownStructuredRichRanges(state, updated)
	}
	s.commitMarkdownEditorSource(state, state.doc.Source())
	return nil
}

func markdownEditorPart(state *markdownEditorState, partID uint64) (editor.Part, bool) {
	if state == nil || state.doc == nil {
		return editor.Part{}, false
	}
	return markdownPartByID(state.doc.Parts(), partID)
}

func markdownPartByID(parts []editor.Part, partID uint64) (editor.Part, bool) {
	for _, part := range parts {
		if part.ID == partID {
			return part, true
		}
	}
	return editor.Part{}, false
}

func markdownStructuredRichRange(part editor.Part, field *markdownStructuredRichFieldState) (editor.SourceRange, bool) {
	if field == nil || part.ID != field.partID || part.Kind != field.partKind {
		return editor.SourceRange{}, false
	}
	switch field.partKind {
	case editor.PartTable:
		rows := editor.TableCells(part.Source)
		if field.row < 0 || field.row >= len(rows) || field.column < 0 || field.column >= len(rows[field.row]) {
			return editor.SourceRange{}, false
		}
		return rows[field.row][field.column].Range, true
	case editor.PartTasks:
		tasks := editor.TaskItems(part.Source)
		if field.taskIndex < 0 || field.taskIndex >= len(tasks) {
			return editor.SourceRange{}, false
		}
		return tasks[field.taskIndex].TextRange, true
	default:
		return editor.SourceRange{}, false
	}
}

func refreshMarkdownStructuredRichRanges(state *markdownEditorState, part editor.Part) bool {
	valid := true
	for _, field := range state.structuredRichFields {
		if field == nil || field.partID != part.ID {
			continue
		}
		rangeOfText, ok := markdownStructuredRichRange(part, field)
		if !ok || rangeOfText.Start < 0 || rangeOfText.End < rangeOfText.Start || rangeOfText.End > len(part.Source) {
			field.stale = true
			valid = false
			continue
		}
		field.start, field.end = rangeOfText.Start, rangeOfText.End
		if field.rich != nil && field.source == part.Source[rangeOfText.Start:rangeOfText.End] {
			field.stale = false
		} else if field.rich != nil {
			field.stale = true
		}
	}
	return valid
}

func markdownStructuredFieldSource(kind editor.PartKind, source string) string {
	switch kind {
	case editor.PartTable:
		return escapeMarkdownStructuredTableCell(source)
	case editor.PartTasks:
		source = strings.TrimRight(source, "\r\n")
		source = strings.ReplaceAll(strings.ReplaceAll(source, "\r\n", "\n"), "\r", "\n")
		return strings.ReplaceAll(source, "\n", " ")
	default:
		return source
	}
}

func escapeMarkdownStructuredTableCell(source string) string {
	source = strings.TrimRight(source, "\r\n")
	source = strings.ReplaceAll(strings.ReplaceAll(source, "\r\n", "\n"), "\r", "\n")
	var out strings.Builder
	codeTicks, backslashes := 0, 0
	for index := 0; index < len(source); {
		char := source[index]
		if char == '|' {
			if codeTicks == 0 && backslashes%2 == 0 {
				out.WriteByte('\\')
			}
			out.WriteByte(char)
			backslashes = 0
			index++
			continue
		}
		if char == '`' && backslashes%2 == 0 {
			end := index + 1
			for end < len(source) && source[end] == '`' {
				end++
			}
			tickCount := end - index
			if codeTicks == 0 {
				codeTicks = tickCount
			} else if codeTicks == tickCount {
				codeTicks = 0
			}
			out.WriteString(source[index:end])
			backslashes = 0
			index = end
			continue
		}
		if char == '\n' {
			out.WriteString("<br>")
			backslashes = 0
		} else {
			out.WriteByte(char)
			if char == '\\' {
				backslashes++
			} else {
				backslashes = 0
			}
		}
		index++
	}
	return out.String()
}

var markdownStructuredHeadingRE = regexp.MustCompile(`^ {0,3}#{1,6}[ \t]+`)
var markdownStructuredListRE = regexp.MustCompile(`^[ \t]*(?:[-+*]|[0-9]+[.)])[ \t]+`)

func markdownStructuredIntroducedBlockSyntax(before, after string) bool {
	lines := func(source string) []string {
		return strings.Split(strings.ReplaceAll(strings.ReplaceAll(source, "\r\n", "\n"), "\r", "\n"), "\n")
	}
	prior := make(map[string]int)
	for _, line := range lines(before) {
		if markdownStructuredHeadingRE.MatchString(line) || markdownStructuredListRE.MatchString(line) {
			prior[line]++
		}
	}
	for _, line := range lines(after) {
		if !markdownStructuredHeadingRE.MatchString(line) && !markdownStructuredListRE.MatchString(line) {
			continue
		}
		if prior[line] == 0 {
			return true
		}
		prior[line]--
	}
	return false
}

func markdownNonblankLineCount(source string) int {
	count := 0
	for _, line := range strings.Split(strings.ReplaceAll(strings.ReplaceAll(source, "\r\n", "\n"), "\r", "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			count++
		}
	}
	return count
}
