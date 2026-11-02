package markdown

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"math"
	"strings"
	"testing"
)

func TestReviewCodeLanguageKeepsScrolledViewportAndOtherFences(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n"} {
		t.Run(fmt.Sprintf("ending-%q", ending), func(t *testing.T) {
			prefix := strings.Repeat("已有中文😀段落。\n\n", 26)
			source := "\ufeff" + strings.ReplaceAll(prefix+"```sql  title=\"query\"\nSELECT 1;\n```\n\n"+strings.Repeat("后续段落。\n\n", 24)+"```js\nconsole.log(2);\n```", "\n", ending)
			at := strings.Index(normalizedText(source), "SELECT")
			s, v := liveTester(source, at, EditorOptions{InPlaceCode: true})
			s.anchor, s.caret, s.reveal = 0, 0, false
			v.Frame()
			before := s.scroll.Y
			if before < 100 {
				t.Fatalf("fixture did not scroll to code: %g", before)
			}
			for _, choice := range []struct{ label, token string }{{"Python", "python"}, {"SQL", "sql"}, {"Python", "python"}} {
				chooseLiveCodeLanguage(t, v, choice.label)
				want := "\ufeff" + strings.ReplaceAll(prefix+"```"+choice.token+"  title=\"query\"\nSELECT 1;\n```\n\n"+strings.Repeat("后续段落。\n\n", 24)+"```js\nconsole.log(2);\n```", "\n", ending)
				if got := s.Buffer().OriginalText(); got != want {
					t.Fatal("language changed another fence, metadata, BOM or EOL")
				}
				if s.anchor != 0 || s.caret != 0 || math.Abs(float64(s.scroll.Y-before)) > 2 {
					t.Fatalf("language moved old caret/viewport: anchor=%d caret=%d scroll=%g -> %g", s.anchor, s.caret, before, s.scroll.Y)
				}
			}
			v.Command("undo")
			if !strings.Contains(s.Buffer().OriginalText(), "```sql  title=") {
				t.Fatal("one undo did not restore the previous language")
			}
		})
	}
}
func TestReviewRejectedCodeLanguageRestoresVisibleSelection(t *testing.T) {
	const source = "```sql\nSELECT 1;\n```"
	s, v := liveTester(source, strings.Index(source, "SELECT"), EditorOptions{InPlaceCode: true})
	s.Buffer().SetMaxSourceBytes(len(source))
	chooseLiveCodeLanguage(t, v, "Python")
	if s.Buffer().OriginalText() != source || s.Buffer().Version() != 0 {
		t.Fatal("rejected language changed source/history")
	}
	if !v.HasText("SQL") || v.HasText("Python") {
		t.Fatalf("rejected metadata left wrong label: %q", v.Texts())
	}
}
func TestReviewOmittedTableNavigationIsNotAnEdit(t *testing.T) {
	for _, tail := range []string{"| x |", "| x"} {
		for _, navigation := range []string{"pointer", "tab"} {
			t.Run(tail+navigation, func(t *testing.T) {
				source := "before\r\n\r\n| A | B | C |\r\n|---|---|---|\r\n" + tail
				changed := 0
				s, v := liveTester(source, strings.Index(normalizedText(source), "x"), EditorOptions{InPlaceTables: true, OnChange: func() { changed++ }})
				if navigation == "pointer" {
					found := false
					for _, hit := range s.tableCells {
						if hit.cell.row == 1 && hit.cell.column == 2 {
							v.ClickAt(hit.rect.X+hit.rect.W/2, hit.rect.Y+hit.rect.H/2)
							found = true
							break
						}
					}
					if !found {
						t.Fatal("missing target cell")
					}
				} else {
					v.Key(0, ui.KeyTab)
					v.Key(0, ui.KeyTab)
				}
				if s.Buffer().OriginalText() != source || s.Buffer().Version() != 0 || changed != 0 {
					t.Fatalf("%s navigation mutated source: %q v=%d changes=%d", navigation, s.Buffer().OriginalText(), s.Buffer().Version(), changed)
				}
				v.Type("中文😀")
				if structuredCellText(s, 0, 1, 2) != "中文😀" || structuredCellText(s, 0, 1, 1) != "" {
					t.Fatal("omitted target column lost identity")
				}
				if s.Buffer().Version() != 1 || changed != 1 {
					t.Fatal("first input did not materialize in one transaction")
				}
				v.Command("undo")
				if s.Buffer().OriginalText() != source {
					t.Fatal("one undo did not restore original omitted cells")
				}
			})
		}
	}
}
