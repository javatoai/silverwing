package markdown

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
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
