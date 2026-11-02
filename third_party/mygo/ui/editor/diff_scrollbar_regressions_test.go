package editor

import (
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSplitDiffHorizontalScrollbarsRemainIndependent(t *testing.T) {
	old := "old long line " + strings.Repeat("x", 130) + strings.Repeat("\n", 60)
	current := "new long line " + strings.Repeat("y", 160) + strings.Repeat("\n", 60)
	d := &DiffState{Old: NewBuffer(old), New: NewBuffer(current), Layout: DiffSplit, SplitRatio: .4, ExpandUnchanged: true}
	var scrollbarColor ui.Color
	tester := ui.NewTester(func(c *ui.Context) { scrollbarColor = c.Theme().Scrollbar; DiffView(c, d).Fill() }, 900, 320)
	tester.Frame()
	for i := range d.eds {
		vertical, horizontal := d.eds[i].scrollbars()
		if !horizontal || vertical != (i == 1) {
			t.Fatalf("pane %d scrollbars vertical=%t horizontal=%t", i, vertical, horizontal)
		}
		// The bottom rows are blank: sample the actual painted thumb against
		// the neighboring track, independently of visibility/hit-test flags.
		ed := &d.eds[i]
		a, z := ed.thumb(true)
		y := int(ed.r.Y + ed.viewH - scrollbarWidth/2)
		frame := tester.Image()
		background := frame.RGBAAt(int(ed.r.X+ed.textX+z+10), y)
		want := scrollbarColor.Alpha(.4).Over(ui.RGBA(background.R, background.G, background.B, 1))
		got := frame.RGBAAt(int(ed.r.X+ed.textX+(a+z)/2), y)
		near := func(a, b uint8) bool { return max(a, b)-min(a, b) <= 1 }
		if !near(got.R, want.R) || !near(got.G, want.G) || !near(got.B, want.B) || got.A != 255 {
			t.Fatalf("pane %d thumb was not painted: got=%v want=%v", i, got, want)
		}
	}
	if dir := os.Getenv("SILVERWING_DIFF_SCROLLBAR_CAPTURE_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(filepath.Join(dir, "split-diff-both-horizontal-bars.png"))
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(file, tester.Image())
		closeErr := file.Close()
		if err != nil {
			t.Fatal(err)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
	}
	for side := range d.eds {
		ed := &d.eds[side]
		before := d.ScrollPosition()
		a, z := ed.thumb(true)
		x, y := ed.r.X+ed.textX+(a+z)/2, ed.r.Y+ed.viewH-scrollbarWidth/2
		tester.Move(x, y)
		if tester.Cursor() != ui.CursorDefault {
			t.Fatalf("pane %d scrollbar hit-test uses a text cursor", side)
		}
		tester.Press(x, y)
		tester.Move(x+100, y)
		tester.Release(x+100, y)
		after := d.ScrollPosition()
		if side == 0 && (after.OldX <= before.OldX || after.NewX != before.NewX) ||
			side == 1 && (after.NewX <= before.NewX || after.OldX != before.OldX) || after.Y != before.Y {
			t.Fatalf("pane %d drag did not stay horizontal and independent: before=%+v after=%+v", side, before, after)
		}
	}
	before := d.ScrollPosition()
	tester.Scroll(d.eds[0].r.X+100, 100, 45, 60)
	after := d.ScrollPosition()
	if after.OldX <= before.OldX || after.NewX != before.NewX || after.Y <= before.Y || d.eds[0].scrollY != d.eds[1].scrollY {
		t.Fatalf("wheel lost independent X/shared Y: before=%+v after=%+v", before, after)
	}
	if d.Old.String() != old || d.New.String() != current {
		t.Fatal("scrollbar gestures changed compared content")
	}
}

func TestSplitDiffHorizontalScrollbarVisibilityTracksEachPane(t *testing.T) {
	long := strings.Repeat("long ", 150)
	for _, test := range []struct {
		name, old, current        string
		wrap, fit, oldBar, newBar bool
	}{
		{"short", "old", "new", false, false, false, false},
		{"old-only", long, "new", false, false, true, false},
		{"new-only", "old", long, false, false, false, true},
		{"wrapped", long, long + " changed", true, false, false, false},
		{"fit-content", long, long + " changed", false, true, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			d := &DiffState{Old: NewBuffer(test.old), New: NewBuffer(test.current), Layout: DiffSplit, Wrap: test.wrap, FitContent: test.fit}
			tester := ui.NewTester(func(c *ui.Context) { DiffView(c, d).FillWidth().Height(300) }, 800, 300)
			tester.Frame()
			for side, want := range []bool{test.oldBar, test.newBar} {
				_, got := d.eds[side].scrollbars()
				if got != want {
					t.Fatalf("pane %d horizontal=%t want %t", side, got, want)
				}
			}
		})
	}
}
