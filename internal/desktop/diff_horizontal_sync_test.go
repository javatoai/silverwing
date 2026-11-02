package desktop

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestWorkspaceNativeAndRowsHorizontalWheelSync(t *testing.T) {
	for _, fallback := range []bool{false, true} {
		state := reviewRendererTestState(fallback)
		state.SplitRatio = .36
		s := &app{}
		tester := ui.NewTester(func(c *ui.Context) {
			ui.Column(c).Fill().Children(func() { s.renderWorkspaceFilePreview(c, setTheme(c, false), state, state.Preview) })
		}, 1000, 480)
		if !fallback {
			if state.NativeDiff == nil || !state.NativeDiff.view.SyncHorizontal {
				t.Fatal("workspace native viewer did not opt into synchronized columns")
			}
			for _, x := range []float32{150, 700} {
				tester.Scroll(x, 260, 65, 0)
				position := state.NativeDiff.view.ScrollPosition()
				if position.OldX <= 0 || position.OldX != position.NewX {
					t.Fatalf("native side wheel drifted: %+v", position)
				}
			}
			captureDesktopTestFrame(t, tester, "diff-synchronized-native")
			continue
		}
		if state.DiffRows == nil {
			t.Fatal("fallback row renderer missing")
		}
		for _, x := range []float32{150, 700} {
			tester.Scroll(x, 260, 65, 0)
			tester.Frame()
			view := state.DiffRows
			if view.oldScroll.X <= 0 || math.Abs(float64(view.oldScroll.X-view.newScroll.X)) > .5 {
				t.Fatalf("row side wheel drifted: %v/%v", view.oldScroll.X, view.newScroll.X)
			}
		}
		if math.Abs(float64(state.DiffRows.oldScroll.MaxX-state.DiffRows.newScroll.MaxX)) > 1 {
			t.Fatalf("unequal row viewports expose different tails %v/%v", state.DiffRows.oldScroll.MaxX, state.DiffRows.newScroll.MaxX)
		}
		captureDesktopTestFrame(t, tester, fmt.Sprintf("diff-synchronized-fallback-%t", fallback))
	}
}

func TestDiffToolHorizontalWheelSyncIncludesShortPane(t *testing.T) {
	state := newToolPageState("")
	state.diffAuto, state.diffWrap = false, false
	state.diffLeft = "short\n" + strings.Repeat("\n", 70)
	state.diffRight = strings.Repeat("long code ", 180) + strings.Repeat("\n", 70)
	s := &app{page: "tools", toolsUnit: toolsController{state: toolsState{tools: state}}}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { s.renderDiffTool(c, setTheme(c, false), state) })
	}, 1000, 600)
	left, ok := tester.Find("左侧文本输入")
	if !ok {
		t.Fatal("left input missing")
	}
	tester.Scroll(left.X+left.W/2, left.Y+left.H/2, 120, 0)
	tester.Frame()
	a, b := state.diffInputHistory[0].view.ScrollPosition(), state.diffInputHistory[1].view.ScrollPosition()
	if a.X <= 0 || a.X != b.X {
		t.Fatalf("short input cannot drive common horizontal extent: %+v/%+v", a, b)
	}
	if state.diffLeft != "short\n"+strings.Repeat("\n", 70) || state.diffRight != strings.Repeat("long code ", 180)+strings.Repeat("\n", 70) {
		t.Fatal("sync changed editable text")
	}
}
