package markdown

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func assertLiveSoftBreakRows(t *testing.T, s *EditorState, stage string) {
	t.Helper()
	start := strings.Index(s.Buffer().String(), "dd\ndd\ndd")
	if start < 0 {
		t.Fatalf("%s: physical source lines changed: %q", stage, s.Buffer().OriginalText())
	}
	var rows [3]float32
	for i := range rows {
		at := start + i*3
		run := s.caretRun(at)
		if run == nil || run.layout == nil {
			t.Fatalf("%s: source offset %d has no shaped run", stage, at)
		}
		caret := run.layout.CaretAt(ui.TextCaretPosition{Index: run.unitForSource(at)})
		rows[i] = run.rect.Y + caret.Y
		t.Logf("%s line=%d sourceOffset=%d glyphY=%.2f shapedLines=%d run=%q", stage, i+1, at, rows[i], len(run.layout.Lines()), run.text)
	}
	a, z := s.Selection()
	t.Logf("%s active=[%d,%d] selection=[%d,%d] focused=%v readOnly=%v original=%q", stage, s.activeStart, s.activeEnd, a, z, s.focused, s.opts.ReadOnly, s.Buffer().OriginalText())
	if rows[0] >= rows[1] || rows[1] >= rows[2] {
		t.Errorf("%s: three physical source lines collapsed: glyphY=%v", stage, rows)
	}
}

func TestLiveEditorSoftBreakEnterKeepsPhysicalLines(t *testing.T) {
	for _, tc := range []struct{ name, source, ending string }{
		{"bare-LF", "dd\ndd\ndd", "\n"},
		{"bare-CRLF", "dd\r\ndd\r\ndd", "\r\n"},
		{"existing-blank-CRLF", "\r\ndd\r\ndd\r\ndd\r\n\r\n", "\r\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			normalized := strings.ReplaceAll(tc.source, "\r\n", "\n")
			at := strings.LastIndex(normalized, "dd") + 2
			s, tester := liveTester(tc.source, at, EditorOptions{})
			buffer := s.Buffer()
			assertLiveSoftBreakRows(t, s, "before Enter")
			originalAt := strings.LastIndex(tc.source, "dd") + 2
			versions := []string{tc.source}
			for enter := 1; enter <= 2; enter++ {
				tester.Key(0, ui.KeyEnter)
				want := tc.source[:originalAt] + strings.Repeat(tc.ending, enter) + tc.source[originalAt:]
				versions = append(versions, want)
				if buffer != s.Buffer() || buffer.OriginalText() != want {
					t.Fatalf("Enter %d changed original newlines or buffer: %q want %q", enter, buffer.OriginalText(), want)
				}
				assertLiveSoftBreakRows(t, s, "after Enter "+strings.Repeat("I", enter))
			}
			for undo := 1; undo <= 2; undo++ {
				tester.Command("undo")
				if want := versions[2-undo]; buffer.OriginalText() != want {
					t.Fatalf("Undo %d did not restore exact source: %q want %q", undo, buffer.OriginalText(), want)
				}
			}
			assertLiveSoftBreakRows(t, s, "after Undo")
		})
	}
}

func TestLiveEditorSoftBreakClickAndModesKeepPhysicalLines(t *testing.T) {
	const source = "# Heading\n\ndd\ndd\ndd\n\nEnd."
	s := NewEditorState(editor.NewBufferPreservingLineEndings(source))
	s.SetSelection(strings.Index(source, "dd"), strings.Index(source, "dd"))
	s.Focus()
	options := EditorOptions{}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			ui.Button(c, "Outside editor")
			Editor(c, s, options).Grow(1).FillWidth()
		})
	}, 700, 500)
	buffer, version := s.Buffer(), s.Buffer().Version()
	assertLiveSoftBreakRows(t, s, "active source")
	heading, ok := tester.Find("Heading")
	if !ok {
		t.Fatal("inactive heading missing")
	}
	x, y := heading.X+2, heading.Y+heading.H/2
	tester.Press(x, y)
	tester.Release(x, y)
	assertLiveSoftBreakRows(t, s, "after clicking another block")
	if err := tester.Click("Outside editor"); err != nil {
		t.Fatal(err)
	}
	assertLiveSoftBreakRows(t, s, "after losing focus")
	s.Focus()
	tester.Frame()
	assertLiveSoftBreakRows(t, s, "after regaining focus")
	options.ReadOnly = true
	tester.Frame()
	assertLiveSoftBreakRows(t, s, "read-only mode")
	options.ReadOnly = false
	s.SourceMode = true
	tester.Frame()
	assertLiveSoftBreakRows(t, s, "source mode")
	s.SourceMode = false
	tester.Frame()
	assertLiveSoftBreakRows(t, s, "return to live mode")
	if s.Buffer() != buffer || buffer.Version() != version || buffer.OriginalText() != source {
		t.Fatal("display/focus changes modified the document buffer")
	}
}

func TestLiveEditorSoftBreakReadOnlyStartsWithPhysicalLines(t *testing.T) {
	const source = "dd\r\ndd\r\ndd\r\n"
	s, _ := liveTester(source, 0, EditorOptions{ReadOnly: true})
	assertLiveSoftBreakRows(t, s, "initial read-only projection")
	if s.Buffer().OriginalText() != source {
		t.Fatal("initial read-only projection changed original CRLF bytes")
	}
}

func TestOrdinaryMarkdownSoftBreakKeepsCommonMarkDisplay(t *testing.T) {
	const source = "dd\ndd\ndd"
	for _, streaming := range []bool{false, true} {
		options := &Options{Reveal: &Reveal{Unit: AtOnce, Fade: -1}}
		tester := ui.NewTester(func(c *ui.Context) {
			if streaming {
				Stream(c, source, options).FillWidth()
			} else {
				View(c, source, options).FillWidth()
			}
		}, 700, 500)
		if !tester.HasText("dd dd dd") || tester.HasText(source) {
			t.Fatalf("ordinary Markdown display changed: streaming=%v texts=%q", streaming, tester.Texts())
		}
	}
}
