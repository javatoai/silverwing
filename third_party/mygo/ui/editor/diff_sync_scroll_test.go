package editor

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestDiffSynchronizedHorizontalDragWheelAndUnequalPanes(t *testing.T) {
	for _, oldLong := range []bool{false, true} {
		old, current := "short", strings.Repeat("long ", 180)
		if oldLong {
			old, current = current, old
		}
		d := &DiffState{Old: NewBuffer(old + strings.Repeat("\n", 60)), New: NewBuffer(current + strings.Repeat("\n", 60)), Layout: DiffSplit, SplitRatio: .37, SyncHorizontal: true, ExpandUnchanged: true}
		tester := ui.NewTester(func(c *ui.Context) { DiffView(c, d).Fill() }, 900, 320)
		for side := range d.eds {
			ed := &d.eds[side]
			_, horizontal := ed.scrollbars()
			if !horizontal {
				t.Fatalf("long-old=%t pane %d lacks shared horizontal scrollbar", oldLong, side)
			}
			a, z := ed.thumb(true)
			x, y := ed.r.X+ed.textX+(a+z)/2, ed.r.Y+ed.viewH-scrollbarWidth/2
			before := d.ScrollPosition()
			tester.Press(x, y)
			tester.Move(x+80, y)
			tester.Release(x+80, y)
			after := d.ScrollPosition()
			if after.OldX <= before.OldX || after.OldX != after.NewX || after.Y != before.Y {
				t.Fatalf("side%d drag not shared: %+v -> %+v", side, before, after)
			}
		}
		before := d.ScrollPosition()
		tester.Scroll(d.eds[1].r.X+100, 100, 50, 60)
		after := d.ScrollPosition()
		if after.OldX <= before.OldX || after.OldX != after.NewX || after.Y <= before.Y || d.eds[0].scrollY != d.eds[1].scrollY {
			t.Fatalf("wheel not shared: %+v -> %+v", before, after)
		}
		// Even the shorter pane can drive the long pane all the way to its tail.
		ed := &d.eds[0]
		a, z := ed.thumb(true)
		x, y := ed.r.X+ed.textX+(a+z)/2, ed.r.Y+ed.viewH-scrollbarWidth/2
		tester.Press(x, y)
		tester.Move(ed.r.X+ed.r.W-1, y)
		tester.Release(ed.r.X+ed.r.W-1, y)
		if d.eds[0].scrollX != d.eds[1].scrollX || d.eds[0].scrollX < 1000 {
			t.Fatalf("shared range cannot reach long tail: %+v", d.ScrollPosition())
		}
		d.SplitRatio = .64
		tester.Frame()
		if d.eds[0].scrollX != d.eds[1].scrollX {
			t.Fatal("resize broke shared X")
		}
		if d.Old.String() != old+strings.Repeat("\n", 60) || d.New.String() != current+strings.Repeat("\n", 60) {
			t.Fatal("scrolling changed compared sources")
		}
	}
}

func TestDiffSynchronizedHorizontalRestoreWrapAndShrink(t *testing.T) {
	d := &DiffState{Old: NewBuffer(strings.Repeat("old ", 150)), New: NewBuffer(strings.Repeat("new ", 180)), Layout: DiffSplit, SyncHorizontal: true}
	d.SetScrollPosition(DiffScrollPosition{OldX: 10, NewX: 80, Y: 0})
	tester := ui.NewTester(func(c *ui.Context) { DiffView(c, d).Fill() }, 800, 300)
	if p := d.ScrollPosition(); p.OldX != 80 || p.NewX != 80 {
		t.Fatalf("restore did not align columns: %+v", p)
	}
	d.Wrap = true
	tester.Frame()
	for _, ed := range d.eds {
		_, h := ed.scrollbars()
		if h || ed.scrollX != 0 {
			t.Fatal("wrap retained horizontal scroll")
		}
	}
	d.Wrap = false
	tester.Frame()
	d.SetScrollPosition(DiffScrollPosition{OldX: 40, NewX: 90})
	tester.Frame()
	d.Old.Reset("old")
	d.New.Reset("new")
	tester.Frame()
	tester.Frame()
	if p := d.ScrollPosition(); p.OldX != 0 || p.NewX != 0 {
		t.Fatalf("short content retained stale shared X: %+v", p)
	}
}
