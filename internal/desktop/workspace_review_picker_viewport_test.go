package desktop

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func workspaceReviewPickerViewportFixture(t *testing.T, count, width, height int) (*workspaceHistoryState, *ui.Tester) {
	t.Helper()
	item := application.Workspace{ServiceID: "service", ModuleID: "module", Path: `C:\synthetic\repo`}
	state := &workspaceHistoryState{
		TaskID: "task", ServiceID: item.ServiceID, ModuleID: item.ModuleID, Path: item.Path,
		Mode: "review", Open: true, CompareMode: workspaceReviewCommits, CommitsLoaded: true,
	}
	for index := 0; index < count; index++ {
		state.Commits = append(state.Commits, domain.WorkspaceGitCommit{
			FullHash: fmt.Sprintf("%040x", index+1), ShortHash: fmt.Sprintf("%07x", index+1), Message: fmt.Sprintf("picker-row-%05d", index),
		})
	}
	s := &app{
		taskUnit:      taskController{state: taskState{tasks: []task{{id: "task", path: `C:\synthetic\task`, workspaces: []application.Workspace{item}}}}},
		workspaceUnit: workspaceController{state: workspaceState{workspaceHistory: state}},
		page:          "tasks",
	}
	t.Cleanup(func() { s.cancelWorkspaceReviewDebounce(state) })
	tester := ui.NewTester(func(c *ui.Context) { s.renderWorkspaceReviewPage(c, setTheme(c, false)) }, width, height)
	return state, tester
}

func workspaceReviewPickerVisibleRect(t *testing.T, tester *ui.Tester, label string) ui.Rect {
	t.Helper()
	rect, found := tester.Find(label)
	if !found || rect.W <= 0 || rect.H <= 0 {
		t.Fatalf("picker element is not actually visible: %s found=%t rect=%+v", label, found, rect)
	}
	return rect
}

func workspaceReviewPickerCheckViewport(t *testing.T, tester *ui.Tester, width, height int) ui.Rect {
	t.Helper()
	panel := workspaceReviewPickerVisibleRect(t, tester, "提交范围选择器")
	list := workspaceReviewPickerVisibleRect(t, tester, "提交范围列表")
	done := workspaceReviewPickerVisibleRect(t, tester, "完成")
	if panel.X < 0 || panel.Y < 0 || panel.X+panel.W > float32(width)+0.5 || panel.Y+panel.H > float32(height)+0.5 ||
		panel.W > 560.5 || panel.H > 510.5 || panel.H > float32(height)-23.5 {
		t.Fatalf("picker exceeded its bounded window viewport: panel=%+v window=%dx%d", panel, width, height)
	}
	if list.X < panel.X || list.Y < panel.Y || list.X+list.W > panel.X+panel.W+0.5 || list.Y+list.H > panel.Y+panel.H+0.5 ||
		done.Y+done.H > panel.Y+panel.H+0.5 || done.Y < list.Y+list.H {
		t.Fatalf("list or Done escaped the picker panel: panel=%+v list=%+v done=%+v", panel, list, done)
	}
	return list
}

func TestWorkspaceReviewPickerLargeHistoryHasBoundedViewportAndRows(t *testing.T) {
	state, tester := workspaceReviewPickerViewportFixture(t, 10000, 1100, 760)
	state.HistorySummaries, state.HistoryHasMore = true, true
	if err := tester.Click("提交范围：选择提交"); err != nil {
		t.Fatal(err)
	}
	workspaceReviewPickerCheckViewport(t, tester, 1100, 760)
	rows := 0
	for _, text := range tester.Texts() {
		if strings.HasPrefix(text, "picker-row-") {
			rows++
		}
	}
	if rows == 0 || rows >= 64 || !tester.HasText("还有更多提交") || !tester.HasText("加载更多") {
		t.Fatalf("large picker did not retain virtualization/progressive loading: built=%d", rows)
	}
	// Scrolling is required only for distant rows, never to repair first-open
	// layout. List owns the reveal, keeping its measured variable-height rows.
	state.CommitPickerList.ScrollToEnd()
	tester.Frame()
	last := state.Commits[len(state.Commits)-1]
	label := last.ShortHash + " " + last.Message
	workspaceReviewPickerVisibleRect(t, tester, label)
	if !state.CommitPickerList.AtEnd() {
		t.Fatal("the last loaded commit cannot be reached by scrolling")
	}
	if err := tester.Click(label); err != nil {
		t.Fatal(err)
	}
	if !state.CommitPickerOpen || len(state.CommitHashes) != 1 || state.CommitHashes[0] != last.FullHash {
		t.Fatal("a revealed distant commit did not enter the selection")
	}
	workspaceReviewPickerCheckViewport(t, tester, 1100, 760)
}

func TestWorkspaceReviewPickerSmallWindowStaysInsideViewport(t *testing.T) {
	for _, size := range [][2]int{{420, 260}, {640, 360}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			state, tester := workspaceReviewPickerViewportFixture(t, 100, size[0], size[1])
			if err := tester.Click("提交范围：选择提交"); err != nil {
				t.Fatal(err)
			}
			list := workspaceReviewPickerCheckViewport(t, tester, size[0], size[1])
			first := state.Commits[0]
			label := first.ShortHash + " " + first.Message
			workspaceReviewPickerVisibleRect(t, tester, label)
			if err := tester.Click(label); err != nil {
				t.Fatal(err)
			}
			if len(state.CommitHashes) != 1 || state.CommitHashes[0] != first.FullHash {
				t.Fatal("the first visible commit in a small window was not clickable")
			}
			tester.Scroll(list.X+list.W/2, list.Y+list.H/2, 0, 300)
			firstVisible, _ := state.CommitPickerList.Visible()
			if firstVisible == 0 {
				t.Fatal("the bounded small-window list did not scroll")
			}
			workspaceReviewPickerCheckViewport(t, tester, size[0], size[1])
			if err := tester.Click("完成"); err != nil {
				t.Fatal(err)
			}
			if state.CommitPickerOpen || !state.Open {
				t.Fatal("small-window Done did not close only the picker")
			}
		})
	}
}

func TestWorkspaceReviewPickerExpandedRowsKeepScrollableViewport(t *testing.T) {
	state, tester := workspaceReviewPickerViewportFixture(t, 50, 1100, 760)
	state.Commits[0].Message = "expanded commit\n" + strings.Repeat("detail line\n", 20)
	if err := tester.Click("提交范围：选择提交"); err != nil {
		t.Fatal(err)
	}
	list := workspaceReviewPickerCheckViewport(t, tester, 1100, 760)
	first := state.Commits[0]
	label := first.ShortHash + " " + strings.TrimSpace(first.Message)
	if err := tester.Click("展开"); err != nil {
		t.Fatal(err)
	}
	expanded := workspaceReviewPickerVisibleRect(t, tester, label)
	if !state.ExpandedCommits[first.FullHash] || expanded.H <= workspaceReviewCommitRowHeight || !tester.HasText("detail line") {
		t.Fatal("expanded commit did not acquire its measured variable row height")
	}
	if current := workspaceReviewPickerCheckViewport(t, tester, 1100, 760); current.H != list.H {
		t.Fatal("expanding a row changed the bounded viewport instead of its scrollable content")
	}
	tester.Scroll(list.X+list.W/2, list.Y+list.H/2, 0, 900)
	firstVisible, _ := state.CommitPickerList.Visible()
	if firstVisible == 0 {
		t.Fatal("the expanded row prevented wheel scrolling to later commits")
	}
	state.CommitPickerList.ScrollToEnd()
	tester.Frame()
	last := state.Commits[len(state.Commits)-1]
	workspaceReviewPickerVisibleRect(t, tester, last.ShortHash+" "+last.Message)
	state.CommitPickerList.ScrollTo(0, ui.Start)
	tester.Frame()
	if err := tester.Click("收起"); err != nil {
		t.Fatal(err)
	}
	if state.ExpandedCommits[first.FullHash] {
		t.Fatal("returning to an expanded row lost its collapse control")
	}
	workspaceReviewPickerCheckViewport(t, tester, 1100, 760)
}
