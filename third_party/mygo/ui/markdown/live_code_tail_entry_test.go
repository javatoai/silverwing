package markdown

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestLiveCodeTailDownReachesEveryRealBlankLineAndEOF(t *testing.T) {
	for _, count := range []int{2, 3} {
		for _, ending := range []string{"\n", "\r\n"} {
			logical := "```js\nx\n```" + strings.Repeat("\n", count)
			source := strings.ReplaceAll(logical, "\n", ending)
			s, view := keyboardViewportTester(source, strings.Index(logical, "x")+1, 700, 1, false, false)
			var previous float32
			for step := 0; step < count; step++ {
				view.Key(0, ui.KeyDown)
				caret, ok := view.TextCaret()
				if !ok || step > 0 && caret.Y <= previous {
					t.Fatalf("Down stopped before a real tail row: step=%d source=%q caret=%+v previousY=%g", step, source, caret, previous)
				}
				previous = caret.Y
			}
			if a, z := s.Selection(); a != len(logical) || z != len(logical) {
				t.Fatalf("Down did not reach EOF: %d:%d want%d", a, z, len(logical))
			}
			if s.Buffer().Version() != 0 || s.Buffer().OriginalText() != source {
				t.Fatal("blank-row navigation changed source")
			}
			view.Type("at EOF")
			if s.Buffer().OriginalText() != source+"at EOF" {
				t.Fatal("tail input landed before a preserved trailing newline")
			}
			view.Command("undo")
			if s.Buffer().OriginalText() != source {
				t.Fatal("tail EOF undo lost original blank lines")
			}
		}
	}
}

func TestLiveCodeTailEOFLocationsTypeWhereTheNativeCaretIsShown(t *testing.T) {
	for _, route := range []string{"selection", "control-end", "source-roundtrip"} {
		for _, ending := range []string{"\n", "\r\n"} {
			t.Run(route+strings.ReplaceAll(ending, "\n", "LF"), func(t *testing.T) {
				logical := "\ufeff```js title=kept\nx\n```"
				source := strings.ReplaceAll(logical, "\n", ending)
				s, view := keyboardViewportTester(source, strings.Index(logical, "x")+1, 700, 1, false, false)
				if route == "control-end" {
					view.Key(ui.Ctrl, ui.KeyEnd)
				} else {
					if route == "source-roundtrip" {
						s.SourceMode = true
						view.Frame()
					}
					s.SetSelection(len(logical), len(logical))
					view.Frame()
					if route == "source-roundtrip" {
						s.SourceMode = false
						view.Frame()
					}
				}
				if a, z := s.Selection(); a != len(logical) || z != len(logical) {
					t.Fatalf("document-end navigation remained inside hidden fences: %d:%d want%d", a, z, len(logical))
				}
				row, ok := view.Find(codeTailLabel)
				caret, active := view.TextCaret()
				if !ok || !active || caret.Y < row.Y-.1 || caret.Y+caret.H > row.Y+row.H+.1 {
					t.Fatal("EOF location has no actual caret in the writing row")
				}
				view.Type("outside😀")
				want := source + ending + "outside😀"
				if s.Buffer().OriginalText() != want || s.Buffer().Version() != 1 {
					t.Fatalf("typing disagrees with visible EOF caret: %q want%q", s.Buffer().OriginalText(), want)
				}
				view.Command("undo")
				if s.Buffer().OriginalText() != source {
					t.Fatal("EOF source selection input did not undo atomically")
				}
				view.Type("again")
				if s.Buffer().OriginalText() != source+ending+"again" {
					t.Fatal("undo of EOF input lost the outside-code location")
				}
			})
		}
	}
}
