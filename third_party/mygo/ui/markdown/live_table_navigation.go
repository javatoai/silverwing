package markdown

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

// Several omitted cells share one source offset. Their column remains a
// version-bound navigation intent until an explicit edit creates delimiters.
type editorOmittedCellHistory struct {
	selection          editorSelection
	start, row, column int
}

func (s *EditorState) focusedOmittedTableCell() *editorTableHit {
	hit := s.omittedTableCell
	if hit == nil || !s.opts.InPlaceTables || s.SourceMode || hit.table.version != s.snapshot.Version() || s.anchor != hit.cell.start || s.caret != hit.cell.start {
		return nil
	}
	return hit
}

func (s *EditorState) omittedCellRun(at int, shaped bool) (*editorRun, bool) {
	if hit := s.focusedOmittedTableCell(); hit != nil && at >= s.virtualOffset(hit.cell.sourceStart, false) && at <= s.virtualOffset(hit.cell.sourceEnd, true) {
		for _, run := range s.runs {
			if run.tableCellBlock == hit.cell.block && (!shaped || run.layout != nil) && at >= run.caretStart && at <= run.caretEnd {
				return run, true
			}
		}
		// Ownership is already known even if a later cell has not shaped in
		// this paint yet. Never draw in an earlier same-offset cell instead.
		return nil, true
	}
	return nil, false
}

func (s *EditorState) restoreOmittedTableCell(saved editorOmittedCellHistory) {
	d := s.editingDocument()
	for i := 1; i < d.Len(); i++ {
		if d.Block(i).Kind != parser.Table {
			continue
		}
		t := s.readTable(d, i)
		if t.start != saved.start || saved.row >= len(t.rows) || saved.column >= len(t.rows[saved.row]) {
			continue
		}
		cell := t.rows[saved.row][saved.column]
		if cell.omitted && s.anchor == cell.start && s.caret == cell.start {
			s.omittedTableCell = &editorTableHit{table: t, cell: cell}
			s.codeGapFocus.valid = false
		}
		return
	}
}

func (s *EditorState) tableCellRun(cell editorTableCell, last bool) *editorRun {
	var found *editorRun
	for _, run := range s.runs {
		if run.layout == nil || run.tableCellBlock != cell.block || run.caretStart < cell.sourceStart || run.caretEnd > cell.sourceEnd {
			continue
		}
		if found == nil || !last && run.caretStart < found.caretStart || last && run.caretEnd > found.caretEnd {
			found = run
		}
	}
	return found
}

// Directional navigation follows the actual cell's visual lines, rather than
// looking one line-height away in the whole grid (which can hit empty space
// beneath a short cell when its neighbor makes the row tall).
func (s *EditorState) tableNavigate(t editorTable, cell editorTableCell, key ui.Key, mods ui.Modifiers) bool {
	if mods != 0 || s.anchor != s.caret {
		return false
	}
	if key != ui.KeyLeft && key != ui.KeyRight && key != ui.KeyUp && key != ui.KeyDown {
		return false
	}
	first, last := s.tableCellRun(cell, false), s.tableCellRun(cell, true)
	if first == nil || last == nil {
		return false
	}
	row, column, end := cell.row, cell.column, false
	switch key {
	case ui.KeyLeft:
		if column == 0 || s.caret > first.boundaries[0] {
			return false
		}
		column--
		end = true
	case ui.KeyRight:
		if column+1 >= len(t.rows[row]) || s.caret < last.boundaries[len(last.boundaries)-1] {
			return false
		}
		column++
	case ui.KeyUp, ui.KeyDown:
		current, ok := s.ownedCaretBounds(s.caret)
		if !ok {
			return false
		}
		if !s.hasDesired {
			s.desiredX, s.hasDesired = current.X, true
		}
		if key == ui.KeyUp {
			top := first.layout.CaretAt(ui.TextCaretPosition{Index: 0, Affinity: ui.TextDownstream})
			if row == 0 || current.Y+s.rect.Y > first.rect.Y+top.Y+.1 {
				return false
			}
			row--
			end = true
		} else {
			bottom := last.layout.CaretAt(ui.TextCaretPosition{Index: last.units[len(last.units)-1], Affinity: ui.TextUpstream})
			if row+1 >= len(t.rows) || current.Y+s.rect.Y < last.rect.Y+bottom.Y-.1 {
				return false
			}
			row++
		}
	default:
		return false
	}
	if column >= len(t.rows[row]) {
		return false
	}
	target := t.rows[row][column]
	at, affinity := target.start, ui.TextDownstream
	if end {
		at, affinity = target.end, ui.TextUpstream
	}
	if key == ui.KeyUp || key == ui.KeyDown {
		if run := s.tableCellRun(target, end); run != nil {
			unit := 0
			if end {
				unit = run.units[len(run.units)-1]
			}
			line := run.layout.CaretAt(ui.TextCaretPosition{Index: unit, Affinity: affinity})
			position := run.layout.PositionAt(ui.Point{X: s.rect.X + s.desiredX - run.rect.X, Y: line.Y + line.H/2})
			start, finish := run.sourceEdgesForUnit(position.Index)
			at, affinity = s.visualSelectionEdge(start, finish, position.Affinity, false), position.Affinity
			at = max(target.start, min(at, target.end))
		}
	} else {
		s.hasDesired = false
	}
	// Missing cells remain source-only until an explicit edit; arrows do not
	// call enterTableCell or create row/column bytes as a side effect.
	s.moveTo(at, affinity, false)
	if target.omitted {
		s.omittedTableCell = &editorTableHit{table: t, cell: target}
	}
	return true
}
