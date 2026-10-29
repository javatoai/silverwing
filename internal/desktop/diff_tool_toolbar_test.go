package desktop

import (
	"context"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/tools"
)

func diffToolbarFixture(t *testing.T, width int, dark bool) (*app, *toolPageState, *ui.Tester) {
	t.Helper()
	state := newToolPageState("")
	state.activeTab, state.diffAuto = "文本对比", false
	state.diffLeft, state.diffRight = "old-a\nkeep-a\nkeep-b\nold-b", "new-a\nkeep-a\nkeep-b\nnew-b"
	state.diffOptions.ContextLines = 0
	result, err := tools.CompareText(context.Background(), state.diffLeft, state.diffRight, state.diffOptions)
	if err != nil {
		t.Fatal(err)
	}
	state.diffResult, state.diffHasData = result, true
	t.Cleanup(state.cancelWork)
	s := &app{
		toolsUnit: toolsController{state: toolsState{tools: state}},
		page:      "tools",
	}
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, dark)
		ui.Column(c).Fill().Padding(12).Background(p.bg).Children(func() { s.renderDiffTool(c, p, state) })
	}, width, 700)
	return s, state, tester
}

func TestDiffToolbarOneRowAcrossWidthsAndThemes(t *testing.T) {
	for _, width := range []int{360, 640, 960, 1440} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				_, state, tester := diffToolbarFixture(t, width, dark)
				for _, status := range []string{"result", "pending", "error"} {
					state.diffPending = status == "pending"
					if status == "error" {
						state.diffError = strings.Repeat("这是一条很长的比较错误说明", 20)
					}
					tester.Frame()
					toolbar, found := tester.Find("文本对比操作栏")
					if !found || toolbar.H != 34 {
						t.Fatalf("toolbar gained an extra row: %+v", toolbar)
					}
					lastRight := toolbar.X
					for _, name := range []string{"比较", "上一处", "差异位置", "下一处", "复制差异", "比较选项"} {
						control, ok := tester.Find(name)
						if !ok || control.X < lastRight || control.X+control.W > toolbar.X+toolbar.W+.5 || control.Y < toolbar.Y || control.Y+control.H > toolbar.Y+toolbar.H+.5 {
							t.Fatalf("%s %q clipped/wrapped/overlapped: control=%+v toolbar=%+v previous=%v", status, name, control, toolbar, lastRight)
						}
						lastRight = control.X + control.W
					}
					left, _ := tester.Find("左侧文本输入")
					right, _ := tester.Find("右侧文本输入")
					if left.Y > toolbar.Y+80 || math.Abs(float64(left.Y-right.Y)) > .5 {
						t.Fatalf("second permanent toolbar/status row remains: toolbar=%+v left=%+v right=%+v", toolbar, left, right)
					}
				}
				if width < 760 && tester.HasText("自动比较") || width < 1280 && tester.HasText("忽略大小写") {
					t.Fatal("narrow toolbar did not move lower-frequency options into the native menu")
				}
			})
		}
	}
}

type diffToolbarCaptureBackend struct{ actions chan application.Action }

func (b *diffToolbarCaptureBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, nil
}
func (b *diffToolbarCaptureBackend) Dispatch(_ context.Context, action application.Action) (application.ActionResult, error) {
	b.actions <- action
	return application.ActionResult{}, nil
}

func TestDiffToolbarNativeActionsAndMenuState(t *testing.T) {
	s, state, tester := diffToolbarFixture(t, 640, false)
	click := func(text string) {
		t.Helper()
		if err := tester.Click(text); err != nil {
			t.Fatal(err)
		}
	}
	if len(state.diffResult.Hunks) != 2 {
		t.Fatalf("fixture requires two separated hunks: %+v", state.diffResult.Hunks)
	}
	click("下一处")
	if state.diffActiveHunk != 1 {
		t.Fatal("next action no longer routes through the existing hunk navigator")
	}
	click("上一处")
	if state.diffActiveHunk != 0 {
		t.Fatal("previous action no longer routes through the existing hunk navigator")
	}
	click("复制差异")
	if tester.Clipboard() != state.diffResult.DifferencesText() {
		t.Fatal("copy action changed its actual exported difference content")
	}
	click("比较选项")
	for _, name := range []string{"交换左右", "自动换行", "自动比较", "忽略大小写", "忽略行首尾空白", "忽略空行", "折叠相同行"} {
		if !tester.HasText(name) {
			t.Fatalf("narrow native menu lost %q", name)
		}
	}
	before := state.diffRevision
	click("忽略大小写")
	if !state.diffOptions.IgnoreCase || state.diffRevision != before+1 || !tester.HasText("比较选项 · 已启用：忽略大小写") {
		t.Fatal("hidden rule lost its existing recomparison route or active-rule tooltip")
	}
	click("比较选项 · 已启用：忽略大小写")
	click("忽略大小写")
	click("比较选项")
	wrapped := state.diffWrap
	click("自动换行")
	if state.diffWrap == wrapped || state.diffInputHistory[0].view.SoftWrap != state.diffWrap || state.diffInputHistory[1].view.SoftWrap != state.diffWrap {
		t.Fatal("wrap menu did not change both existing native editors")
	}
	click("比较选项")
	click("自动比较")
	if !state.diffAuto {
		t.Fatal("automatic comparison menu no longer enables the original state")
	}
	click("比较选项")
	click("自动比较")
	if state.diffAuto {
		t.Fatal("automatic comparison checked state did not toggle back")
	}
	click("比较选项")
	collapsed, revision := state.diffCollapseEqual, state.diffRevision
	click("折叠相同行")
	if state.diffCollapseEqual == collapsed || state.diffRevision != revision {
		t.Fatal("fold-only menu action changed the existing compare semantics")
	}
	click("比较选项")
	oldLeft, oldRight := state.diffLeft, state.diffRight
	click("交换左右")
	if state.diffLeft != oldRight || state.diffRight != oldLeft {
		t.Fatal("swap menu changed the original left/right input route")
	}
	backend := &diffToolbarCaptureBackend{actions: make(chan application.Action, 1)}
	s.backend = backend
	t.Cleanup(func() { s.cancelDeveloperToolJob("tool.diff") })
	click("比较")
	select {
	case action := <-backend.actions:
		if action.Kind != "tool.diff" || payloadString(action.Payload, "left") != oldRight || payloadString(action.Payload, "right") != oldLeft || action.Payload["options"] != state.diffOptions {
			t.Fatalf("compare bypassed the existing primary action/payload: %+v", action)
		}
	case <-time.After(time.Second):
		t.Fatal("compare did not dispatch to the memory backend")
	}
}
