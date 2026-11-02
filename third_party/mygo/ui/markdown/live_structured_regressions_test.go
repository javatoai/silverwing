package markdown

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func structuredTables(s *EditorState) []editorTable {
	s.sync()
	d := parser.Parse(s.source)
	var tables []editorTable
	for i := 1; i < d.Len(); i++ {
		if d.Block(i).Kind == parser.Table {
			tables = append(tables, s.readTable(d, i))
		}
	}
	return tables
}

func structuredCellText(s *EditorState, table, row, column int) string {
	tables := structuredTables(s)
	cell := tables[table].rows[row][column]
	return s.source[cell.start:cell.end]
}

func TestStructuredRenderedLinkDragKeepsRootInput(t *testing.T) {
	for _, source := range []string{
		"Prefix [**LinkText**](https://example.com) suffix",
		"> Prefix [**LinkText**](https://example.com) suffix",
		"- Prefix [**LinkText**](https://example.com) suffix",
		"| A | B |\n|---|---|\n| [**LinkText**](https://example.com) | suffix |",
	} {
		t.Run(source, func(t *testing.T) {
			opened := 0
			s, view := liveTester(source, 0, EditorOptions{RenderedLinks: true, InPlaceTables: true, Options: &Options{OnLink: func(string) { opened++ }}})
			buffer := s.Buffer()
			box, ok := view.Find("LinkText")
			if !ok {
				t.Fatal("missing rendered label")
			}
			x, y := box.X+box.W*.15, box.Y+box.H/2
			view.Press(x, y)
			view.Move(box.X+box.W*.85, y)
			view.Release(box.X+box.W*.85, y)
			a, z := s.Selection()
			if a == z || opened != 0 || s.dragging || s.linkPress || s.Buffer() != buffer {
				t.Fatalf("drag lost root ownership: selection=%d:%d opened=%d", a, z, opened)
			}
			if active, _ := view.TextInputActive(); !active {
				t.Fatal("drag left the root input-method client")
			}
			view.Command("copy")
			if got := view.Clipboard(); got != buffer.Snapshot().OriginalSlice(min(a, z), max(a, z)) {
				t.Fatalf("drag copy = %q selection=%d:%d", got, a, z)
			}
			if err := view.ClickWith(ui.Ctrl, "LinkText"); err != nil {
				t.Fatal(err)
			}
			if opened != 1 || buffer.OriginalText() != source {
				t.Fatalf("no-movement click failed after drag: opened=%d source=%q", opened, buffer.OriginalText())
			}
		})
	}
}

func TestStructuredLinkControlDragNeverActivates(t *testing.T) {
	for _, mods := range []ui.Modifiers{ui.Ctrl, ui.Super} {
		opened := 0
		s, view := liveTester("A [LinkText](https://example.com) Z", 0, EditorOptions{RenderedLinks: true, Options: &Options{OnLink: func(string) { opened++ }}})
		box, _ := view.Find("LinkText")
		x, y := box.X+box.W*.2-s.rect.X, box.Y+box.H/2-s.rect.Y
		if !s.input(ui.InputEvent{Kind: ui.InputPointerDown, X: x, Y: y, Button: 0, Mods: mods}) {
			t.Fatal("modified press did not capture subsequent native moves/releases")
		}
		s.input(ui.InputEvent{Kind: ui.InputPointerMove, X: x + box.W*.6, Y: y, Button: 0, Mods: mods})
		s.input(ui.InputEvent{Kind: ui.InputPointerUp, X: x + box.W*.6, Y: y, Button: 0, Mods: mods})
		view.Frame()
		a, z := s.Selection()
		if a == z || opened != 0 {
			t.Fatalf("modified drag activated: %d:%d opened=%d", a, z, opened)
		}
	}
}

func TestStructuredReadOnlyRenderedLinkStillOpens(t *testing.T) {
	for _, custom := range []bool{false, true} {
		opened := 0
		opts := EditorOptions{ReadOnly: true, RenderedLinks: true}
		if custom {
			opts.Options = &Options{OnLink: func(string) { opened++ }}
		}
		s, view := liveTester("[LinkText](https://example.com)", 0, opts)
		if err := view.Click("LinkText"); err != nil {
			t.Fatal(err)
		}
		if custom && opened != 1 || !custom && len(view.OpenedURLs()) != 1 || s.Buffer().String() != "[LinkText](https://example.com)" {
			t.Fatalf("read-only click custom=%v callbacks=%d urls=%v", custom, opened, view.OpenedURLs())
		}
	}
}

func TestStructuredOmittedCellsUseClickedColumnAndTab(t *testing.T) {
	for _, tail := range []string{"| x |", "| x"} {
		source := "before\r\n\r\n| A | B | C |\r\n|---|---|---|\r\n" + tail
		s, view := liveTester(source, len(normalizedText(source)), EditorOptions{InPlaceTables: true})
		var box ui.Rect
		for _, hit := range s.tableCells {
			if hit.cell.row == 1 && hit.cell.column == 2 {
				box = hit.rect
			}
		}
		if box.W == 0 {
			t.Fatal("missing virtual third cell geometry")
		}
		view.ClickAt(box.X+box.W/2, box.Y+box.H/2)
		view.Type("chosen")
		if structuredCellText(s, 0, 1, 0) != "x" || structuredCellText(s, 0, 1, 1) != "" || structuredCellText(s, 0, 1, 2) != "chosen" {
			t.Fatalf("clicked omitted column lost its identity: %q", s.Buffer().OriginalText())
		}
		view.Command("undo")
		if s.Buffer().OriginalText() != source {
			t.Fatalf("materialization undo lost original CRLF: %q", s.Buffer().OriginalText())
		}
		normalized := s.source
		at := strings.LastIndex(normalized, "x") + 1
		s.SetSelection(at, at)
		view.Frame()
		view.Key(0, ui.KeyTab)
		view.Type("second")
		view.Key(0, ui.KeyTab)
		view.Type("third")
		if structuredCellText(s, 0, 1, 1) != "second" || structuredCellText(s, 0, 1, 2) != "third" {
			t.Fatalf("Tab reused an omitted column offset: %q", s.source)
		}
		view.Key(ui.Shift, ui.KeyTab)
		_, cell, ok := s.editingTable()
		if !ok || cell.column != 1 {
			t.Fatal("ShiftTab did not return to the preceding materialized cell")
		}
	}
}

func TestStructuredTableWritesPreserveContainerPrefixes(t *testing.T) {
	for _, prefix := range []struct{ first, continuation string }{{"- ", "  "}, {"12. ", "    "}, {"> - ", ">   "}, {"- > ", "  > "}} {
		for _, action := range []string{"row-after", "column-after", "align-center"} {
			source := "before\r\n\r\n" + prefix.first + "| A | B |\r\n" + prefix.continuation + "|---|---|\r\n" + prefix.continuation + "| x | y |\r\n\r\nafter"
			s, view := liveTester(source, strings.Index(normalizedText(source), "x"), EditorOptions{InPlaceTables: true})
			table, cell, ok := s.editingTable()
			if !ok {
				t.Fatalf("fixture did not parse inside %q", prefix.first)
			}
			before := parser.Parse(s.source)
			var ancestors []parser.Kind
			for i := before.Block(table.block).Parent; i > 0; i = before.Block(i).Parent {
				ancestors = append(ancestors, before.Block(i).Kind)
			}
			if !s.tableStructure(table, action, cell) {
				t.Fatalf("%s failed in %q", action, prefix.first)
			}
			view.Frame()
			tables := structuredTables(s)
			if len(tables) != 1 || !strings.HasPrefix(s.Buffer().OriginalText(), "before\r\n\r\n"+prefix.first+"| A") || !strings.HasSuffix(s.Buffer().OriginalText(), "\r\n\r\nafter") {
				t.Fatalf("structure moved outside container: %q", s.Buffer().OriginalText())
			}
			after := parser.Parse(s.source)
			i := after.Block(tables[0].block).Parent
			for _, kind := range ancestors {
				if i <= 0 || after.Block(i).Kind != kind {
					t.Fatalf("container ancestry changed after %s: %q", action, s.source)
				}
				i = after.Block(i).Parent
			}
			if i != 0 {
				t.Fatal("new container introduced by repeated first-line markers")
			}
			view.Command("undo")
			if s.Buffer().OriginalText() != source {
				t.Fatal("container operation undo changed outside bytes")
			}
		}
	}
}

func TestStructuredTableInputUsesSurroundingPipeParity(t *testing.T) {
	for _, method := range []string{"typing", "paste", "composition"} {
		for slashes := 0; slashes <= 3; slashes++ {
			source := "| A | B |\n|---|---|\n| x" + strings.Repeat(`\`, slashes) + " | y |"
			at := strings.Index(source, "x") + 1 + slashes
			s, view := liveTester(source, at, EditorOptions{InPlaceTables: true})
			switch method {
			case "typing":
				view.Type("|")
			case "paste":
				view.SetClipboard("|")
				view.Command("paste")
			case "composition":
				view.Compose("|", 1)
				view.Type("|")
			}
			tables := structuredTables(s)
			if len(tables) != 1 || len(tables[0].align) != 2 || structuredCellText(s, 0, 1, 1) != "y" {
				t.Fatalf("%s after %d slashes split a cell: %q", method, slashes, s.source)
			}
		}
	}
	for _, fixture := range []struct{ source, insertion string }{
		{"| A | B |\n|---|---|\n| x | y |", `\`},
		{"| A | B |\n|---|---|\n| x\\|tail | y |", `\`},
		{"|A|B|\n|-|-|\n|x|y|", `\`},
	} {
		s, view := liveTester(fixture.source, strings.Index(fixture.source, "x")+1, EditorOptions{InPlaceTables: true})
		view.Type(fixture.insertion)
		view.Type("|")
		if tables := structuredTables(s); len(tables) != 1 || structuredCellText(s, 0, 1, 1) != "y" {
			t.Fatalf("separate backslash/pipe or suffix parity broke table: %q", s.source)
		}
	}
}

func TestStructuredTSVPasteHonorsWholeAndCrossBlockSelections(t *testing.T) {
	const table = "| A | B |\n|---|---|\n| x | y"
	for _, fixture := range []struct {
		source string
		a, z   int
	}{
		{table, 0, len(table)},
		{"before\n\n" + table + "\n\nafter", 0, len("before\n\n") + strings.Index(table, "x") + 1},
		{"before\n\n" + table + "\n\nafter", len("before\n\n" + table + "\n\nafter"), len("before\n\n") + strings.Index(table, "x")},
	} {
		s, view := liveTester(fixture.source, fixture.z, EditorOptions{InPlaceTables: true})
		s.SetSelection(fixture.a, fixture.z)
		view.Frame()
		view.SetClipboard("P\tQ")
		view.Command("paste")
		a, z := min(fixture.a, fixture.z), max(fixture.a, fixture.z)
		want := fixture.source[:a] + "P\tQ" + fixture.source[z:]
		if s.Buffer().OriginalText() != want {
			t.Fatalf("selection was ignored for TSV paste: %q want %q", s.Buffer().OriginalText(), want)
		}
		view.Command("undo")
		if s.Buffer().OriginalText() != fixture.source {
			t.Fatal("ordinary TSV replacement did not undo atomically")
		}
	}
}

func TestStructuredTableContextMenuUsesClickedTableAndColumn(t *testing.T) {
	const first = "| A | B |\n|---|---|\n| x | y |\n\n"
	const second = "| C | D |\n|---|---|\n| q | z |"
	for _, label := range []string{"左侧插入列", "右侧插入列"} {
		s, view := liveTester(first+second, strings.Index(first, "x"), EditorOptions{InPlaceTables: true})
		if err := view.RightClick("z"); err != nil {
			t.Fatal(err)
		}
		if err := view.ChooseMenuItem(label); err != nil {
			t.Fatal(err)
		}
		tables := structuredTables(s)
		if !strings.HasPrefix(s.source, first) || len(tables) != 2 || len(tables[0].align) != 2 || len(tables[1].align) != 3 {
			t.Fatalf("right click changed the old caret table: %q", s.source)
		}
		column := 1
		if label == "左侧插入列" {
			column = 2
		}
		if structuredCellText(s, 1, 1, column) != "z" {
			t.Fatalf("right-click action used the wrong column: %q", s.source)
		}
	}
}

func TestStructuredCompositionAcrossCellsAndTablesIsSingleProjection(t *testing.T) {
	for _, source := range []string{
		"before\r\n\r\n| A | B |\r\n|---|---|\r\n| x | y |\r\n\r\nafter",
		"- | A | B |\r\n  |---|---|\r\n  | x | y |",
		"| A | B |\r\n|---|---|\r\n| x | first |\r\n\r\nmiddle\r\n\r\n| C | D |\r\n|---|---|\r\n| last | y |",
	} {
		normalized := normalizedText(source)
		a, z := strings.Index(normalized, "x"), strings.LastIndex(normalized, "y")+1
		s, view := liveTester(source, a, EditorOptions{InPlaceTables: true})
		buffer := s.Buffer()
		s.SetSelection(a, z)
		view.Frame()
		view.Compose("预编辑😀", 4)
		var rendered strings.Builder
		for _, run := range s.runs {
			rendered.WriteString(run.text)
		}
		if strings.Count(rendered.String(), "预编辑😀") != 1 || s.virtualText() != normalized[:a]+"预编辑😀"+normalized[z:] || buffer.OriginalText() != source {
			t.Fatalf("composition projection duplicated/lost content: %q virtual=%q", rendered.String(), s.virtualText())
		}
		if caret, active := view.TextCaret(); !active || caret.H <= 0 {
			t.Fatal("cross-structure composition lost the native candidate caret")
		}
		view.Type("提交")
		if s.source != normalized[:a]+"提交"+normalized[z:] || s.Buffer() != buffer {
			t.Fatalf("cross-structure commit did not use the original Buffer: %q", s.source)
		}
		view.Command("undo")
		if buffer.OriginalText() != source {
			t.Fatal("cross-structure IME undo did not restore original bytes")
		}
	}
}

func TestStructuredLinkDeletionPreservesLabelOrDeletesWholeLink(t *testing.T) {
	const source = "A [**甲😀乙**](https://example.com) B\r\n"
	normalized := normalizedText(source)
	textStart := strings.Index(normalized, "甲")
	textEnd := strings.Index(normalized, "乙") + len("乙")
	for _, fixture := range []struct {
		at   int
		key  ui.Key
		want string
	}{
		{strings.Index(normalized, "😀") + len("😀"), ui.KeyBackspace, "A [**甲乙**](https://example.com) B\r\n"},
		{strings.Index(normalized, "😀"), ui.KeyDelete, "A [**甲乙**](https://example.com) B\r\n"},
		{textStart, ui.KeyBackspace, "A  B\r\n"},
		{textEnd, ui.KeyDelete, "A  B\r\n"},
		{strings.Index(normalized, "["), ui.KeyDelete, "A  B\r\n"},
		{strings.Index(normalized, ")") + 1, ui.KeyBackspace, "A  B\r\n"},
	} {
		s, view := liveTester(source, fixture.at, EditorOptions{RenderedLinks: true})
		view.Key(0, fixture.key)
		if got := s.Buffer().OriginalText(); got != fixture.want {
			t.Fatalf("key %v at %d damaged hidden link syntax: %q want %q", fixture.key, fixture.at, got, fixture.want)
		}
		view.Command("undo")
		if s.Buffer().OriginalText() != source {
			t.Fatal("link deletion undo did not restore exact source")
		}
	}
}
