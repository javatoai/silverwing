package desktop

import (
	"fmt"
	"strings"

	"silverwing-go/internal/editor"

	"github.com/egoist/mygo/ui"
)

func (s *app) renderReadonlyMarkdownOriginal(c *ui.Context, p palette, state *markdownEditorState, part editor.Part) {
	source := markdownReadonlyOriginalSource(state.doc.Source(), part)
	key := markdownOriginalFindKey(part.ID)
	find := s.markdownFindStateFor(state)
	renderReadonlyTextEditor(c.Key(markdownEditorControlKey(state, fmt.Sprintf("markdown-readonly-original-%d", part.ID))), p,
		markdownEditorHostID(state)+"-"+key, "", source, "源码只读预览", state.zoom, find, key)
}

func markdownReadonlyOriginalSource(source string, part editor.Part) string {
	// Goldmark's indented-code position starts after the first indent. Those
	// bytes belong to an empty RICH gap; display them with their original line.
	if part.Range.Start > 0 && part.Range.Start <= part.Range.End && part.Range.End <= len(source) {
		start := strings.LastIndexAny(source[:part.Range.Start], "\r\n") + 1
		if start < part.Range.Start && strings.Trim(source[start:part.Range.Start], " \t") == "" {
			return source[start:part.Range.End]
		}
	}
	return part.Source
}

func markdownOriginalFindKey(partID uint64) string {
	return fmt.Sprintf("markdown-original:%d", partID)
}

func (s *app) renderReadonlyMarkdownTable(c *ui.Context, p palette, state *markdownEditorState, part editor.Part) {
	if len(part.Nodes) == 0 || len(part.Nodes[0].Children) == 0 {
		return
	}
	if state.tableCellBounds == nil {
		state.tableCellBounds = make(map[markdownTableCellKey]ui.Rect)
	}
	find := s.markdownFindStateFor(state)
	ui.ScrollHorizontal(c.Key(markdownEditorControlKey(state, fmt.Sprintf("markdown-readonly-table-scroll-%d", part.ID)))).FillWidth().Children(func() {
		root := ui.Column(c.Key(markdownEditorControlKey(state, fmt.Sprintf("markdown-readonly-table-%d", part.ID)))).Focusable().Unselectable()
		grid := ui.Local(root, "focus", func() ui.Handle { return ui.Handle{} })
		cells := ui.Local(root, "cells", func() map[markdownTableCellKey]*ui.Handle { return make(map[markdownTableCellKey]*ui.Handle) })
		copySelection := func() { copyReadonlyMarkdownTableSelection(state, part.ID, s.clipboardTextWriter(c.Services())) }
		root.Bind(grid).HandleInput(func(event ui.InputEvent) bool {
			return s.handleReadonlyMarkdownTableInput(c, state, part.ID, *grid, *cells, event)
		}).OnShortcut(ui.Ctrl, ui.KeyC, copySelection).OnShortcut(ui.Super, ui.KeyC, copySelection).ContextMenu(func(menu *ui.Menu) {
			selection := state.doc.Snapshot().TableSelection
			if menu.Item("复制").Disabled(selection == nil || selection.PartID != part.ID).Chosen() {
				copySelection()
			}
		}).Children(func() {
			for rowIndex, row := range part.Nodes[0].Children {
				ui.Row(c.Key(fmt.Sprintf("row-%d", rowIndex))).Children(func() {
					for column, node := range row.Children {
						key := markdownTableCellKey{partID: part.ID, row: rowIndex, column: column}
						if (*cells)[key] == nil {
							(*cells)[key] = &ui.Handle{}
						}
						cell := ui.Column(c.Key(fmt.Sprintf("cell-%d", column))).Bind((*cells)[key]).Width(200).MinHeight(36).Padding(8).Border(1, p.line)
						if markdownTableCellSelected(state.doc, part.ID, rowIndex, column) {
							cell.Background(p.selected)
						} else if rowIndex == 0 {
							cell.Background(p.soft)
						}
						findKey := markdownTableFindKey(part.ID, rowIndex, column)
						cell.Children(func() {
							cellPart := part
							cellPart.Source, cellPart.Range = node.Text, node.Range
							s.renderMarkdownInlineNodes(c, p, state, cellPart, node.Children, scaleMarkdownSize(15, state.zoom), 400, false, false, false, findKey, new(int), p.text, false)
						})
						if find != nil && find.shouldReveal(findKey) {
							cell.ScrollIntoView()
						}
					}
				})
			}
		})
	})
}
