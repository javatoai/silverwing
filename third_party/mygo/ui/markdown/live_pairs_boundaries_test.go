package markdown

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
	"strings"
	"testing"
)

func TestDailyPairsSkipOnlyGeneratedClosersDeleteAndUndo(t *testing.T) {
	s, v := liveTester("", 0, dailyOptions())
	v.Type("(")
	if s.source != "()" || s.caret != 1 {
		t.Fatalf("pair failed: %q %d", s.source, s.caret)
	}
	v.Key(0, ui.KeyBackspace)
	if s.source != "" {
		t.Fatal("empty pair not removed")
	}
	v.Command("undo")
	version := s.Buffer().Version()
	v.Type(")")
	if s.source != "()" || s.caret != 2 || s.Buffer().Version() != version {
		t.Fatalf("undo lost generated closer: %q %d", s.source, s.caret)
	}
	s, v = liveTester("word", 4, dailyOptions())
	v.Type("'")
	if s.source != "word'" {
		t.Fatal("English apostrophe paired")
	}
	s, v = liveTester("", 0, dailyOptions())
	v.Compose("候选😀", 4)
	v.Type("提交(")
	if s.source != "提交(" {
		t.Fatal("composition was paired")
	}
	s, v = liveTester("中文😀", 0, dailyOptions())
	s.SetSelection(0, len(s.source))
	v.Frame()
	v.Type("[")
	if s.source != "[中文😀]" {
		t.Fatal("selection wrapping lost text")
	}
	v.Command("undo")
	if s.source != "中文😀" {
		t.Fatal("wrap was not one undo")
	}
}

func TestDailyCodeIndentMovesActualTextCaretAndUndo(t *testing.T) {
	const source = "```sql\nSELECT 1;\nSELECT 2;\n```"
	s, v := liveTester(source, 0, dailyOptions())
	first := strings.Index(source, "SELECT")
	s.SetSelection(first, first)
	v.Frame()
	before, ok := s.ownedCaretBounds(first)
	if !ok {
		t.Fatal("missing original caret")
	}
	s.SetSelection(first, strings.LastIndex(source, ";")+1)
	v.Frame()
	v.Key(0, ui.KeyTab)
	at := strings.Index(s.source, "SELECT")
	s.SetSelection(at, at)
	v.Frame()
	after, ok := s.ownedCaretBounds(at)
	if !ok || after.X-before.X < 15 {
		t.Fatalf("indent has no visible advance: before=%+v after=%+v", before, after)
	}
	v.Command("undo")
	if s.Buffer().OriginalText() != source {
		t.Fatal("indent undo changed code bytes")
	}
}

func TestDailyTableTailIsWritableOutsideTableAndEmptyTableCanExit(t *testing.T) {
	const source = "| A | B |\n|---|---|\n| x | y |"
	s, v := liveTester(source, len(source), dailyOptions())
	version := s.Buffer().Version()
	if s.focusedCodeGap() == nil || s.Buffer().Version() != version {
		t.Fatal("no passive table writing row")
	}
	v.Type("after table")
	if s.source != source+"\n\nafter table" {
		t.Fatalf("tail text entered table: %q", s.source)
	}
	d := parser.Parse(s.source)
	if d.Block(d.Len()-1).Kind != parser.Paragraph {
		t.Fatal("tail still parsed as table")
	}
	v.Command("undo")
	if s.source != source {
		t.Fatal("table tail not one undo")
	}
	s, v = liveTester("|  |  |\n|---|---|\n|  |  |", 2, dailyOptions())
	t1, cell, ok := s.editingTable()
	if !ok {
		t.Fatal("missing empty table")
	}
	s.SetSelection(cell.start, cell.start)
	v.Frame()
	v.Key(0, ui.KeyBackspace)
	if strings.Contains(s.source, "|") {
		t.Fatalf("empty table did not exit: %q (%+v)", s.source, t1)
	}
}
