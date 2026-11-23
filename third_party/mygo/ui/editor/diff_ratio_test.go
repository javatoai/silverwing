package editor

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestDiffViewerUnequalWidthsKeepWrappedLinesAligned(t *testing.T) {
	common := strings.Repeat("long_source_identifier ", 24)
	for _, ratio := range []float32{.25, .75} {
		d := &DiffState{Old: NewBuffer(common + "\nold\n" + common + "\ntail"),
			New:    NewBuffer(common + "\nnew\ninserted\n" + common + "\ntail"),
			Layout: DiffSplit, SplitRatio: ratio, ExpandUnchanged: true, Wrap: true}
		tester := ui.NewTester(func(c *ui.Context) { DiffView(c, d).Fill() }, 1000, 500)
		tester.Frame()
		old, current := &d.eds[0].view.heights, &d.eds[1].view.heights
		if old.total() != current.total() {
			t.Fatalf("ratio %v produces different side heights: %v vs %v", ratio, old.total(), current.total())
		}
		for _, lines := range [][2]int{{0, 0}, {2, 3}, {3, 4}} {
			if a, b := old.geometry(lines[0]).rows, current.geometry(lines[1]).rows; a != b {
				t.Fatalf("ratio %v misaligned unchanged lines %v: %v vs %v", ratio, lines, a, b)
			}
		}
	}
}

func TestDiffSplitRatioDividerKeepsFractionAndDoubleClickReset(t *testing.T) {
	d := &DiffState{Old: NewBuffer("old"), New: NewBuffer("new"), SplitRatio: .4}
	tester := ui.NewTester(func(c *ui.Context) { DiffView(c, d).Fill() }, 901, 400)
	divider, found := tester.Find("Divider")
	if !found {
		t.Fatal("split diff has no keyboard-accessible divider")
	}
	x, y := divider.X+divider.W/2, divider.Y+divider.H/2
	tester.ClickAt(x, y)
	if !tester.Focused("Divider") {
		t.Fatal("click did not focus the divider")
	}
	tester.Key(0, ui.KeyRight)
	if d.SplitRatio <= .4 || d.SplitPercent != 0 {
		t.Fatalf("keyboard adjustment changed the wrong split value: ratio=%v percent=%v", d.SplitRatio, d.SplitPercent)
	}
	divider, _ = tester.Find("Divider")
	x = divider.X + divider.W/2
	tester.ClickAt(x, y)
	tester.ClickAt(x, y)
	if d.SplitRatio != .5 {
		t.Fatalf("double-click reset ratio to %v, want .5", d.SplitRatio)
	}
}
