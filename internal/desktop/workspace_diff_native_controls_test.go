package desktop

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"silverwing-go/internal/workspace"
)

func workspaceNativeControlsFixture(t *testing.T, width int, dark bool) (*workspaceHistoryState, *ui.Tester) {
	t.Helper()
	state := reviewRendererTestState(false)
	comparison := state.Preview.Comparison
	for _, index := range []int{20, 200} {
		row := &comparison.Rows[index]
		row.OldLine.Kind = workspace.PreviewDeleted
		row.NewLine.Kind = workspace.PreviewAdded
		row.NewLine.Text += " changed"
	}
	newLines := make([]string, len(comparison.Rows))
	for index, row := range comparison.Rows {
		newLines[index] = row.NewLine.Text
	}
	comparison.NewContent.Text = strings.Join(newLines, "\n")
	s := &app{}
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, dark)
		ui.Column(c).Fill().Padding(8).Children(func() {
			view := s.workspaceNativeDiffView(state, comparison)
			if view != nil {
				s.renderWorkspaceNativeComparison(c, p, state, view)
			}
		})
	}, width, 720)
	if state.NativeDiff == nil || state.NativeDiff.hunkCount() != 3 {
		t.Fatal("fixture must render three native changes")
	}
	return state, tester
}

func TestWorkspaceNativeContextModeActuallyCollapsesUnchangedLines(t *testing.T) {
	state, tester := workspaceNativeControlsFixture(t, 1100, false)
	state.NativeDiff.view.GoToHunk(1)
	tester.Frame()
	state.ContextLinesOnly = true
	tester.Frame()
	if state.NativeDiff.view.ExpandUnchanged || state.NativeDiff.view.ContextLines != 3 || !tester.HasText("行相同内容已折叠") {
		t.Fatalf("context mode did not collapse unchanged source with three context lines: expand=%v context=%d", state.NativeDiff.view.ExpandUnchanged, state.NativeDiff.view.ContextLines)
	}
	if hunk, _ := state.NativeDiff.view.CurrentHunk(); hunk != 1 {
		t.Fatalf("changing context mode lost the current source hunk: %d", hunk)
	}
	state.ContextLinesOnly = false
	tester.Frame()
	if !state.NativeDiff.view.ExpandUnchanged || tester.HasText("行相同内容已折叠") {
		t.Fatal("all-lines mode did not restore complete source")
	}
}

func TestWorkspaceNativeHunkControlsFollowCaretAndKeyboardWrap(t *testing.T) {
	state, tester := workspaceNativeControlsFixture(t, 1100, false)
	// Focus the new editor, then navigate with the SDK's real key binding.
	tester.ClickAt(850, 300)
	state.NativeDiff.view.GoToHunk(0)
	tester.Frame()
	tester.Key(ui.Alt, ui.KeyF5)
	if hunk, inside := state.NativeDiff.view.CurrentHunk(); hunk != 1 || !inside {
		t.Fatalf("Alt+F5 did not move the native caret: hunk=%d inside=%v", hunk, inside)
	}
	if state.DiffHunkIndex != 1 || !state.DiffHunkSelected || !tester.HasText("2 / 3") {
		t.Fatalf("counter ignored native keyboard navigation: index=%d selected=%v texts=%q", state.DiffHunkIndex, state.DiffHunkSelected, tester.Texts())
	}
	for _, want := range []int{2, 0} {
		if err := tester.Click("下一处"); err != nil {
			t.Fatal(err)
		}
		if hunk, inside := state.NativeDiff.view.CurrentHunk(); hunk != want || !inside || state.DiffHunkIndex != want {
			t.Fatalf("button and caret did not use the same wrapping navigation: caret=%d inside=%v index=%d want=%d", hunk, inside, state.DiffHunkIndex, want)
		}
	}
	if err := tester.Click("上一处"); err != nil {
		t.Fatal(err)
	}
	if hunk, inside := state.NativeDiff.view.CurrentHunk(); hunk != 2 || !inside || !tester.HasText("3 / 3") {
		t.Fatalf("previous button did not wrap with the native caret: hunk=%d inside=%v", hunk, inside)
	}
	state.NativeDiff.view.Run(editor.MoveDocStart)
	tester.Frame()
	if hunk, _ := state.NativeDiff.view.CurrentHunk(); hunk != -1 || state.DiffHunkSelected {
		t.Fatalf("source before the first change retained a selected hunk: caret=%d selected=%v", hunk, state.DiffHunkSelected)
	}
}

func TestWorkspaceNativeReadingCommandsAreVisibleAndFitNarrowWidths(t *testing.T) {
	for _, width := range []int{320, 540, 1100} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%v", width, dark), func(t *testing.T) {
				_, tester := workspaceNativeControlsFixture(t, width, dark)
				for _, action := range []string{"上一处", "下一处", "查找", "跳到行"} {
					bounds, found := tester.Find(action)
					if !found || bounds.W <= 0 || bounds.H <= 0 || bounds.X < 8 || bounds.X+bounds.W > float32(width-8)+.5 {
						t.Fatalf("native reading command %q is missing or overflows: %+v", action, bounds)
					}
				}
				for _, source := range []string{"主分支基线", "当前提交"} {
					bounds, found := tester.Find(source)
					if !found || bounds.W <= 0 || bounds.X < 8 || bounds.X+bounds.W > float32(width-8)+.5 {
						t.Fatalf("source label %q lost its available width: %+v", source, bounds)
					}
				}
			})
		}
	}
}

func TestWorkspaceNativeReadingButtonsUseSDKCommands(t *testing.T) {
	state, tester := workspaceNativeControlsFixture(t, 1100, false)
	oldText, newText := state.NativeDiff.oldText, state.NativeDiff.newText
	var findBar *editor.FindBar
	var goToLine *editor.GoToLineBar
	state.NativeDiff.view.RenderFindBar = func(c *ui.Context, bar ui.Element, find *editor.FindBar) {
		findBar = find
		editor.DefaultFindBar(c, bar, find)
	}
	state.NativeDiff.view.RenderGoToLine = func(c *ui.Context, bar ui.Element, target *editor.GoToLineBar) {
		goToLine = target
		editor.DefaultGoToLine(c, bar, target)
	}
	if err := tester.Click("查找"); err != nil {
		t.Fatal(err)
	}
	if findBar == nil || findBar.CanReplace() {
		t.Fatal("find button did not open the SDK's read-only find bar")
	}
	*findBar.Query() = "line_200"
	findBar.Next()
	tester.Frame()
	if state.DiffHunkIndex != 2 || !tester.HasText("3 / 3") {
		t.Fatal("SDK find did not update the current change")
	}
	findBar.Close()
	tester.Frame()
	if err := tester.Click("跳到行"); err != nil {
		t.Fatal(err)
	}
	if goToLine == nil || goToLine.LineCount() != 240 {
		t.Fatal("go-to-line button did not open the SDK's original source line selector")
	}
	*goToLine.Text() = "121"
	goToLine.Go()
	tester.Frame()
	if state.NativeDiff.view.CursorPos().Line != 120 || state.DiffHunkIndex != 1 {
		t.Fatal("SDK go-to-line did not select the original line and update the change counter")
	}
	if state.NativeDiff.view.Old.String() != oldText || state.NativeDiff.view.New.String() != newText {
		t.Fatal("native reading commands changed source text")
	}
}

func TestWorkspaceNativeStrictModeRoundTripKeepsHorizontalPositionsAndHunk(t *testing.T) {
	state := reviewRendererTestState(false)
	state.PreviewMode = workspace.PreviewComparison
	comparison := state.Preview.Comparison
	row := &comparison.Rows[200]
	row.OldLine.Kind, row.NewLine.Kind = workspace.PreviewDeleted, workspace.PreviewAdded
	row.NewLine.Text += " changed"
	lines := make([]string, len(comparison.Rows))
	for index, source := range comparison.Rows {
		lines[index] = source.NewLine.Text
	}
	comparison.NewContent.Text = strings.Join(lines, "\n")
	s := &app{}
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		ui.Column(c).Fill().Padding(8).Children(func() {
			s.renderWorkspaceFilePreview(c, p, state, state.Preview)
		})
	}, 1100, 720)
	if state.NativeDiff == nil || state.NativeDiff.hunkCount() != 2 {
		t.Fatal("fixture must start in native all-lines mode with two changes")
	}
	position := state.NativeDiff.view.ScrollPosition()
	position.OldX, position.NewX = 20, 70
	state.NativeDiff.view.SetScrollPosition(position)
	tester.Frame()
	if got := state.NativeDiff.view.ScrollPosition(); got.OldX != 70 || got.NewX != 70 {
		t.Fatalf("restored native positions did not synchronize: %+v", got)
	}
	if err := tester.Click("仅看改动"); err != nil {
		t.Fatal(err)
	}
	if state.NativeDiff != nil || state.DiffRows == nil || !state.DiffRows.changedOnly {
		t.Fatal("strict changed-lines mode did not activate its numbered row renderer")
	}
	if err := tester.Click("下一处"); err != nil {
		t.Fatal(err)
	}
	if state.DiffHunkIndex != 1 || !state.DiffHunkSelected {
		t.Fatal("strict mode did not select the second source change")
	}
	if err := tester.Click("查看全部"); err != nil {
		t.Fatal(err)
	}
	if state.NativeDiff == nil {
		t.Fatal("all-lines mode did not restore the native renderer")
	}
	if hunk, inside := state.NativeDiff.view.CurrentHunk(); hunk != 1 || !inside || state.NativeDiff.view.CursorPos().Line != 200 {
		t.Fatalf("native round trip lost the source hunk selected in strict mode: hunk=%d inside=%v cursor=%+v", hunk, inside, state.NativeDiff.view.CursorPos())
	}
	if got := state.NativeDiff.view.ScrollPosition(); got.OldX != 70 || got.NewX != 70 {
		t.Fatalf("strict mode round trip lost synchronized native horizontal positions: %+v", got)
	}
}
