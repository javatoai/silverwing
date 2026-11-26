package markdown

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// A custom fence may render native shapes or images without contributing any
// text runs. Pointer input must still enter that fence's complete source block.
func TestLiveEditorPointerCustomFenceRevealsSource(t *testing.T) {
	const fence = "```mermaid\nflowchart LR\nA[开始] --> B[完成]\n```"
	for _, ending := range []struct{ name, text string }{
		{"following-paragraph", "\n\nAfter paragraph.\n"},
		{"trailing-blank-line", "\n\n"},
		{"trailing-newline", "\n"},
		{"end-of-document", ""},
	} {
		for _, target := range []string{"Native fence header", "Native fence node"} {
			t.Run(ending.name+"/"+target, func(t *testing.T) {
				source := "# Title\n\n" + fence + ending.text
				options := &Options{Code: map[string]func(*ui.Context, *CodeBlockView){
					"mermaid": func(c *ui.Context, _ *CodeBlockView) {
						ui.Column(c).FillWidth().Height(100).Children(func() {
							ui.Text(c, "Native fence header")
							ui.Column(c).FillWidth().Height(64).Label("Native fence node")
						})
					},
				}}
				s, tester := liveTester(source, 2, EditorOptions{Options: options})
				buffer, version := s.Buffer(), s.Buffer().Version()
				bounds, ok := tester.Find(target)
				if !ok || bounds.W <= 0 || bounds.H <= 0 {
					t.Fatalf("native fence target missing: %+v texts=%q", bounds, tester.Texts())
				}
				start := strings.Index(source, fence)
				end := start + len(fence)
				for _, run := range s.runs {
					if len(run.boundaries) > 0 && run.boundaries[0] >= start && run.boundaries[0] < end {
						t.Fatalf("custom fence unexpectedly contributed a text run: %+v", run.boundaries)
					}
				}
				x, y := bounds.X+bounds.W/2, bounds.Y+bounds.H/2
				tester.Press(x, y)
				tester.Release(x, y)
				a, z := s.Selection()
				t.Logf("pointer target=%q position=(%.1f,%.1f) selection=[%d,%d] fence=[%d,%d)", target, x, y, a, z, start, end)
				if a != z || a < start || a >= end {
					t.Fatalf("pointer selected outside custom fence: [%d,%d], fence [%d,%d)", a, z, start, end)
				}
				if !tester.HasText(fence) || tester.HasText("Native fence header") {
					t.Fatalf("pointer did not replace the rendered fence with complete source: %q", tester.Texts())
				}
				if s.Buffer() != buffer || buffer.Version() != version || buffer.OriginalText() != source {
					t.Fatal("revealing custom fence source replaced or changed the document buffer")
				}
			})
		}
	}
}

func TestLiveEditorPointerEmptyDocumentKeepsInputPosition(t *testing.T) {
	s, tester := liveTester("", 0, EditorOptions{})
	bounds, ok := tester.Find("Markdown")
	if !ok {
		t.Fatal("empty native editor missing")
	}
	x, y := bounds.X+16, bounds.Y+16
	tester.Press(x, y)
	tester.Release(x, y)
	a, z := s.Selection()
	if a != 0 || z != 0 {
		t.Fatalf("empty document pointer selected [%d,%d]", a, z)
	}
	tester.Type("中文")
	if s.Buffer().OriginalText() != "中文" {
		t.Fatalf("empty document lost native input: %q", s.Buffer().OriginalText())
	}
}

func TestLiveEditorPointerLongSQLKeepsViewport(t *testing.T) {
	const visibleLine = "SELECT 111;"
	const offscreenLine = "SELECT 222;"
	source := "Before SQL.\n\n```sql\n" + strings.Repeat("SELECT 0;\n", 10) + visibleLine + "\n" +
		strings.Repeat("SELECT 0;\n", 80) + offscreenLine + "\n```\n\nAfter SQL."
	lineStart := strings.Index(source, visibleLine)
	lineEnd := lineStart + len(visibleLine)
	target := lineStart + len("SELECT ")
	s := NewEditorState(editor.NewBufferPreservingLineEndings(source))
	var paintedRows []ui.Rect
	observe := false
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().DrawOver(func(_ *ui.Painter, _ ui.Rect) {
			if observe {
				row, _ := s.ownedCaretBounds(target)
				paintedRows = append(paintedRows, row)
			}
		}).Children(func() {
			Editor(c, s, EditorOptions{InPlaceCode: true}).Fill()
		})
	}, 600, 300)
	tester.Scroll(300, 150, 0, 80)
	row, ok := s.ownedCaretBounds(target)
	before := s.scroll.Y
	if !ok || before == 0 || row.Y < 16 || row.Y+row.H > s.rect.H-16 {
		t.Fatalf("SQL fixture needs a visible interior line after scrolling: row=%+v scroll=%g viewport=%+v", row, before, s.rect)
	}
	x, y := s.rect.X+row.X, s.rect.Y+row.Y+row.H/2
	observe = true
	checkViewport := func(phase string) {
		t.Helper()
		if s.scroll.Y != before {
			t.Errorf("%s shifted document scroll from %g to %g", phase, before, s.scroll.Y)
		}
		for _, painted := range paintedRows {
			if delta := painted.Y - row.Y; painted.H == 0 || delta < -0.5 || delta > 0.5 {
				t.Errorf("%s moved the clicked SQL line during paint: before=%+v painted=%+v", phase, row, painted)
				break
			}
		}
		paintedRows = paintedRows[:0]
	}
	tester.Press(x, y)
	checkViewport("pointer down")
	tester.Release(x, y)
	checkViewport("pointer up")
	tester.Frame()
	checkViewport("settled frame")
	observe = false
	if anchor, caret := s.Selection(); anchor != caret || caret < lineStart || caret > lineEnd {
		t.Fatalf("click placed selection %d:%d outside SQL line %d:%d", anchor, caret, lineStart, lineEnd)
	}

	offscreen := strings.Index(source, offscreenLine) + len("SELECT ")
	s.SetSelection(offscreen, offscreen)
	tester.Frame()
	if caret, active := tester.TextCaret(); !active || caret.H <= 0 || caret.Y < 0 || caret.Y+caret.H > s.rect.H || s.scroll.Y <= before {
		t.Fatalf("explicit selection did not reveal the offscreen SQL caret: caret=%+v active=%v scroll=%g", caret, active, s.scroll.Y)
	}
}
