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
