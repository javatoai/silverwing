package markdown

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestInPlaceTableStructureRetainsSingleHistory(t *testing.T) {
	source := "before\r\n\r\n| A | B |\r\n|---|---|\r\n| 甲 | 乙 |\r\n\r\nafter"
	normalized := strings.ReplaceAll(source, "\r\n", "\n")
	state, tester := liveTester(source, strings.Index(normalized, "甲"), EditorOptions{InPlaceTables: true})
	table, cell, ok := state.editingTable()
	if !ok {
		t.Fatal("missing cell")
	}
	if !state.tableStructure(table, "column-after", cell) {
		t.Fatal("insert column failed")
	}
	tester.Frame()
	if !strings.Contains(state.Buffer().OriginalText(), "| A |  | B |\r\n") || !strings.HasSuffix(state.Buffer().OriginalText(), "\r\n\r\nafter") {
		t.Fatalf("structure affected outside table: %q", state.Buffer().OriginalText())
	}
	tester.Command("undo")
	if state.Buffer().OriginalText() != source {
		t.Fatal("structure undo lost original bytes")
	}
	tester.Key(0, ui.KeyTab)
	table, cell, ok = state.editingTable()
	if !ok || cell.column != 1 {
		t.Fatal("tab lost cell")
	}
	if !state.tableStructure(table, "align-right", cell) {
		t.Fatal("alignment failed")
	}
	tester.Frame()
	if !strings.Contains(state.Buffer().OriginalText(), "---:") {
		t.Fatal("alignment delimiter not updated")
	}
}

func TestInPlaceTableMatrixPasteIsOneUndoAndCopiesCells(t *testing.T) {
	source := "before\n\n| A | B |\n|---|---|\n| 甲 | 乙 |\n\nafter"
	state, tester := liveTester(source, strings.Index(source, "甲"), EditorOptions{InPlaceTables: true})
	if !state.tablePaste("x\ty\nz\tw") {
		t.Fatal("matrix not handled")
	}
	tester.Frame()
	if !strings.Contains(state.Buffer().OriginalText(), "| x | y |\n| z | w |") || !strings.HasSuffix(state.Buffer().OriginalText(), "\n\nafter") {
		t.Fatalf("matrix %q", state.Buffer().OriginalText())
	}
	table, _, ok := state.editingTable()
	if !ok {
		t.Fatal("missing pasted cells")
	}
	a, z := table.rows[1][0].start, table.rows[2][1].end
	if text, ok := state.tableCopy(a, z); !ok || text != "x\ty\nz\tw" {
		t.Fatalf("matrix copy %q %v", text, ok)
	}
	tester.Command("undo")
	if state.Buffer().OriginalText() != source {
		t.Fatal("matrix paste was not atomic")
	}
}
