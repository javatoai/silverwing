package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func TestWorkspaceReviewPickerStaysOpenAndRestoresFocus(t *testing.T) {
	item := application.Workspace{ServiceID: "service", ModuleID: "module", Path: `C:\synthetic\repo`}
	state := &workspaceHistoryState{
		TaskID: "task", ServiceID: item.ServiceID, ModuleID: item.ModuleID, Path: item.Path,
		Mode: "review", Open: true, CompareMode: workspaceReviewCommits, CommitsLoaded: true,
		Commits: []domain.WorkspaceGitCommit{
			{FullHash: "c", ShortHash: "c", Message: "latest", ParentHashes: []string{"b"}},
			{FullHash: "b", ShortHash: "b", Message: "middle", ParentHashes: []string{"a"}},
			{FullHash: "a", ShortHash: "a", Message: "first"},
		},
	}
	s := &app{page: "tasks", workspaceHistory: state, tasks: []task{{id: "task", path: `C:\synthetic\task`, workspaces: []application.Workspace{item}}}}
	t.Cleanup(func() { s.cancelWorkspaceReviewDebounce(state) })
	tester := ui.NewTester(func(c *ui.Context) { s.renderWorkspaceReviewPage(c, setTheme(c, false)) }, 1100, 760)
	click := func(label string) {
		t.Helper()
		if err := tester.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	click("提交范围：选择提交")
	click("a first")
	click("c latest")
	if !state.CommitPickerOpen || state.StartCommit != "a" || state.EndCommit != "c" || len(state.CommitHashes) != 3 {
		t.Fatalf("picker did not keep preview selection A..C open: %#v", state.CommitHashes)
	}
	click("b middle")
	if !state.CommitPickerOpen || state.StartCommit != "b" || state.EndCommit != "b" || len(state.CommitHashes) != 1 {
		t.Fatal("clicking an interior commit did not collapse the range to that commit")
	}
	click("完成")
	if state.CommitPickerOpen || !state.Open || !tester.Focused("提交范围：提交 b") {
		t.Fatal("Done did not close only the picker and return focus to its trigger")
	}
	click("提交范围：提交 b")
	tester.Key(0, ui.KeyEscape)
	if state.CommitPickerOpen || !state.Open || !tester.Focused("提交范围：提交 b") {
		t.Fatal("Escape closed the review or did not restore picker trigger focus")
	}
	click("提交范围：提交 b")
	tester.ClickAt(1080, 730)
	if state.CommitPickerOpen || !state.Open {
		t.Fatal("outside click did not close only the picker")
	}
}
