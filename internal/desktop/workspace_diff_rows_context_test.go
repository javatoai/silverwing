package desktop

import (
	"math"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestWorkspaceRowsContextProjectionRetainsThreeLinesAndCountedGaps(t *testing.T) {
	state := workspaceRowsControlsFixture(60, []int{12, 45}, "\r\n", 0)
	state.ContextLinesOnly = true
	tester := workspaceRowsControlsTester(state, 1100, 650)
	want := []int{9, 10, 11, 12, 13, 14, 15, 42, 43, 44, 45, 46, 47, 48}
	got, gaps := workspaceRowsVisibleSources(state.DiffRows)
	if !slices.Equal(got, want) || gaps != 3 {
		t.Fatalf("context projection must retain three unchanged rows around each change, got sources=%v gaps=%d", got, gaps)
	}
	for _, text := range []string{"已折叠 9 行", "已折叠 26 行", "已折叠 11 行", "向上展开", "向下展开", "全部展开"} {
		if _, found := tester.Find(text); !found {
			t.Fatalf("context gap lost %q: %v", text, tester.Texts())
		}
	}
	state.ChangedLinesOnly = true
	tester.Frame()
	got, gaps = workspaceRowsVisibleSources(state.DiffRows)
	if !slices.Equal(got, []int{12, 45}) || gaps != 0 {
		t.Fatal("strict changed-line filtering must take precedence over context folding")
	}
}

func TestWorkspaceRowsContextMergesWindowsWithoutMergingHunks(t *testing.T) {
	state := workspaceRowsControlsFixture(28, []int{8, 14}, "\r\n", 0)
	state.ContextLinesOnly = true
	tester := workspaceRowsControlsTester(state, 1050, 600)
	want := []int{5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17}
	got, gaps := workspaceRowsVisibleSources(state.DiffRows)
	if !slices.Equal(got, want) || gaps != 2 {
		t.Fatalf("touching context windows must form one visible region, got sources=%v gaps=%d", got, gaps)
	}
	if !tester.HasText("1 / 2") {
		t.Fatal("context window coalescing incorrectly combined distinct source change blocks")
	}
}

func TestWorkspaceRowsContextGapSupportsDirectionalAndFullExpansion(t *testing.T) {
	for _, action := range []string{"向下展开", "向上展开", "全部展开"} {
		t.Run(action, func(t *testing.T) {
			state := workspaceRowsControlsFixture(180, []int{70, 140}, "\r\n", 0)
			state.ContextLinesOnly = true
			tester := workspaceRowsControlsTester(state, 1100, 650)
			before, _ := workspaceRowsVisibleSources(state.DiffRows)
			if slices.Contains(before, 30) {
				t.Fatal("leading context must start folded")
			}
			if err := tester.Click(action); err != nil {
				t.Fatal(err)
			}
			after, _ := workspaceRowsVisibleSources(state.DiffRows)
			switch action {
			case "向下展开":
				if !slices.Contains(after, 0) || slices.Contains(after, 66) {
					t.Fatal("downward expansion must reveal the top edge of the selected gap")
				}
			case "向上展开":
				if !slices.Contains(after, 66) || slices.Contains(after, 0) {
					t.Fatal("upward expansion must reveal the bottom edge of the selected gap")
				}
			case "全部展开":
				for source := 0; source < 67; source++ {
					if !slices.Contains(after, source) {
						t.Fatalf("whole-gap expansion omitted source row %d", source)
					}
				}
			}
			if !state.ContextLinesOnly || state.ChangedLinesOnly {
				t.Fatal("expanding a gap changed projection preferences")
			}
		})
	}
}

func TestWorkspaceRowsContextGotoRevealsOriginalLine(t *testing.T) {
	state := workspaceRowsControlsFixture(180, []int{70, 140}, "\r\n", 0)
	state.ContextLinesOnly = true
	tester := workspaceRowsControlsTester(state, 1050, 540)
	if err := tester.Click("跳到行"); err != nil {
		t.Fatal(err)
	}
	tester.Type("31")
	tester.Key(0, ui.KeyEnter)
	sources, _ := workspaceRowsVisibleSources(state.DiffRows)
	if !slices.Contains(sources, 30) || !state.ContextLinesOnly || state.ChangedLinesOnly {
		t.Fatal("go-to-original-line should reveal the requested collapsed row without changing the mode")
	}
	if _, found := tester.Find(state.Preview.Comparison.Rows[30].NewLine.Text); !found {
		t.Fatal("the original line was expanded but not brought into the viewport")
	}
}

func TestWorkspaceRowsProjectionSwitchKeepsSourceAnchorAndSynchronizedX(t *testing.T) {
	state := workspaceRowsControlsFixture(180, []int{70, 140, 150, 160, 170}, "\r\n", 180)
	tester := workspaceRowsControlsTester(state, 1050, 420)
	// Position a source row immediately before the second block at the top.
	state.DiffRows.oldScroll.Y = 138 * workspaceDiffRowHeight
	state.DiffRows.newScroll.Y = state.DiffRows.oldScroll.Y
	state.DiffRows.lastSharedY = state.DiffRows.oldScroll.Y
	state.DiffRows.oldScroll.X, state.DiffRows.newScroll.X = 70, 70
	tester.Frame()
	state.ContextLinesOnly = true
	tester.Frame()
	view := state.DiffRows
	displayIndex := int(view.newScroll.Y / workspaceDiffRowHeight)
	if displayIndex >= len(view.rows) || view.rows[displayIndex].sourceIndex != 138 {
		t.Fatalf("folding did not preserve the visible original source row: display=%d y=%v", displayIndex, view.newScroll.Y)
	}
	if view.oldScroll.X != 70 || view.newScroll.X != 70 {
		t.Fatal("projection switch lost synchronized horizontal positions")
	}
	state.ContextLinesOnly = false
	tester.Frame()
	if got := int(state.DiffRows.newScroll.Y / workspaceDiffRowHeight); got != 138 {
		t.Fatalf("unfolding moved the original source anchor to %d", got)
	}
}

func TestWorkspaceRowsExpandedContextRestoresSourceAfterFileRoundTrip(t *testing.T) {
	state := workspaceRowsControlsFixture(180, []int{70, 140}, "\r\n", 180)
	state.ContextLinesOnly = true
	firstPreview, firstPath := state.Preview, state.SelectedPath
	secondPreview := workspaceRowsControlsFixture(180, []int{70, 140}, "\r\n", 180).Preview
	tester := workspaceRowsControlsTester(state, 1050, 500)
	if err := tester.Click("全部展开"); err != nil {
		t.Fatal(err)
	}
	if err := tester.Click("跳到行"); err != nil {
		t.Fatal(err)
	}
	tester.Type("31")
	tester.Key(0, ui.KeyEnter)
	state.DiffRows.oldScroll.X, state.DiffRows.newScroll.X = 70, 70
	tester.Frame()
	anchor, fraction := state.DiffRows.sourceAnchor()
	if _, found := tester.Find(firstPreview.Comparison.Rows[30].NewLine.Text); !found {
		t.Fatal("expanded context fixture did not reveal original line 31 before leaving")
	}
	state.saveActiveWorkspaceDiffRenderer()
	state.SelectedPath, state.Preview = "second.java", secondPreview
	tester.Frame()
	state.SelectedPath, state.Preview = firstPath, firstPreview
	tester.Frame()
	view := state.DiffRows
	if _, found := tester.Find(firstPreview.Comparison.Rows[30].NewLine.Text); !found {
		t.Fatal("returning to an expanded file hid original line 31 again")
	}
	restoredAnchor, restoredFraction := view.sourceAnchor()
	if restoredAnchor != anchor || math.Abs(float64(restoredFraction-fraction)) > 1 {
		t.Fatalf("file round trip changed original source anchor: before=%d+%v after=%d+%v", anchor, fraction, restoredAnchor, restoredFraction)
	}
	if view.oldScroll.X != 70 || view.newScroll.X != 70 {
		t.Fatalf("file round trip changed synchronized horizontal positions: old=%v new=%v", view.oldScroll.X, view.newScroll.X)
	}
}

func TestWorkspaceRowsSavedExpansionClampsWhenSourcesShrink(t *testing.T) {
	state := workspaceRowsControlsFixture(180, []int{70, 140}, "\r\n", 0)
	state.ContextLinesOnly = true
	tester := workspaceRowsControlsTester(state, 1050, 500)
	if err := tester.Click("全部展开"); err != nil {
		t.Fatal(err)
	}
	state.saveActiveWorkspaceDiffRenderer()
	state.Preview = workspaceRowsControlsFixture(8, []int{4}, "\r\n", 0).Preview
	tester.Frame()
	view := state.DiffRows
	for _, expanded := range view.expanded {
		if expanded.start < 0 || expanded.start >= expanded.end || expanded.end > len(view.sourceRows) {
			t.Fatalf("saved expansion escaped the new source bounds: %+v", expanded)
		}
	}
	if sources, gaps := workspaceRowsVisibleSources(view); len(sources) != 8 || gaps != 0 {
		t.Fatal("a valid saved expansion did not survive source truncation")
	}
}
