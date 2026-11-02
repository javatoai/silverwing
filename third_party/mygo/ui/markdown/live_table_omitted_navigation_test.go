package markdown

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestLiveOmittedCellArrowsKeepTheIntendedColumnAndUndo(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n"} {
		for _, route := range []string{"down", "right-twice"} {
			t.Run(route+strings.ReplaceAll(ending, "\n", "LF"), func(t *testing.T) {
				logical := "\ufeff| A | B | C |\n|---|---|---|\n| x |\n\n<!-- untouched -->\n"
				source := strings.ReplaceAll(logical, "\n", ending)
				at := strings.Index(logical, "C")
				if route == "right-twice" {
					at = strings.Index(logical, "x") + 1
				}
				s, view := keyboardViewportTester(source, at, 960, 1, false, false)
				buffer, version := s.Buffer(), s.Buffer().Version()
				if route == "down" {
					view.Key(0, ui.KeyDown)
				} else {
					view.Key(0, ui.KeyRight)
					view.Key(0, ui.KeyRight)
				}
				assertNavigationTableCell(t, s, 1, 2)
				caret, ok := view.TextCaret()
				for _, hit := range s.tableCells {
					if hit.cell.row == 1 && hit.cell.column == 2 && (!ok || caret.X < hit.rect.X || caret.X > hit.rect.X+hit.rect.W || caret.Y < hit.rect.Y || caret.Y+caret.H > hit.rect.Y+hit.rect.H) {
						t.Fatalf("caret is outside the intended virtual cell: %+v target=%+v", caret, hit.rect)
					}
				}
				if buffer.Version() != version || buffer.OriginalText() != source {
					t.Fatal("arrow navigation materialized omitted cells")
				}
				view.Type("third")
				want := strings.Replace(source, "| x |", "| x |  | third |", 1)
				if buffer.OriginalText() != want || buffer.Version() != version+1 {
					t.Fatalf("input did not materialize only the intended cell atomically: %q", buffer.OriginalText())
				}
				assertNavigationTableCell(t, s, 1, 2)
				view.Command("undo")
				if buffer.OriginalText() != source {
					t.Fatal("one undo did not restore the original omitted row")
				}
				assertNavigationTableCell(t, s, 1, 2)
				view.Type("again")
				if buffer.OriginalText() != strings.Replace(source, "| x |", "| x |  | again |", 1) {
					t.Fatal("undo lost the omitted column's editing intent")
				}
			})
		}
	}
}

func TestLiveOmittedCellNavigationIMEPasteAndSourceLimits(t *testing.T) {
	for _, operation := range []string{"ime", "paste", "limit"} {
		t.Run(operation, func(t *testing.T) {
			const source = "| A | B | C |\r\n|---|---|---|\r\n| x\r\n"
			s, view := keyboardViewportTester(source, strings.Index(source, "C"), 960, 1, false, false)
			view.Key(0, ui.KeyDown)
			assertNavigationTableCell(t, s, 1, 2)
			const payload = "中文😀|值\n下一行"
			if operation == "ime" {
				view.Compose("候选😀", 4)
				if s.Buffer().Version() != 0 || s.Buffer().OriginalText() != source {
					t.Fatal("omitted cell preedit committed source")
				}
				shown := 0
				for _, run := range s.runs {
					shown += strings.Count(run.text, "候选😀")
				}
				if shown != 1 {
					t.Fatalf("preedit rendered %d times across cells sharing an offset", shown)
				}
				keyboardViewportCaret(t, s, view, s.displayCaretOffset())
				view.Type(payload)
			} else if operation == "paste" {
				view.SetClipboard(payload)
				view.Command("paste")
			} else {
				s.Buffer().SetMaxSourceBytes(len(source) + len(payload))
				view.Type(payload)
				if s.Buffer().OriginalText() != source || s.Buffer().Version() != 0 {
					t.Fatal("oversize omitted-cell edit partially materialized source")
				}
				assertNavigationTableCell(t, s, 1, 2)
				s.Buffer().SetMaxSourceBytes(len(source) + 100)
				view.Type(payload)
			}
			want := strings.Replace(source, "| x\r\n", "| x |  | 中文😀\\|值<br>下一行 |\r\n", 1)
			if s.Buffer().OriginalText() != want || s.Buffer().Version() != 1 {
				t.Fatalf("omitted-cell edit lost escaping or one-transaction semantics: %q", s.Buffer().OriginalText())
			}
			view.Command("undo")
			if s.Buffer().OriginalText() != source {
				t.Fatal("omitted-cell edit cannot be undone in one step")
			}
		})
	}
}

func TestLiveOmittedCellArrowsRemainReadOnlyAndClearOnExplicitSelection(t *testing.T) {
	const source = "| A | B | C |\n|---|---|---|\n| x |\n"
	s, view := keyboardViewportTester(source, strings.Index(source, "C"), 960, 1, false, true)
	view.Key(0, ui.KeyDown)
	assertNavigationTableCell(t, s, 1, 2)
	view.Type("no")
	if s.Buffer().Version() != 0 || s.Buffer().OriginalText() != source {
		t.Fatal("read-only virtual cell accepted input")
	}
	s, view = keyboardViewportTester(source, strings.Index(source, "C"), 960, 1, false, false)
	view.Key(0, ui.KeyDown)
	s.SetSelection(strings.Index(source, "x"), strings.Index(source, "x"))
	view.Frame()
	view.Type("first-")
	if s.Buffer().OriginalText() != strings.Replace(source, "| x |", "| first-x |", 1) {
		t.Fatal("old omitted-cell intent hijacked explicit source selection")
	}
}
