package desktop

import (
	"fmt"
	"strings"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo/ui"
)

func TestWorkspaceHistoryPageAppendRejectsWrongOffsetSHAAndDuplicates(t *testing.T) {
	head, baseline := strings.Repeat("a", 40), strings.Repeat("b", 40)
	state := &workspaceHistoryState{HistoryLoading: true}
	first := workspace.WorkspaceGitHistoryPage{HeadSHA: head, BaselineSHA: baseline, Commits: []domain.WorkspaceGitCommit{{FullHash: "one"}, {FullHash: "two"}}, NextOffset: 2, HasMore: true, SummariesOnly: true}
	if err := state.acceptHistoryPage(first); err != nil || len(state.Commits) != 2 || !state.HistoryHasMore {
		t.Fatalf("first page = %+v, %v", state, err)
	}
	for _, page := range []workspace.WorkspaceGitHistoryPage{
		{HeadSHA: "changed", BaselineSHA: baseline, Offset: 2, NextOffset: 3, Commits: []domain.WorkspaceGitCommit{{FullHash: "three"}}},
		{HeadSHA: head, BaselineSHA: baseline, Offset: 1, NextOffset: 2, Commits: []domain.WorkspaceGitCommit{{FullHash: "three"}}},
		{HeadSHA: head, BaselineSHA: baseline, Offset: 2, NextOffset: 3, Commits: []domain.WorkspaceGitCommit{{FullHash: "one"}}},
		{HeadSHA: head, BaselineSHA: baseline, Offset: 2, NextOffset: 2, HasMore: true},
	} {
		if err := state.acceptHistoryPage(page); err == nil || len(state.Commits) != 2 {
			t.Fatalf("bad page changed accepted history: %+v, %v", page, err)
		}
	}
	last := workspace.WorkspaceGitHistoryPage{HeadSHA: head, BaselineSHA: baseline, Offset: 2, NextOffset: 3, Commits: []domain.WorkspaceGitCommit{{FullHash: "three"}}, SummariesOnly: true}
	if err := state.acceptHistoryPage(last); err != nil || state.HistoryHasMore || len(state.Commits) != 3 {
		t.Fatalf("last page not complete: %+v, %v", state, err)
	}
	state.CommitHashes, state.StartCommit, state.EndCommit = []string{"one"}, "one", "one"
	state.rememberHistoryMessage(workspace.WorkspaceGitCommitMessage{SHA: "one", Message: "full"})
	state.resetHistoryPages()
	if len(state.Commits) != 0 || len(state.HistoryMessages) != 0 || len(state.CommitHashes) != 0 || state.StartCommit != "" || state.HistoryHeadSHA != "" {
		t.Fatal("snapshot reset retained old rows, details or selection")
	}
}

func TestWorkspaceHistoryPagedActionsAndDetailCacheStayInScope(t *testing.T) {
	state := &workspaceHistoryState{Mode: "history", Open: true, TaskID: "task", ServiceID: "service", ModuleID: "module", Path: "worktree", BaselineRef: "origin/main", Loading: true, HistoryLoading: true,
		Commits: []domain.WorkspaceGitCommit{{FullHash: "one"}}, HistoryHeadSHA: strings.Repeat("a", 40), HistoryBaselineSHA: strings.Repeat("b", 40), HistoryNextOffset: 1}
	action := application.Action{Kind: "workspace.history", TaskID: state.TaskID, ServiceID: state.ServiceID, ModuleID: state.ModuleID, Path: state.Path,
		Payload: map[string]any{"paged": true, "baselineRef": state.BaselineRef, "historyPage": state.historyPageRequest()}}
	if !state.matches(action) {
		t.Fatal("current page request rejected")
	}
	stale := state.historyPageRequest()
	stale.HeadSHA = strings.Repeat("c", 40)
	action.Payload["historyPage"] = stale
	if state.matches(action) {
		t.Fatal("stale SHA page request accepted")
	}
	state.ExpandedCommits = map[string]bool{}
	state.HistorySummaries = true
	for index := 0; index < 24; index++ {
		sha := fmt.Sprintf("commit-%d", index)
		state.ExpandedCommits[sha] = true
		state.rememberHistoryMessage(workspace.WorkspaceGitCommitMessage{SHA: sha, Message: "full message", Truncated: true, Notice: "not complete"})
	}
	if len(state.HistoryMessages) != 16 || state.ExpandedCommits["commit-0"] {
		t.Fatal("expanded message cache exceeded its dialog budget")
	}
	message, notice := state.historyCommitText(domain.WorkspaceGitCommit{FullHash: "commit-23", Message: "summary"})
	if message != "full message" || notice != "not complete" {
		t.Fatal("detail truncation notice was hidden")
	}
}

func TestWorkspaceHistoryBuildsOnlyVisibleRowsAndShowsMoreState(t *testing.T) {
	state := &workspaceHistoryState{}
	for index := 0; index < 10000; index++ {
		state.Commits = append(state.Commits, domain.WorkspaceGitCommit{FullHash: fmt.Sprintf("%040x", index+1), Message: fmt.Sprintf("visible-history-row-%05d", index)})
	}
	s := &app{}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { s.renderWorkspaceCommitHistory(c, setTheme(c, false), state) })
	}, 700, 400)
	rows := 0
	for _, text := range tester.Texts() {
		if strings.HasPrefix(text, "visible-history-row-") {
			rows++
		}
	}
	if rows == 0 || rows >= 100 {
		t.Fatalf("built %d rows for a 400px viewport over 10000 commits", rows)
	}
	state.HistorySummaries, state.HistoryHasMore = true, true
	footer := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { s.renderWorkspaceHistoryMore(c, setTheme(c, false), state, "fixture") })
	}, 700, 80)
	if !footer.HasText("还有更多提交") || !footer.HasText("加载更多") {
		t.Fatal("partial history appeared complete to the user")
	}
}
