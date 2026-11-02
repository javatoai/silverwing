package markdown

import (
	"testing"
)

func TestRenderedLinkOnlyParagraphReceivesClick(t *testing.T) {
	source := "# title\n\n| a | b |\n|---|---|\n| x | y |\n\n[编辑链接](https://example.com)\n"
	opened := 0
	state, tester := liveTester(source, 0, EditorOptions{InPlaceTables: true, RenderedLinks: true, Options: &Options{OnLink: func(string) { opened++ }}})
	r, ok := tester.Find("编辑链接")
	if !ok {
		t.Fatalf("missing link %v", tester.Texts())
	}
	x, y := r.X+r.W/2-state.rect.X, r.Y+r.H/2-state.rect.Y
	a, z, _ := state.positionEdges(x, y)
	if err := tester.Click("编辑链接"); err != nil {
		t.Fatal(err)
	}
	if opened != 1 {
		for _, run := range state.runs {
			t.Logf("run %q %v %v", run.text, run.boundaries, run.rect)
		}
		t.Fatalf("click count %d hit %d:%d at %v,%v root %v texts %v", opened, a, z, x, y, state.rect, tester.Texts())
	}
}
