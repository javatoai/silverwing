package desktop

import (
	"fmt"
	"image"
	"strings"
	"testing"

	"silverwing-go/internal/editor"

	"github.com/egoist/mygo/ui"
)

// Exercise the production Options.Code Mermaid renderer through the complete
// materials page. The bitmap is a cached render result: both its native image
// and the real header intentionally contribute no editor text runs.
func TestMaterialsMarkdownPointerMermaidRevealsWholeSource(t *testing.T) {
	const fence = "```mermaid\nflowchart LR\n    A[开始] --> B[完成]\n```"
	for _, ending := range []struct{ name, text string }{
		{"following-paragraph", "\n\n末尾段落。\n"},
		{"trailing-newline", "\n"},
	} {
		for _, width := range []int{620, 1100} {
			for _, target := range []string{"Mermaid 图表", "Mermaid 图表预览"} {
				t.Run(fmt.Sprintf("%s/%d/%s", ending.name, width, target), func(t *testing.T) {
					source := "# 点击图表验收\n\n" + fence + ending.text
					s, _, _, tester := markdownLiveAcceptanceFixture(t, source, width == 620, width)
					state := focusMarkdownLiveAcceptance(t, s, tester)
					var part editor.Part
					for _, candidate := range state.doc.Parts() {
						if candidate.Kind == editor.PartMermaid {
							part = candidate
							break
						}
					}
					if part.ID == 0 {
						t.Fatal("fixture did not parse the Mermaid fence")
					}
					if previous := state.diagrams[part.ID]; previous != nil && previous.cancel != nil {
						previous.cancel()
					}
					code := editor.FindFence(part.Source).Code(part.Source)
					key := code
					if s.dark {
						key += "\x00dark"
					}
					state.diagrams[part.ID] = &markdownDiagramState{
						key: key, source: code,
						bitmap: ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 240, 72))),
					}
					state.live.SetSelection(2, 2)
					tester.Frame()
					live, buffer, version := state.live, state.live.Buffer(), state.live.Buffer().Version()
					bounds, ok := tester.Find(target)
					if !ok || bounds.W <= 0 || bounds.H <= 0 {
						t.Fatalf("production Mermaid target missing: %+v texts=%q", bounds, tester.Texts())
					}
					x, y := bounds.X+bounds.W/2, bounds.Y+bounds.H/2
					tester.Press(x, y)
					tester.Release(x, y)
					a, z := live.Selection()
					start := strings.Index(source, fence)
					end := start + len(fence)
					t.Logf("production pointer target=%q position=(%.1f,%.1f) selection=[%d,%d] fence=[%d,%d)", target, x, y, a, z, start, end)
					if a != z || a < start || a >= end {
						t.Fatalf("Mermaid pointer selected outside fence: [%d,%d], fence [%d,%d)", a, z, start, end)
					}
					if !tester.HasText(fence) || tester.HasText("Mermaid 图表预览") {
						t.Fatalf("Mermaid pointer did not reveal complete fence source in place: %q", tester.Texts())
					}
					if state.live != live || live.Buffer() != buffer || buffer.Version() != version || s.materialsUnit.state.markdownBitmapViewer != nil {
						t.Fatal("Mermaid source reveal changed the buffer or opened another viewer")
					}
					assertMarkdownAcceptanceSource(t, s, state, source)
				})
			}
		}
	}
}
