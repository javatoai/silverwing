package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func TestDiffToolSplitRatioCanBeAdjustedAndSurvivesResize(t *testing.T) {
	state := newToolPageState("")
	state.diffAuto = false
	state.diffWrap = false
	longLine := strings.Repeat("long-line-", 240)
	state.diffLeft = strings.Repeat(longLine+"\n", 240)
	state.diffRight = strings.Repeat(longLine+"\n", 240)
	s := &app{
		toolsUnit: toolsController{state: toolsState{tools: state}},
		page:      "tools",
	}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(12).Children(func() {
			s.renderDiffTool(c, setTheme(c, false), state)
		})
	}, 1200, 720)

	left, leftOK := tester.Find("左侧文本输入")
	right, rightOK := tester.Find("右侧文本输入")
	if !leftOK || !rightOK {
		t.Fatalf("text diff panes not rendered: left=%v right=%v", leftOK, rightOK)
	}
	dividerX := left.X + left.W + (right.X-left.X-left.W)/2
	tester.ClickAt(dividerX, left.Y+left.H/2)
	tester.Key(0, ui.KeyRight)
	tester.Frame()
	if state.diffSplitRatio <= .5 {
		t.Fatalf("splitter keyboard adjustment did not increase ratio: %v", state.diffSplitRatio)
	}

	before := state.diffSplitRatio
	tester.SetSize(1400, 720)
	left, leftOK = tester.Find("左侧文本输入")
	right, rightOK = tester.Find("右侧文本输入")
	if !leftOK || !rightOK {
		t.Fatal("text diff panes disappeared after resize")
	}
	actual := left.W / (left.W + right.W)
	if delta := actual - before; delta < -.02 || delta > .02 {
		t.Fatalf("split ratio did not survive resize: got %v want %v", actual, before)
	}

	state.diffInputHistory[0].view.SetScrollPosition(editor.ScrollPosition{X: 30, Y: 120})
	state.diffInputHistory[1].view.SetScrollPosition(editor.ScrollPosition{X: 70, Y: 0})
	tester.Frame()
	leftPosition := state.diffInputHistory[0].view.ScrollPosition()
	rightPosition := state.diffInputHistory[1].view.ScrollPosition()
	if delta := leftPosition.Y - rightPosition.Y; delta < -0.5 || delta > 0.5 {
		t.Fatalf("vertical scroll positions are not synchronized: left=%v right=%v", leftPosition.Y, rightPosition.Y)
	}
	if leftPosition.X != 30 || rightPosition.X != 70 {
		t.Fatalf("horizontal scroll positions should remain independent: left=%v right=%v", leftPosition.X, rightPosition.X)
	}
}
