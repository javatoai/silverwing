package markdown

import (
	"errors"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func (s *EditorState) blockHasLinks(block int) bool {
	for _, in := range s.view.doc.Inlines(block) {
		if in.Kind == parser.Link {
			return true
		}
	}
	return false
}

type editorTableCell struct {
	block, start, end, row, column int
	sourceStart, sourceEnd         int
	omitted                        bool
}
type editorTable struct {
	block, start, end  int
	rows               [][]editorTableCell
	align              []parser.Align
	version            uint64
	firstPrefix        string
	continuationPrefix string
}

type editorTableHit struct {
	table editorTable
	cell  editorTableCell
	rect  ui.Rect
}

type editorTableContext struct {
	start, row, column int
	version            uint64
	valid              bool
}

func (s *EditorState) editingTable() (editorTable, editorTableCell, bool) {
	s.sync()
	if hit := s.focusedOmittedTableCell(); hit != nil {
		return hit.table, hit.cell, true
	}
	d := s.editingDocument()
	for i := 1; i < d.Len(); i++ {
		if d.Block(i).Kind != parser.Table {
			continue
		}
		t := s.readTable(d, i)
		for _, row := range t.rows {
			for _, cell := range row {
				if s.caret >= cell.sourceStart && s.caret <= cell.sourceEnd {
					return t, cell, true
				}
			}
		}
	}
	return editorTable{}, editorTableCell{}, false
}

func (s *EditorState) readTable(d *parser.Document, index int) editorTable {
	b := d.Block(index)
	start, end := s.blockRange(b)
	t := editorTable{block: index, start: start, end: end, align: b.Align, version: s.snapshot.Version()}
	for row := index + 1; row < b.Next; row = d.Block(row).Next {
		var cells []editorTableCell
		rowEnd := len(s.source)
		if next := strings.IndexByte(s.source[d.Block(row).Start:], '\n'); next >= 0 {
			rowEnd = d.Block(row).Start + next
		}
		for cell := row + 1; cell < d.Block(row).Next; cell = d.Block(cell).Next {
			cb := d.Block(cell)
			value := editorTableCell{block: cell, start: cb.Start, end: cb.End, row: len(t.rows), column: len(cells), omitted: cb.Start == rowEnd && cb.End == rowEnd,
				sourceStart: cb.Start, sourceEnd: cb.End}
			// Cell blocks trim Markdown padding. It still belongs to this
			// cell's source selection, but never to a neighboring delimiter
			// or the row's container prefix / physical newline.
			if !value.omitted {
				for value.sourceStart > d.Block(row).Start && (s.source[value.sourceStart-1] == ' ' || s.source[value.sourceStart-1] == '\t') {
					value.sourceStart--
				}
				for value.sourceEnd < rowEnd && (s.source[value.sourceEnd] == ' ' || s.source[value.sourceEnd] == '\t') {
					value.sourceEnd++
				}
			}
			cells = append(cells, value)
		}
		t.rows = append(t.rows, cells)
	}
	if len(t.rows) > 0 && len(t.rows[0]) > 0 {
		t.firstPrefix = s.source[start:t.rows[0][0].start]
		if at := strings.LastIndexByte(t.firstPrefix, '|'); at >= 0 {
			t.firstPrefix = t.firstPrefix[:at]
		}
		// The delimiter row already contains the exact continuation prefix,
		// including quote markers and list indentation. Its first non-prefix
		// character is table syntax (a pipe, colon or dash), never a list
		// marker which should be repeated on subsequent rows.
		if next := strings.IndexByte(s.source[start:end], '\n'); next >= 0 {
			a := start + next + 1
			z := a
			for z < end && (s.source[z] == ' ' || s.source[z] == '\t' || s.source[z] == '>') {
				z++
			}
			t.continuationPrefix = s.source[a:z]
		}
	}
	return t
}

func (s *EditorState) tableCellAt(x, y float32) *editorTableHit {
	if !s.opts.InPlaceTables || s.SourceMode {
		return nil
	}
	for _, hit := range s.tableCells {
		if hit.table.version == s.snapshot.Version() && hit.rect.Contains(x+s.rect.X, y+s.rect.Y) {
			return hit
		}
	}
	return nil
}

func (s *EditorState) tableContextAt(x, y float32) *editorTableContext {
	target := &editorTableContext{version: s.snapshot.Version()}
	if hit := s.tableCellAt(x, y); hit != nil {
		target.start, target.row, target.column = hit.table.start, hit.cell.row, hit.cell.column
		target.valid = true
	}
	return target
}

func (s *EditorState) enterTableCell(t editorTable, cell editorTableCell) (editorTableCell, bool) {
	if !cell.omitted {
		return cell, true
	}
	s.sync()
	if s.opts.ReadOnly || t.version != s.snapshot.Version() {
		return editorTableCell{}, false
	}
	return cell, true
}

func (s *EditorState) omittedCellPrefix(t editorTable, cell editorTableCell) string {
	first := cell.column
	for first > 0 && t.rows[cell.row][first-1].omitted {
		first--
	}
	end := cell.start
	start := strings.LastIndexByte(s.source[:end], '\n') + 1
	line := strings.TrimRight(s.source[start:end], " \t")
	closing := strings.HasSuffix(line, "|")
	if closing {
		slashes := 0
		for i := len(line) - 2; i >= 0 && line[i] == '\\'; i-- {
			slashes++
		}
		closing = slashes%2 == 0
	}
	prefix := strings.Repeat("  |", cell.column-first) + " "
	if !closing {
		prefix = " |" + prefix
	}
	return prefix
}

func (s *EditorState) tableKeepsGrid(index int) bool {
	if !s.composing {
		return true
	}
	t := s.readTable(s.view.doc, index)
	if s.activeEnd <= t.start || s.activeStart >= t.end {
		return true
	}
	for _, row := range t.rows {
		for _, cell := range row {
			if s.composeStart >= cell.sourceStart && s.composeEnd <= cell.sourceEnd {
				return true
			}
		}
	}
	return false
}

// tableBlock retains the grid even while its cell is active. The cell contains
// shaped display runs only; the root is still the sole input-method client.
func (s *EditorState) tableBlock(c *ui.Context, index int, base spanStyle) {
	d, th := s.view.doc, &s.view.th
	t := s.readTable(d, index)
	activeTable, activeCell, hasActive := s.editingTable()
	cols := len(t.align)
	if cols == 0 {
		return
	}
	widths := make([]float32, cols)
	padX, padY := th.em(th.TableCellPadding[0]), th.em(th.TableCellPadding[1])
	for _, row := range t.rows {
		for column, cell := range row {
			if column >= cols {
				break
			}
			p := s.view.para(cell.block, base)
			w := float32(0)
			for _, piece := range p.pieces {
				measure, _ := c.MeasureText(0, piece.spans...)
				w += measure
			}
			widths[column] = max(widths[column], max(th.em(6), min(w, th.em(th.TableMaxColumn)))+2*padX+1)
		}
	}
	wrap := ui.ScrollHorizontal(c).Key(t.start).AlignSelf(ui.Stretch)
	block := &editorBlock{start: s.virtualOffset(t.start, false), end: s.virtualOffset(t.end, true), element: wrap}
	s.blocks = append(s.blocks, block)
	wrap.Draw(func(_ *ui.Painter, r ui.Rect) { block.rect = r })
	wrap.Children(func() {
		ui.Column(c).Border(1, th.TableBorder).Radius(th.em(.3)).Clip().Shrink(0).Role(ui.RoleTable).Children(func() {
			for rowIndex, row := range t.rows {
				if !s.projectionCurrent() {
					return
				}
				line := ui.Row(c).Key(rowIndex).AlignItems(ui.Stretch).Role(ui.RoleRow)
				if rowIndex == 0 {
					line.Background(th.TableHeader)
				} else if rowIndex%2 == 0 {
					line.Background(th.TableStripe)
				}
				if rowIndex > 0 {
					line.BorderWidth(1, 0, 0, 0).BorderColor(th.TableBorder)
				}
				line.Children(func() {
					for column, cell := range row {
						if !s.projectionCurrent() {
							return
						}
						if column >= cols {
							break
						}
						box := ui.Column(c).Key(column).Width(widths[column]).MinHeight(th.FontSize*th.LineHeight+2*padY).Padding(padY, padX).Role(ui.RoleCell)
						if rowIndex == 0 {
							box.Role(ui.RoleColumnHeader)
						}
						if column > 0 {
							box.BorderWidth(0, 0, 0, 1).BorderColor(th.TableBorder)
						}
						hit := &editorTableHit{table: t, cell: cell}
						s.tableCells = append(s.tableCells, hit)
						box.Draw(func(_ *ui.Painter, r ui.Rect) { hit.rect = r })
						box.Children(func() {
							firstRun := len(s.runs)
							align := ui.Start
							if t.align[column] == parser.AlignCenter {
								align = ui.Center
							} else if t.align[column] == parser.AlignRight {
								align = ui.End
							}
							active := s.focused && !s.opts.ReadOnly && hasActive && activeTable.start == t.start && activeCell.block == cell.block
							if active && s.composing {
								font := ui.Font{Family: th.Font, Size: th.FontSize}
								if rowIndex == 0 {
									font.Weight = th.StrongWeight
								}
								s.literalRun(c, cell.sourceStart, cell.sourceEnd, font, th.Text, th.LineHeight, true).FillWidth().TextAlign(align)
							} else {
								style := base
								if rowIndex == 0 {
									style.weight = th.StrongWeight
								}
								if cell.start == cell.end {
									s.literalRun(c, cell.start, cell.end, ui.Font{Family: th.Font, Size: th.FontSize}, th.Text, th.LineHeight).FillWidth().TextAlign(align)
								} else {
									s.view.paragraph(c, cell.block, style).TextAlign(align)
								}
							}
							if firstRun < len(s.runs) {
								for _, run := range s.runs[firstRun:] {
									run.tableCellBlock = cell.block
									run.align, run.direction = align, c.Direction()
								}
								// Like hidden link wrappers, trimmed cell trivia shares
								// the formatted body's visible edges. Explicit ownership
								// keeps the native caret in this cell without rendering
								// its Markdown source or using a nearest unrelated run.
								first, last := s.runs[firstRun], s.runs[len(s.runs)-1]
								first.caretStart = min(first.caretStart, s.virtualOffset(cell.sourceStart, false))
								last.caretEnd = max(last.caretEnd, s.virtualOffset(cell.sourceEnd, true))
							}
						})
					}
				})
			}
		})
	})
}

func (s *EditorState) tableKey(key ui.Key, mods ui.Modifiers) bool {
	if !s.opts.InPlaceTables || s.SourceMode || s.composing {
		return false
	}
	t, cell, ok := s.editingTable()
	if !ok {
		return false
	}
	if s.tableNavigate(t, cell, key, mods) {
		return true
	}
	if s.opts.ReadOnly {
		return false
	}
	if s.opts.ContinueBlocks && key == ui.KeyTab && s.anchor != s.caret {
		a, z := min(s.anchor, s.caret), max(s.anchor, s.caret)
		if a < t.start || z > t.end {
			return false // a directional caret cannot claim an outside selection
		}
	}
	if mods&^(ui.Shift) != 0 {
		return false
	}
	if key == ui.KeyBackspace && s.anchor == s.caret && (s.caret == cell.start || s.caret == cell.sourceStart) {
		return true
	}
	if key == ui.KeyDelete && s.anchor == s.caret && (s.caret == cell.end || s.caret == cell.sourceEnd) {
		return true
	}
	if s.anchor == s.caret && key == ui.KeyBackspace {
		at := s.caret
		if at >= cell.start+4 && s.source[at-4:at] == "<br>" {
			s.replace(at-4, at, "", editor.EditDeleting)
			return true
		}
		if at >= cell.start+2 && s.source[at-2:at] == `\|` {
			s.replace(at-2, at, "", editor.EditDeleting)
			return true
		}
	}
	if s.anchor == s.caret && key == ui.KeyDelete {
		at := s.caret
		if at+4 <= cell.end && s.source[at:at+4] == "<br>" {
			s.replace(at, at+4, "", editor.EditDeleting)
			return true
		}
		if at+2 <= cell.end && s.source[at:at+2] == `\|` {
			s.replace(at, at+2, "", editor.EditDeleting)
			return true
		}
	}
	if key == ui.KeyEnter && mods&ui.Shift != 0 {
		s.buffer.EndUndoGroup()
		s.replace(min(s.anchor, s.caret), max(s.anchor, s.caret), "<br>", editor.EditOther)
		s.buffer.EndUndoGroup()
		return true
	}
	if key != ui.KeyTab && key != ui.KeyEnter {
		return false
	}
	row, column := cell.row, cell.column
	if key == ui.KeyEnter {
		row++
	} else if mods&ui.Shift != 0 {
		column--
		if column < 0 {
			row--
			if row >= 0 {
				column = len(t.rows[row]) - 1
			}
		}
	} else {
		column++
		if column >= len(t.rows[row]) {
			row++
			column = 0
		}
	}
	if row < 0 {
		return true
	}
	if row >= len(t.rows) {
		if mods&ui.Shift != 0 {
			return true
		}
		if !s.tableStructure(t, "row-after", cell) {
			return true
		}
		t, _, ok = s.editingTable()
		if !ok {
			return true
		}
		row = min(row, len(t.rows)-1)
	}
	if row < len(t.rows) && column < len(t.rows[row]) {
		if next, ok := s.enterTableCell(t, t.rows[row][column]); ok {
			s.moveTo(next.start, ui.TextDownstream, false)
			if next.omitted {
				s.omittedTableCell = &editorTableHit{table: t, cell: next}
			}
		}
	}
	return true
}

func escapeEditorTableText(text string) string {
	escaped, _, _ := escapeEditorTableTextAt(text, 0, len(text))
	return escaped
}

func escapeEditorTableTextAt(text string, slashes, endpoint int) (string, int, int) {
	// Map the requested endpoint through the very same normalization and
	// escaping pass as the retained suffix. Raw suffix bytes cannot be
	// subtracted from a longer escaped result.
	endpoint = len(normalizedText(text[:byteBoundary(text, endpoint)]))
	text = normalizedText(text)
	var out strings.Builder
	mapped := 0
	for at, ch := range text {
		if at == endpoint {
			mapped = out.Len()
		}
		if ch == '|' && slashes%2 == 0 {
			out.WriteByte('\\')
		}
		if ch == '\n' {
			out.WriteString("<br>")
		} else {
			out.WriteRune(ch)
		}
		if ch == '\\' {
			slashes++
		} else {
			slashes = 0
		}
	}
	if endpoint == len(text) {
		mapped = out.Len()
	}
	return out.String(), slashes, mapped
}

func (s *EditorState) tableMenu(menu *ui.Menu) {
	if !s.opts.InPlaceTables || s.SourceMode || s.opts.ReadOnly {
		return
	}
	t, cell, ok := s.editingTable()
	if target := s.contextTable; target != nil {
		ok = false
		if target.valid && target.version == s.snapshot.Version() {
			d := s.view.doc
			if d == nil || d.Source() != s.source {
				d = parser.New(s.view.ext)
				d.SetWithPolicy(s.source, s.sourcePrefixEnd(), s.literalFenceLines())
			}
			for i := 1; i < d.Len(); i++ {
				if d.Block(i).Kind != parser.Table {
					continue
				}
				candidate := s.readTable(d, i)
				if candidate.start == target.start && target.row < len(candidate.rows) && target.column < len(candidate.rows[target.row]) {
					t, cell, ok = candidate, candidate.rows[target.row][target.column], true
					break
				}
			}
		}
	}
	if !ok {
		return
	}
	menu.Separator()
	if s.opts.SmartEditing {
		for _, placement := range []struct {
			label string
			at    int
		}{{"表格上方插入段落", t.start}, {"表格下方插入段落", t.end}} {
			if menu.Item(placement.label).Chosen() {
				text := s.buffer.LineEnding() + s.buffer.LineEnding()
				s.report(s.ApplyEdits([]editor.Edit{{Start: placement.at, End: placement.at, Text: text}}, placement.at, placement.at, s.snapshot.Version()))
			}
		}
	}
	for _, item := range []struct {
		action, label string
		disabled      bool
	}{
		{"row-before", "上方插入行", cell.row == 0}, {"row-after", "下方插入行", false}, {"row-delete", "删除当前行", cell.row == 0},
		{"column-before", "左侧插入列", false}, {"column-after", "右侧插入列", false}, {"column-delete", "删除当前列", len(t.align) <= 1},
		{"align-left", "左对齐", false}, {"align-center", "居中对齐", false}, {"align-right", "右对齐", false},
	} {
		if menu.Item(item.label).Disabled(item.disabled).Chosen() {
			s.tableStructure(t, item.action, cell)
		}
	}
}

func (s *EditorState) tableStructure(t editorTable, action string, cell editorTableCell) bool {
	s.sync()
	if t.version != s.snapshot.Version() {
		return false
	}
	rows := make([][]string, len(t.rows))
	for i, row := range t.rows {
		for _, value := range row {
			rows[i] = append(rows[i], s.source[value.start:value.end])
		}
	}
	align := append([]parser.Align(nil), t.align...)
	row, column := cell.row, cell.column
	blank := func() []string { return make([]string, len(align)) }
	switch action {
	case "row-before", "row-after":
		if action == "row-after" {
			row++
		}
		row = max(1, row)
		rows = append(rows, nil)
		copy(rows[row+1:], rows[row:])
		rows[row] = blank()
	case "row-delete":
		if row == 0 {
			return false
		}
		rows = append(rows[:row], rows[row+1:]...)
		row = min(row, len(rows)-1)
	case "column-before", "column-after":
		if action == "column-after" {
			column++
		}
		for i, values := range rows {
			values = append(values, "")
			copy(values[column+1:], values[column:])
			values[column] = ""
			rows[i] = values
		}
		align = append(align, parser.AlignNone)
		copy(align[column+1:], align[column:])
		align[column] = parser.AlignNone
	case "column-delete":
		if len(align) <= 1 {
			return false
		}
		for i, values := range rows {
			rows[i] = append(values[:column], values[column+1:]...)
		}
		align = append(align[:column], align[column+1:]...)
		column = min(column, len(align)-1)
	case "align-left":
		align[column] = parser.AlignLeft
	case "align-center":
		align[column] = parser.AlignCenter
	case "align-right":
		align[column] = parser.AlignRight
	default:
		return false
	}
	return s.writeTable(t, rows, align, row, column)
}

func (s *EditorState) writeTable(t editorTable, rows [][]string, align []parser.Align, row, column int) bool {
	s.sync()
	if t.version != s.snapshot.Version() {
		return false
	}
	var out strings.Builder
	for i, values := range rows {
		if i == 0 {
			out.WriteString(t.firstPrefix)
		} else {
			out.WriteString(t.continuationPrefix)
		}
		out.WriteString("| ")
		out.WriteString(strings.Join(values, " | "))
		out.WriteString(" |\n")
		if i == 0 {
			out.WriteString(t.continuationPrefix)
			out.WriteString("|")
			for _, a := range align {
				switch a {
				case parser.AlignLeft:
					out.WriteString(" :--- |")
				case parser.AlignCenter:
					out.WriteString(" :---: |")
				case parser.AlignRight:
					out.WriteString(" ---: |")
				default:
					out.WriteString(" --- |")
				}
			}
			out.WriteByte('\n')
		}
	}
	text := out.String()
	if t.end == len(s.source) && !strings.HasSuffix(s.source, "\n") {
		text = strings.TrimSuffix(text, "\n")
	}
	text = strings.ReplaceAll(text, "\n", s.buffer.LineEnding())
	if s.ReplaceRange(t.start, t.end, text, s.snapshot.Version()) != nil {
		return false
	}
	d := parser.New(s.view.ext)
	d.SetWithPolicy(s.source, s.sourcePrefixEnd(), s.literalFenceLines())
	for i := 1; i < d.Len(); i++ {
		if d.Block(i).Kind == parser.Table && d.Block(i).Start >= t.start {
			next := s.readTable(d, i)
			row = min(row, len(next.rows)-1)
			if row >= 0 && column < len(next.rows[row]) {
				s.moveTo(next.rows[row][column].start, ui.TextDownstream, false)
			}
			break
		}
	}
	return true
}

func (s *EditorState) tablePaste(text string) bool {
	if !s.opts.InPlaceTables || s.SourceMode || s.opts.ReadOnly || !strings.Contains(text, "\t") {
		return false
	}
	t, cell, ok := s.editingTable()
	a, z := min(s.anchor, s.caret), max(s.anchor, s.caret)
	if !ok || a < t.start || z > t.end || a == 0 && z == len(s.source) {
		return false
	}
	if a != z {
		// A grid paste begins at the first selected cell, in either
		// selection direction. Outside/whole-document selections retain
		// the root's ordinary replacement semantics.
		found := false
		for _, row := range t.rows {
			for _, candidate := range row {
				if a >= candidate.sourceStart && a <= candidate.sourceEnd {
					cell, found = candidate, true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return false
		}
	}
	lines := strings.Split(strings.TrimSuffix(strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n"), "\n"), "\n")
	columns := len(t.align)
	for _, line := range lines {
		columns = max(columns, cell.column+strings.Count(line, "\t")+1)
	}
	rowCount := max(len(t.rows), cell.row+len(lines))
	// Match the maximum cell count of explicit table creation. Check the
	// product before allocation: a small ragged TSV may describe a huge grid.
	if columns <= 0 || rowCount > 100000/columns {
		s.report(errors.New("表格粘贴超过单元格数量上限"))
		return true
	}
	if limit, bounded := s.buffer.SourceLimit(); bounded {
		// A normalized Buffer may remember CRLF while budgeting LF bytes.
		// Use LF for this allocation lower bound; the final transaction checks
		// the exact representation, including any preserved CRLF bytes.
		const eol = 1
		minimum := int64(rowCount)*(3*int64(columns)+1+int64(eol)) + int64(len(t.firstPrefix)) + int64(rowCount-1)*int64(len(t.continuationPrefix))
		minimum += 6*int64(columns) + 1 + int64(eol) + int64(len(t.continuationPrefix))
		if t.end == len(s.source) && !strings.HasSuffix(s.source, "\n") {
			minimum -= int64(eol)
		}
		outside := len(s.snapshot.OriginalText()) - len(s.snapshot.OriginalSlice(t.start, t.end))
		if minimum > int64(limit)-int64(outside) {
			s.report(errors.New("Markdown edit exceeds the source size limit"))
			return true
		}
	}
	values := make([][]string, len(lines))
	for i, line := range lines {
		values[i] = strings.Split(line, "\t")
	}
	rows := make([][]string, rowCount)
	for i := range rows {
		rows[i] = make([]string, columns)
		if i < len(t.rows) {
			for k, current := range t.rows[i] {
				rows[i][k] = s.source[current.start:current.end]
			}
		}
	}
	for i, line := range values {
		for k, value := range line {
			rows[cell.row+i][cell.column+k] = escapeEditorTableText(value)
		}
	}
	align := append([]parser.Align(nil), t.align...)
	for len(align) < columns {
		align = append(align, parser.AlignNone)
	}
	s.writeTable(t, rows, align, cell.row+len(values)-1, cell.column+len(values[len(values)-1])-1)
	return true
}

func (s *EditorState) tableCopy(a, z int) (string, bool) {
	if !s.opts.InPlaceTables || s.SourceMode || a == 0 && z == len(s.source) {
		return "", false
	}
	t, _, ok := s.editingTable()
	if !ok || a < t.start || z > t.end {
		return "", false
	}
	d := s.view.doc
	if d.Source() != s.source {
		d = parser.New(s.view.ext)
		d.SetWithPolicy(s.source, s.sourcePrefixEnd(), s.literalFenceLines())
	}
	visible := make(map[int]string)
	for _, block := range TextBlocks(d) {
		visible[block.Index] = block.Text
	}
	var lines []string
	count := 0
	for _, row := range t.rows {
		var cells []string
		for _, cell := range row {
			if cell.start >= a && cell.end <= z {
				cells = append(cells, visible[cell.block])
				count++
			}
		}
		if len(cells) > 0 {
			lines = append(lines, strings.Join(cells, "\t"))
		}
	}
	return strings.Join(lines, "\n"), count > 1
}

// A printable edit cannot introduce an unescaped structural pipe or physical
// newline into one cell. Structure commands use ReplaceRange directly.
func (s *EditorState) tableInputText(a, z int, text string, endpoint int) (string, int) {
	if !s.opts.InPlaceTables || s.SourceMode || a == z && text == "" {
		return text, endpoint
	}
	t, cell, ok := s.editingTable()
	if !ok || a < cell.sourceStart || z > cell.sourceEnd {
		return text, endpoint
	}
	slashes := 0
	for at := a - 1; at >= cell.sourceStart && s.source[at] == '\\'; at-- {
		slashes++
	}
	escaped, trailing, mapped := escapeEditorTableTextAt(text, slashes, endpoint)
	if cell.omitted {
		prefix := s.omittedCellPrefix(t, cell)
		return prefix + escaped + " |", len(prefix) + mapped
	}
	next := z
	for next < cell.sourceEnd && s.source[next] == '\\' {
		trailing++
		next++
	}
	if next < len(s.source) && s.source[next] == '|' {
		// Inside the cell the following pipe must stay escaped. At the
		// source range's end it is a delimiter and must stay unescaped.
		if next < cell.sourceEnd && trailing%2 == 0 || next == cell.sourceEnd && trailing%2 != 0 {
			escaped += `\`
			if endpoint == len(text) {
				mapped = len(escaped)
			}
		}
	}
	return escaped, mapped
}
