package markdown

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func dailyOptions() EditorOptions {
	return EditorOptions{InPlaceCode: true, InPlaceTables: true, RenderedLinks: true, ContinueBlocks: true, SmartEditing: true, ConfirmCodeFenceOnEnter: true}
}

func TestDailyFenceWaitsForEnterAndCreatesRepeatedIndependentCards(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n"} {
		source := "\ufeff# preserved" + ending + ending + "<!-- untouched -->" + ending + ending
		s, v := liveTester(source, len(normalizedText(source)), dailyOptions())
		buffer := s.Buffer()
		for repeat := 0; repeat < 3; repeat++ {
			v.Key(ui.Ctrl, ui.KeyEnd)
			for _, ch := range []string{"`", "`", "`", "s", "q", "l"} {
				v.Type(ch)
			}
			draft := buffer.OriginalText()
			if !v.HasText("```sql") || !strings.HasSuffix(s.source, "```sql") {
				t.Fatalf("typed info became code before Enter: %q / %q", s.source, v.Texts())
			}
			v.Key(0, ui.KeyEnter)
			a, z := s.Selection()
			if a != z || !strings.HasSuffix(buffer.OriginalText(), "```sql"+ending+ending+"```") {
				t.Fatalf("Enter did not complete an empty card: %q (%d:%d)", buffer.OriginalText(), a, z)
			}
			code, ok := s.editingCode(a, z)
			if !ok || !code.closed || a != code.bodyStart {
				t.Fatalf("new card caret not in body: %+v %d", code, a)
			}
			v.Command("undo")
			if buffer.OriginalText() != draft || !v.HasText("```sql") {
				t.Fatal("one undo failed to restore literal draft")
			}
			v.Command("redo")
			v.Type("SELECT 1; -- 中文😀")
		}
		d := parser.Parse(s.source)
		count := 0
		for i := 1; i < d.Len(); i++ {
			if d.Block(i).Kind == parser.CodeBlock {
				syntax, _ := d.CodeSource(i)
				if syntax.ClosingStart < 0 {
					t.Fatal("new unclosed block")
				}
				count++
			}
		}
		if count != 3 || buffer != s.Buffer() || !strings.HasPrefix(buffer.OriginalText(), source) {
			t.Fatalf("repeat creation damaged document: %d %q", count, buffer.OriginalText())
		}
	}
}

func TestDailyFenceDraftDoesNotConsumeLaterBlockAndRejectedEnterIsAtomic(t *testing.T) {
	const source = "before\n\n\n```python\nprint(2)\n```\n\nafter"
	s, v := liveTester(source, len("before\n\n"), dailyOptions())
	v.Type("```")
	if !v.HasText("print(2)") || !v.HasText("after") || !v.HasText("```") {
		t.Fatalf("draft consumed later blocks: %q", v.Texts())
	}
	before := s.Buffer().OriginalText()
	version := s.Buffer().Version()
	s.Buffer().SetMaxSourceBytes(len(before))
	v.Key(0, ui.KeyEnter)
	if s.Buffer().OriginalText() != before || s.Buffer().Version() != version || !v.HasText("```") {
		t.Fatal("rejected creation changed source or draft")
	}
	s.Buffer().SetMaxSourceBytes(-1)
	v.Key(0, ui.KeyEnter)
	if !v.HasText("print(2)") || !v.HasText("after") {
		t.Fatal("creation swallowed following content")
	}
}

func TestDailyCodeMultiLineIndentOutdentAndEmptyExitPreserveSource(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n"} {
		source := "before" + ending + ending + "```sql metadata=kept" + ending + "SELECT 中文😀;" + ending + "  SELECT 2;" + ending + "```" + ending + ending + "after"
		s, v := liveTester(source, 0, dailyOptions())
		a, z := strings.Index(s.source, "SELECT"), strings.LastIndex(s.source, ";")+1
		s.SetSelection(z, a)
		v.Frame()
		v.Key(0, ui.KeyTab)
		want := strings.Replace(strings.Replace(source, "SELECT 中文😀;", "    SELECT 中文😀;", 1), "  SELECT 2;", "      SELECT 2;", 1)
		if s.Buffer().OriginalText() != want {
			t.Fatalf("Tab replaced content: %q", s.Buffer().OriginalText())
		}
		v.Key(ui.Shift, ui.KeyTab)
		if s.Buffer().OriginalText() != source {
			t.Fatal("ShiftTab did not remove only the added indentation")
		}
		v.Command("undo")
		if s.Buffer().OriginalText() != want {
			t.Fatal("indent history not atomic")
		}
		v.Command("undo")
		if s.Buffer().OriginalText() != source {
			t.Fatal("original bytes not restored")
		}
	}
	s, v := liveTester("before\n\n```sql\n\n```\n\nafter", len("before\n\n```sql\n"), dailyOptions())
	original := s.Buffer().OriginalText()
	v.Key(0, ui.KeyBackspace)
	if strings.Contains(s.source, "```") || !strings.Contains(s.source, "before") || !strings.Contains(s.source, "after") {
		t.Fatal("empty card did not exit safely")
	}
	v.Command("undo")
	if s.Buffer().OriginalText() != original {
		t.Fatal("empty card exit not one undo")
	}
}
