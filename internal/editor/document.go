package editor

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf16"

	"silverwing-go/internal/markdowntext"
)

var ErrComposing = errors.New("中文输入尚未提交，当前操作已暂停")

type documentSnapshot struct {
	parts  []Part
	cursor *Cursor
}

type Document struct {
	id             string
	parts          []Part
	version        uint64
	focusRequest   uint64
	cursor         *Cursor
	selection      *Selection
	tableSelect    *TableSelection
	composing      bool
	error          string
	undo           []documentSnapshot
	redo           []documentSnapshot
	lastPart       uint64
	lastChange     time.Time
	nextID         uint64
	flushers       map[any]func()
	compositions   map[any]bool
	clock          func() time.Time
	maxSourceBytes int
}

type TableSelection struct {
	PartID      uint64 `json:"partId"`
	FirstRow    int    `json:"firstRow"`
	LastRow     int    `json:"lastRow"`
	FirstColumn int    `json:"firstColumn"`
	LastColumn  int    `json:"lastColumn"`
}

func NewDocument(source, documentID string) *Document {
	d := &Document{id: documentID, flushers: make(map[any]func()), compositions: make(map[any]bool), clock: time.Now}
	d.parts = d.parseWithIDs(source, nil, 0)
	return d
}

func (d *Document) SetClock(clock func() time.Time) {
	if clock != nil {
		d.clock = clock
	}
}

func (d *Document) Source() string { return joinParts(d.parts) }

// SetMaxSourceBytes reserves persisted bytes outside the editable source, such
// as the UTF-8 BOM stripped by the materials host. The default is 3 MiB.
func (d *Document) SetMaxSourceBytes(limit int) {
	if limit <= 0 || limit > MaxDocumentBytes {
		limit = MaxDocumentBytes
	}
	d.maxSourceBytes = limit
}

func (d *Document) MaxSourceBytes() int {
	if d.maxSourceBytes > 0 {
		return d.maxSourceBytes
	}
	return MaxDocumentBytes
}

func (d *Document) Composing() bool { return d.composing }

func (d *Document) Parts() []Part { return cloneParts(d.parts) }

func (d *Document) Snapshot() Snapshot {
	return Snapshot{DocumentID: d.id, Source: d.Source(), Parts: d.Parts(), Version: d.version, FocusRequest: d.focusRequest,
		Cursor: cloneCursor(d.cursor), Selection: cloneSelection(d.selection), TableSelection: cloneTableSelection(d.tableSelect), Composing: d.composing,
		Error: d.error, CanUndo: len(d.undo) > 0, CanRedo: len(d.redo) > 0}
}

func (d *Document) SetComposing(value bool) { d.composing = value }

// Composition tracks concurrent native text fields as a set so one field
// committing does not end another field's active IME composition.
func (d *Document) Composition(owner any, active bool) {
	if active {
		d.compositions[owner] = true
	} else {
		delete(d.compositions, owner)
	}
	d.composing = len(d.compositions) != 0
}

func (d *Document) SetCursor(cursor *Cursor) {
	d.cursor = cloneCursor(cursor)
}

// RequestFocusCursor records the cursor a native view should apply on its next
// build. Unlike SetCursor, it also advances FocusRequest so the view can
// distinguish a new navigation request from the cursor it already displayed.
func (d *Document) RequestFocusCursor(cursor *Cursor) {
	if d == nil {
		return
	}
	d.cursor = cloneCursor(cursor)
	d.focusRequest++
}

func (d *Document) SetSelection(selection *Selection) {
	d.selection = cloneSelection(selection)
	d.tableSelect = nil
}

func (d *Document) ClearSelection() { d.selection = nil; d.tableSelect = nil }

func (d *Document) SelectAll() {
	if len(d.parts) == 0 {
		d.selection = nil
		return
	}
	d.selection = &Selection{First: d.parts[0].ID, Last: d.parts[len(d.parts)-1].ID}
	d.tableSelect = nil
}

func (d *Document) SelectedSource() string {
	rangeOfParts := d.selectedPartRange()
	if rangeOfParts == nil {
		return ""
	}
	return joinParts(d.parts[rangeOfParts[0] : rangeOfParts[1]+1])
}

func (d *Document) selectedPartRange() *[2]int {
	if d.selection == nil {
		return nil
	}
	first, last := -1, -1
	for i, part := range d.parts {
		if part.ID == d.selection.First {
			first = i
		}
		if part.ID == d.selection.Last {
			last = i
		}
	}
	if first < 0 || last < 0 {
		return nil
	}
	if first > last {
		first, last = last, first
	}
	return &[2]int{first, last}
}

func (d *Document) Bind(owner any, flush func()) {
	if owner != nil && flush != nil {
		d.flushers[owner] = flush
	}
}

func (d *Document) Unbind(owner any) {
	delete(d.flushers, owner)
	delete(d.compositions, owner)
	d.composing = len(d.compositions) != 0
}

func (d *Document) Flush() bool {
	for _, flush := range d.flushers {
		flush()
	}
	return d.error == ""
}

// SyncExternal replaces the document from an acknowledged host update, keeps
// matching block identities/cursor, and clears history just like the native
// editor's source-binding synchronization.
func (d *Document) SyncExternal(source string) {
	if d.Source() == source {
		return
	}
	oldParts := d.parts
	oldCursor := cloneCursor(d.cursor)
	d.parts = d.parseWithIDs(source, oldParts, 0)
	if oldCursor != nil && !containsPart(d.parts, oldCursor.Part) {
		oldCursor = nil
	}
	d.cursor = oldCursor
	d.selection = nil
	d.tableSelect = nil
	d.compositions = make(map[any]bool)
	d.composing = false
	d.error = ""
	d.undo = nil
	d.redo = nil
	d.version++
}

// ReplaceSource records a user's raw-source edit in the document history.
// Unlike an acknowledged external synchronization, it must remain undoable.
// Parsing only refreshes the range map; the supplied source is kept verbatim.
func (d *Document) ReplaceSource(source string, coalesce bool) error {
	if d.Source() == source {
		return nil
	}
	next := d.parseWithIDs(source, d.parts, 0)
	if err := d.commit(next, 0, coalesce); err != nil {
		return err
	}
	d.selection, d.tableSelect = nil, nil
	if d.cursor != nil && !containsPart(d.parts, d.cursor.Part) {
		d.cursor = nil
	}
	return nil
}

func (d *Document) ReplacePart(partID uint64, value string, coalesce bool) error {
	if d.selection != nil {
		return d.ReplaceSelection(value)
	}
	index := d.indexOf(partID)
	if index < 0 {
		return fmt.Errorf("Markdown 块不存在：%d", partID)
	}
	part := d.parts[index]
	if part.Source == value {
		return nil
	}
	next := cloneParts(d.parts)
	delta := len(value) - len(part.Source)
	next[index].Source = value
	next[index].Range.End += delta
	next[index].Nodes = reparsePart(value, part.Range.Start, part.Kind)
	for i := index + 1; i < len(next); i++ {
		next[i].Range.Start += delta
		next[i].Range.End += delta
		next[i].Nodes = shiftNodes(next[i].Nodes, delta)
	}
	return d.commit(next, partID, coalesce)
}

// ReplaceRange edits a local UTF-8 range inside one source-preserving part.
func (d *Document) ReplaceRange(partID uint64, start, end int, value string, coalesce bool) error {
	index := d.indexOf(partID)
	if index < 0 {
		return fmt.Errorf("Markdown 块不存在：%d", partID)
	}
	original := d.parts[index].Source
	if start < 0 || end < start || end > len(original) {
		return errors.New("Markdown 编辑范围无效")
	}
	return d.ReplacePart(partID, original[:start]+value+original[end:], coalesce)
}

func (d *Document) ReplaceSelection(value string) error {
	selected := d.selectedPartRange()
	if selected == nil {
		return errors.New("没有可替换的 Markdown 选区")
	}
	replacement, _ := parseDocument(value)
	for i := range replacement {
		replacement[i].ID = d.newID()
	}
	combined := make([]Part, 0, len(d.parts)-(selected[1]-selected[0]+1)+len(replacement))
	combined = append(combined, cloneParts(d.parts[:selected[0]])...)
	combined = append(combined, replacement...)
	combined = append(combined, cloneParts(d.parts[selected[1]+1:])...)
	combined = recalculateRanges(combined)
	previousSelection, previousTableSelection := d.selection, d.tableSelect
	d.selection = nil
	d.tableSelect = nil
	if err := d.commit(combined, replacement[0].ID, false); err != nil {
		d.selection, d.tableSelect = previousSelection, previousTableSelection
		return err
	}
	d.cursor = &Cursor{Part: replacement[0].ID}
	d.focusRequest++
	return nil
}

func (d *Document) JoinAdjacent(firstID, secondID uint64) error {
	index := d.indexOf(firstID)
	if index < 0 || index+1 >= len(d.parts) || d.parts[index+1].ID != secondID {
		return errors.New("Markdown 块不相邻")
	}
	if d.parts[index].Kind != PartRich || d.parts[index+1].Kind != PartRich {
		return errors.New("只有相邻富文本块可以合并")
	}
	first, second := d.parts[index], d.parts[index+1]
	joined := strings.TrimRight(first.Source, "\r\n") + second.Source
	next := cloneParts(d.parts)
	next[index].Source = joined
	next[index].Range.End += len(joined) - len(first.Source)
	next[index].Nodes = reparsePart(joined, first.Range.Start, PartRich)
	next = append(next[:index+1], next[index+2:]...)
	return d.commit(recalculateRanges(next), firstID, false)
}

func (d *Document) InsertParagraph(after uint64) uint64 {
	index := d.indexOf(after)
	if index < 0 {
		index = len(d.parts) - 1
	}
	part := Part{ID: d.newID(), Kind: PartRich, Source: detectNewline(d.Source())}
	next := cloneParts(d.parts)
	insertAt := index + 1
	if insertAt < 0 {
		insertAt = 0
	}
	next = append(next, Part{})
	copy(next[insertAt+1:], next[insertAt:])
	next[insertAt] = part
	if err := d.commit(recalculateRanges(next), part.ID, false); err != nil {
		return 0
	}
	d.cursor = &Cursor{Part: part.ID}
	d.focusRequest++
	return part.ID
}

func (d *Document) Format(request FormatRequest) error {
	index := d.indexOf(request.PartID)
	if index < 0 {
		return fmt.Errorf("Markdown 块不存在：%d", request.PartID)
	}
	part := d.parts[index]
	if part.Kind != PartRich {
		return fmt.Errorf("%s 块不支持富文本格式化", part.Kind)
	}
	if request.Start < 0 || request.End < request.Start || request.End > len(part.Source) {
		return errors.New("格式化范围无效")
	}
	if request.Start == request.End {
		return errors.New("请先选择要格式化的文本")
	}
	selected := part.Source[request.Start:request.End]
	var replacement string
	switch request.Command {
	case FormatBold:
		replacement = toggleDelimited(selected, "**")
	case FormatItalic:
		replacement = toggleDelimited(selected, "*")
	case FormatStrike:
		replacement = toggleDelimited(selected, "~~")
	case FormatInlineCode:
		replacement = toggleCode(selected)
	case FormatLink:
		if strings.HasPrefix(selected, "[") && strings.Contains(selected, "](") && strings.HasSuffix(selected, ")") {
			replacement = selected
		} else if request.Value == "" {
			replacement = "[" + selected + "]()"
		} else {
			replacement = "[" + selected + "](" + request.Value + ")"
		}
	case FormatHeading:
		return d.formatHeading(index, request)
	case FormatUnorderedList, FormatOrderedList:
		return d.formatList(index, request)
	default:
		return fmt.Errorf("Markdown 格式命令不支持：%s", request.Command)
	}
	return d.ReplaceRange(request.PartID, request.Start, request.End, replacement, false)
}

func (d *Document) formatHeading(index int, request FormatRequest) error {
	part := d.parts[index]
	level := request.Level
	if level < 0 || level > 6 {
		return errors.New("标题级别必须在 0 到 6 之间")
	}
	start, end := lineBounds(part.Source, request.Start, request.End)
	line := part.Source[start:end]
	lineEnd := strings.TrimRight(line, "\r\n")
	newline := line[len(lineEnd):]
	indent := len(lineEnd) - len(strings.TrimLeft(lineEnd, " \t"))
	content := lineEnd[indent:]
	content = regexp.MustCompile(`^#{1,6}[ \t]+`).ReplaceAllString(content, "")
	if level > 0 {
		content = strings.Repeat("#", level) + " " + content
	}
	return d.ReplaceRange(request.PartID, start, end, lineEnd[:indent]+content+newline, false)
}

func (d *Document) formatList(index int, request FormatRequest) error {
	part := d.parts[index]
	start, end := lineBounds(part.Source, request.Start, request.End)
	selection := part.Source[start:end]
	lines := scanLines(selection)
	if len(lines) == 0 {
		return nil
	}
	ordered := request.Command == FormatOrderedList
	allTarget := true
	for _, line := range lines {
		match, matched := markdowntext.ParseListMarker(line.text)
		if !matched || (ordered && (len(match.Marker) == 1 || match.Marker[0] < '0' || match.Marker[0] > '9')) || (!ordered && len(match.Marker) != 1) {
			allTarget = false
			break
		}
	}
	var out strings.Builder
	number := 1
	for _, line := range lines {
		text := line.text
		ending := part.Source[start+line.contentEnd : start+line.end]
		if allTarget {
			if match, matched := markdowntext.ParseListMarker(text); matched {
				text = match.Indent + strings.TrimLeft(text[len(match.Indent)+len(match.Marker):], " \t")
			}
		} else if strings.TrimSpace(text) != "" {
			match, matched := markdowntext.ParseListMarker(text)
			indent, content := "", text
			if matched {
				indent, content = match.Indent, text[len(match.Indent)+len(match.Marker):]
				content = strings.TrimLeft(content, " \t")
			}
			if ordered {
				text = indent + fmt.Sprintf("%d. ", number) + content
			} else {
				text = indent + "- " + content
			}
			number++
		}
		out.WriteString(text)
		out.WriteString(ending)
	}
	return d.ReplaceRange(request.PartID, start, end, out.String(), false)
}

func (d *Document) SetTaskChecked(partID uint64, taskIndex int, checked bool) error {
	index := d.indexOf(partID)
	if index < 0 {
		return fmt.Errorf("Markdown 块不存在：%d", partID)
	}
	part := d.parts[index]
	matches := markdowntext.TaskCheckboxRanges(part.Source)
	if taskIndex < 0 || taskIndex >= len(matches) {
		return errors.New("任务复选框不存在")
	}
	checkAt := matches[taskIndex].Start
	value := " "
	if checked {
		value = "x"
	}
	return d.ReplaceRange(partID, checkAt, checkAt+1, value, false)
}

func (d *Document) SetCode(partID uint64, code string) error {
	index := d.indexOf(partID)
	if index < 0 {
		return errors.New("代码块不存在")
	}
	return d.ReplacePart(partID, ReplaceFenceCode(d.parts[index].Source, code), true)
}

func (d *Document) SetTableCell(partID uint64, row, column int, value string) error {
	index := d.indexOf(partID)
	if index < 0 || d.parts[index].Kind != PartTable {
		return errors.New("Markdown 表格不存在")
	}
	rows := TableCells(d.parts[index].Source)
	if row < 0 || row >= len(rows) || column < 0 || column >= len(rows[row]) {
		return errors.New("Markdown 表格单元格不存在")
	}
	cell := rows[row][column]
	return d.ReplaceRange(partID, cell.Range.Start, cell.Range.End, escapeTableCell(value), true)
}

func escapeTableCell(value string) string {
	value = strings.TrimRight(value, "\r\n")
	value = strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		if value[i] == '|' {
			backslashes := 0
			for j := i - 1; j >= 0 && value[j] == '\\'; j-- {
				backslashes++
			}
			if backslashes%2 == 0 {
				out.WriteByte('\\')
			}
		}
		if value[i] == '\n' {
			out.WriteString("<br>")
		} else {
			out.WriteByte(value[i])
		}
	}
	return out.String()
}

func (d *Document) SelectTableCells(partID uint64, firstRow, lastRow, firstColumn, lastColumn int) error {
	index := d.indexOf(partID)
	if index < 0 || d.parts[index].Kind != PartTable {
		return errors.New("Markdown 表格不存在")
	}
	table, ok := ParseTable(d.parts[index].Source)
	if !ok {
		return errors.New("Markdown 表格格式无效")
	}
	if firstRow > lastRow {
		firstRow, lastRow = lastRow, firstRow
	}
	if firstColumn > lastColumn {
		firstColumn, lastColumn = lastColumn, firstColumn
	}
	if firstRow < 0 || lastRow >= len(table.Rows) || firstColumn < 0 || lastColumn >= len(table.Rows[0]) {
		return errors.New("Markdown 表格选区超出范围")
	}
	d.selection = nil
	d.tableSelect = &TableSelection{PartID: partID, FirstRow: firstRow, LastRow: lastRow, FirstColumn: firstColumn, LastColumn: lastColumn}
	return nil
}

func (d *Document) CopyTableSelection() (string, bool) {
	if d.tableSelect == nil {
		return "", false
	}
	index := d.indexOf(d.tableSelect.PartID)
	if index < 0 {
		return "", false
	}
	table, ok := ParseTable(d.parts[index].Source)
	if !ok {
		return "", false
	}
	s := d.tableSelect
	return TableFragment(table, s.FirstRow, s.LastRow, s.FirstColumn, s.LastColumn), true
}

func (d *Document) Undo() bool { return d.restore(&d.undo, &d.redo) }
func (d *Document) Redo() bool { return d.restore(&d.redo, &d.undo) }

func (d *Document) restore(from, to *[]documentSnapshot) bool {
	if d.composing || len(*from) == 0 {
		return false
	}
	restored := (*from)[len(*from)-1]
	*from = (*from)[:len(*from)-1]
	*to = append(*to, snapshotOf(d.parts, d.cursor))
	d.parts = cloneParts(restored.parts)
	d.cursor = cloneCursor(restored.cursor)
	d.selection, d.tableSelect = nil, nil
	d.version++
	d.error = ""
	d.lastPart = 0
	d.lastChange = time.Time{}
	d.focusRequest++
	return true
}

func (d *Document) commit(next []Part, part uint64, coalesce bool) error {
	if len(joinParts(next)) > d.MaxSourceBytes() {
		d.error = "Markdown 内容超过 3 MiB（含 BOM），无法保存本次输入。请缩小文档后重试。"
		return errors.New(d.error)
	}
	now := d.clock()
	if !coalesce || d.lastPart != part || d.lastChange.IsZero() || now.Sub(d.lastChange) > 650*time.Millisecond || len(d.undo) == 0 {
		d.undo = append(d.undo, snapshotOf(d.parts, d.cursor))
	}
	d.trimUndo()
	d.redo = nil
	d.parts = cloneParts(next)
	d.version++
	d.lastPart, d.lastChange, d.error = part, now, ""
	return nil
}

func (d *Document) trimUndo() {
	for len(d.undo) > 100 || (len(d.undo) > 1 && historyBytes(d.undo) > 8_000_000) {
		d.undo = d.undo[1:]
	}
}

func (d *Document) parseWithIDs(source string, previous []Part, preferred uint64) []Part {
	parts, _ := parseDocument(source)
	used := make(map[uint64]bool)
	for i := range parts {
		var match *Part
		for j := range previous {
			if !used[previous[j].ID] && previous[j].Kind == parts[i].Kind && previous[j].Source == parts[i].Source {
				match = &previous[j]
				break
			}
		}
		if match == nil && len(previous) == len(parts) && i < len(previous) && !used[previous[i].ID] && previous[i].Kind == parts[i].Kind {
			match = &previous[i]
		}
		if match == nil && preferred != 0 && i < len(previous) && previous[i].ID == preferred && !used[preferred] {
			match = &previous[i]
		}
		if match != nil {
			parts[i].ID = match.ID
			used[match.ID] = true
		} else {
			parts[i].ID = d.newID()
		}
	}
	return parts
}

func (d *Document) newID() uint64 { d.nextID++; return d.nextID }
func (d *Document) indexOf(id uint64) int {
	for i := range d.parts {
		if d.parts[i].ID == id {
			return i
		}
	}
	return -1
}

func joinParts(parts []Part) string {
	var out strings.Builder
	for _, part := range parts {
		out.WriteString(part.Source)
	}
	return out.String()
}

func cloneParts(parts []Part) []Part {
	result := make([]Part, len(parts))
	for i := range parts {
		result[i] = parts[i]
		result[i].Nodes = cloneNodes(parts[i].Nodes)
	}
	return result
}

func cloneNodes(nodes []Node) []Node {
	result := make([]Node, len(nodes))
	for i := range nodes {
		result[i] = nodes[i]
		if nodes[i].Checked != nil {
			flag := *nodes[i].Checked
			result[i].Checked = &flag
		}
		if nodes[i].Attributes != nil {
			result[i].Attributes = map[string]string{}
			for k, v := range nodes[i].Attributes {
				result[i].Attributes[k] = v
			}
		}
		result[i].Children = cloneNodes(nodes[i].Children)
	}
	return result
}

func cloneCursor(cursor *Cursor) *Cursor {
	if cursor == nil {
		return nil
	}
	copy := *cursor
	return &copy
}
func cloneSelection(selection *Selection) *Selection {
	if selection == nil {
		return nil
	}
	copy := *selection
	return &copy
}

func cloneTableSelection(selection *TableSelection) *TableSelection {
	if selection == nil {
		return nil
	}
	copy := *selection
	return &copy
}
func containsPart(parts []Part, id uint64) bool {
	for _, part := range parts {
		if part.ID == id {
			return true
		}
	}
	return false
}

func snapshotOf(parts []Part, cursor *Cursor) documentSnapshot {
	return documentSnapshot{parts: cloneParts(parts), cursor: cloneCursor(cursor)}
}

func historyBytes(history []documentSnapshot) int64 {
	var total int64
	for _, snap := range history {
		for _, part := range snap.parts {
			total += int64(len(utf16.Encode([]rune(part.Source))) * 2)
		}
	}
	return total
}

func recalculateRanges(parts []Part) []Part {
	offset := 0
	for i := range parts {
		oldStart := parts[i].Range.Start
		delta := offset - oldStart
		parts[i].Range = SourceRange{Start: offset, End: offset + len(parts[i].Source)}
		parts[i].Nodes = shiftNodes(parts[i].Nodes, delta)
		offset += len(parts[i].Source)
	}
	return parts
}

func shiftNodes(nodes []Node, delta int) []Node {
	if delta == 0 {
		return cloneNodes(nodes)
	}
	result := cloneNodes(nodes)
	var shift func([]Node)
	shift = func(items []Node) {
		for i := range items {
			items[i].Range.Start += delta
			items[i].Range.End += delta
			shift(items[i].Children)
		}
	}
	shift(result)
	return result
}

func reparsePart(source string, offset int, kind PartKind) []Node {
	if kind == PartMetadata {
		return []Node{{Kind: "metadata", Range: SourceRange{Start: offset, End: offset + len(source)}, Text: source}}
	}
	parts, _ := parseDocument(source)
	var nodes []Node
	for _, part := range parts {
		nodes = append(nodes, shiftNodes(part.Nodes, offset)...)
	}
	if len(nodes) == 0 && source != "" {
		nodes = []Node{{Kind: string(kind), Range: SourceRange{Start: offset, End: offset + len(source)}, Text: source}}
	}
	return nodes
}

func toggleDelimited(value, marker string) string {
	if len(value) >= 2*len(marker) && strings.HasPrefix(value, marker) && strings.HasSuffix(value, marker) {
		return value[len(marker) : len(value)-len(marker)]
	}
	return marker + value + marker
}

func toggleCode(value string) string {
	if strings.HasPrefix(value, "`") && strings.HasSuffix(value, "`") {
		return strings.Trim(value, "`")
	}
	marker := "`"
	for strings.Contains(value, marker) {
		marker += "`"
	}
	pad := strings.HasPrefix(value, "`") || strings.HasSuffix(value, "`")
	if pad {
		return marker + " " + value + " " + marker
	}
	return marker + value + marker
}

func lineBounds(source string, start, end int) (int, int) {
	if start < 0 {
		start = 0
	}
	if end > len(source) {
		end = len(source)
	}
	lineStart := strings.LastIndex(source[:start], "\n") + 1
	lineEnd := strings.Index(source[end:], "\n")
	if lineEnd < 0 {
		return lineStart, len(source)
	}
	return lineStart, end + lineEnd + 1
}

func detectNewline(source string) string { return markdowntext.DetectNewline(source) }
