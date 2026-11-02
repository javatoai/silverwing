package markdown

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func TestWritingExclusiveSelectionBeforeLiteralMovesSameItemBothDirections(t *testing.T) {
	for _, fixture := range []struct {
		name, literal, separator string
		kind                     parser.Kind
	}{
		{"code", "```sql\nx\n```", "", parser.CodeBlock},
		{"html", "<div>\nraw\n</div>", "", parser.HTMLBlock},
		{"table", "A | B\n---|---\nx | y", "\n", parser.Table},
	} {
		for _, reverse := range []bool{false, true} {
			name := fixture.name + "/forward"
			if reverse {
				name = fixture.name + "/reverse"
			}
			t.Run(name, func(t *testing.T) {
				source := "- parent\n- move\n" + fixture.separator + fixture.literal
				want := "- parent\n  - move\n" + fixture.separator + fixture.literal
				a, z := 9, 16+len(fixture.separator) // code/html: exactly "- move\n"
				d := parser.Parse(source)
				literal := false
				for i := 1; i < d.Len(); i++ {
					if b := d.Block(i); b.Kind == fixture.kind && b.Start == z {
						literal = true
					}
				}
				if !literal || source[a:z] != "- move\n"+fixture.separator {
					t.Fatal("fixture does not select exactly the item before a real literal block")
				}
				s, view := liveTester(source, a, EditorOptions{ContinueBlocks: true, InPlaceCode: true, InPlaceTables: true})
				anchor, caret := a, z
				if reverse {
					anchor, caret = z, a
				}
				s.SetSelection(anchor, caret)
				view.Frame()
				buffer := s.Buffer()
				view.Key(0, ui.KeyTab)
				if got := buffer.OriginalText(); got != want || s.Buffer() != buffer {
					t.Fatalf("exclusive literal endpoint stole/destroyed the selected item: %q want %q", got, want)
				}
				if gotAnchor, gotCaret := s.Selection(); gotAnchor != anchor+2 || gotCaret != caret+2 {
					t.Fatalf("indent lost selection direction/range: %d:%d want %d:%d", gotAnchor, gotCaret, anchor+2, caret+2)
				}
				d = parser.Parse(s.source)
				nested := false
				for i := 1; i < d.Len(); i++ {
					b := d.Block(i)
					if b.Kind == parser.Item && b.Start == a+2 {
						parent := d.Block(b.Parent).Parent
						nested = parent > 0 && d.Block(parent).Kind == parser.Item && d.Block(parent).Start == 0
					}
				}
				if !nested || !strings.HasSuffix(buffer.OriginalText(), fixture.literal) {
					t.Fatal("the whole Item was not nested under its predecessor or the literal bytes changed")
				}
				view.Command("undo")
				if gotAnchor, gotCaret := s.Selection(); buffer.OriginalText() != source || gotAnchor != anchor || gotCaret != caret {
					t.Fatalf("one undo did not restore exact source and directional selection: %q %d:%d", buffer.OriginalText(), gotAnchor, gotCaret)
				}
				view.Command("redo")
				if buffer.OriginalText() != want {
					t.Fatal("exclusive-boundary indent did not redo as one native source transaction")
				}
			})
		}
	}
}

func TestWritingMixedItemAndLiteralSelectionDoesNotReplaceContent(t *testing.T) {
	const source = "- parent\n- move\n```sql\nx\n```"
	a, z := 9, strings.Index(source, "\nx\n")+2 // actually includes code content
	for _, reverse := range []bool{false, true} {
		for _, mods := range []ui.Modifiers{0, ui.Shift} {
			s, view := liveTester(source, a, EditorOptions{ContinueBlocks: true, InPlaceCode: true})
			anchor, caret := a, z
			if reverse {
				anchor, caret = z, a
			}
			s.SetSelection(anchor, caret)
			view.Frame()
			version := s.Buffer().Version()
			view.Key(mods, ui.KeyTab)
			if gotAnchor, gotCaret := s.Selection(); s.Buffer().OriginalText() != source || s.Buffer().Version() != version || gotAnchor != anchor || gotCaret != caret {
				t.Fatalf("mixed selection was destructively replaced, reverse=%v mods=%v: %q %d:%d", reverse, mods, s.Buffer().OriginalText(), gotAnchor, gotCaret)
			}
		}
	}
}

func TestWritingExclusiveBoundaryRetainsRawSourceAndCodeTabContracts(t *testing.T) {
	const source = "- parent\n- move\n```sql\nx\n```"
	for _, sourceMode := range []bool{false, true} {
		for _, reverse := range []bool{false, true} {
			s, view := liveTester(source, 9, EditorOptions{ContinueBlocks: sourceMode, InPlaceCode: true})
			s.SourceMode = sourceMode // SDK default false or explicitly raw source
			if reverse {
				s.SetSelection(16, 9)
			} else {
				s.SetSelection(9, 16)
			}
			view.Frame()
			view.Key(0, ui.KeyTab)
			if got := s.Buffer().OriginalText(); got != "- parent\n\t```sql\nx\n```" {
				t.Fatalf("default/source Tab contract changed: %q", got)
			}
			view.Command("undo")
			if s.Buffer().OriginalText() != source {
				t.Fatal("raw Tab contract no longer undoes through the same Buffer")
			}
		}
	}
	a := strings.Index(source, "\nx\n") + 1
	s, view := liveTester(source, a, EditorOptions{ContinueBlocks: true, InPlaceCode: true})
	s.SetSelection(a, a+1)
	view.Frame()
	view.Key(0, ui.KeyTab)
	if got := s.Buffer().OriginalText(); got != strings.Replace(source, "\nx\n", "\n\t\n", 1) {
		t.Fatalf("a true code-only selection lost literal Tab editing: %q", got)
	}
	view.Command("undo")
	if s.Buffer().OriginalText() != source {
		t.Fatal("literal code Tab undo changed original source")
	}
}
