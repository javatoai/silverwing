package desktop

import (
	"math"

	"silverwing-go/internal/editor"

	"github.com/egoist/mygo/ui"
)

func markdownTableCellSelected(doc *editor.Document, partID uint64, row, column int) bool {
	if doc == nil {
		return false
	}
	selection := doc.Snapshot().TableSelection
	return selection != nil && selection.PartID == partID && row >= selection.FirstRow && row <= selection.LastRow &&
		column >= selection.FirstColumn && column <= selection.LastColumn
}

func markdownTableCellAt(bounds map[markdownTableCellKey]ui.Rect, partID uint64, x, y float32) (markdownTableCellKey, bool) {
	for key, rect := range bounds {
		if key.partID != partID || rect.W <= 0 || rect.H <= 0 {
			continue
		}
		if x >= rect.X && x < rect.X+rect.W && y >= rect.Y && y < rect.Y+rect.H {
			return key, true
		}
	}
	return markdownTableCellKey{}, false
}

func refreshMarkdownTableCellBounds(c *ui.Context, state *markdownEditorState, partID uint64) {
	if state == nil {
		return
	}
	if state.tableCellBounds == nil {
		state.tableCellBounds = make(map[markdownTableCellKey]ui.Rect)
	}
	for _, field := range state.editFields {
		if !field.tableCell || field.partID != partID {
			continue
		}
		if field.rich != nil && field.rich.input != nil {
			state.tableCellBounds[markdownTableCellKey{partID: partID, row: field.row, column: field.column}] = field.rich.input.handle.Bounds(c)
		}
	}
}

func selectMarkdownTableRectangle(doc *editor.Document, partID uint64, anchorRow, anchorColumn, endRow, endColumn int) bool {
	return doc != nil && doc.SelectTableCells(partID, anchorRow, endRow, anchorColumn, endColumn) == nil
}

func (s *app) handleMarkdownTableCellInput(c *ui.Context, state *markdownEditorState, partID uint64, row, column int, field ui.Handle, event ui.InputEvent) bool {
	if state == nil || state.doc == nil || state.readOnly || state.detached || state.doc.Composing() {
		return false
	}
	switch event.Kind {
	case ui.InputPointerDown:
		if event.Button != 0 {
			return false
		}
		bounds := field.Bounds(c)
		if bounds.W <= 0 || bounds.H <= 0 {
			return false
		}
		refreshMarkdownTableCellBounds(c, state, partID)
		state.doc.ClearSelection()
		state.tableDrag = &markdownTableDragState{
			partID: partID, anchorRow: row, anchorCol: column,
			startX: bounds.X + event.X, startY: bounds.Y + event.Y,
		}
		return false // Keep the native text input's click-to-edit and caret behavior.
	case ui.InputPointerMove:
		drag := state.tableDrag
		if drag == nil || drag.partID != partID || event.Button != 0 {
			return false
		}
		bounds := field.Bounds(c)
		x, y := bounds.X+event.X, bounds.Y+event.Y
		if !drag.dragging && math.Abs(float64(x-drag.startX))+math.Abs(float64(y-drag.startY)) < 4 {
			return false
		}
		drag.dragging = true
		refreshMarkdownTableCellBounds(c, state, partID)
		target, ok := markdownTableCellAt(state.tableCellBounds, partID, x, y)
		if !ok || drag.hasLastCell && target.row == drag.lastRow && target.column == drag.lastColumn {
			return false
		}
		if selectMarkdownTableRectangle(state.doc, partID, drag.anchorRow, drag.anchorCol, target.row, target.column) {
			drag.lastRow, drag.lastColumn, drag.hasLastCell = target.row, target.column, true
		}
	case ui.InputPointerUp:
		if event.Button == 0 && state.tableDrag != nil && state.tableDrag.partID == partID {
			state.tableDrag = nil
		}
	}
	return false
}

func copyMarkdownTableSelection(doc *editor.Document, write func(string)) bool {
	if doc == nil || write == nil {
		return false
	}
	markdown, ok := doc.CopyTableSelection()
	if !ok {
		return false
	}
	write(markdown)
	return true
}

func copyReadonlyMarkdownTableSelection(state *markdownEditorState, partID uint64, write func(string)) bool {
	if state == nil || state.doc == nil || state.closed || !state.readOnly {
		return false
	}
	selection := state.doc.Snapshot().TableSelection
	return selection != nil && selection.PartID == partID && copyMarkdownTableSelection(state.doc, write)
}

// The read-only grid owns a rectangle instead of native editable text fields.
// Handles resolve committed bounds when input arrives, after the build ends.
func (s *app) handleReadonlyMarkdownTableInput(c *ui.Context, state *markdownEditorState, partID uint64, grid ui.Handle, cells map[markdownTableCellKey]*ui.Handle, event ui.InputEvent) bool {
	if state == nil || state.doc == nil || state.closed || !state.readOnly {
		return false
	}
	if event.Kind == ui.InputCommand && event.Text == "copy" {
		return copyReadonlyMarkdownTableSelection(state, partID, s.clipboardTextWriter(c.Services()))
	}
	if event.Kind == ui.InputKeyDown {
		if event.Key == ui.KeyC && event.Mods&(ui.Ctrl|ui.Super) != 0 {
			return copyReadonlyMarkdownTableSelection(state, partID, s.clipboardTextWriter(c.Services()))
		}
		if event.Key == ui.KeyEscape {
			if selection := state.doc.Snapshot().TableSelection; selection != nil && selection.PartID == partID {
				state.doc.ClearSelection()
				return true
			}
		}
		return false
	}
	if event.Button != 0 {
		return false
	}
	for key, handle := range cells {
		state.tableCellBounds[key] = handle.Bounds(c)
	}
	bounds := grid.Bounds(c)
	cell, hit := markdownTableCellAt(state.tableCellBounds, partID, bounds.X+event.X, bounds.Y+event.Y)
	switch event.Kind {
	case ui.InputPointerDown:
		if !hit {
			return false
		}
		grid.Focus(c)
		state.tableDrag = &markdownTableDragState{partID: partID, anchorRow: cell.row, anchorCol: cell.column}
		return selectMarkdownTableRectangle(state.doc, partID, cell.row, cell.column, cell.row, cell.column)
	case ui.InputPointerMove, ui.InputPointerUp:
		drag := state.tableDrag
		if drag == nil || drag.partID != partID {
			return false
		}
		if hit {
			selectMarkdownTableRectangle(state.doc, partID, drag.anchorRow, drag.anchorCol, cell.row, cell.column)
		}
		if event.Kind == ui.InputPointerUp {
			state.tableDrag = nil
		}
		return true
	}
	return false
}
