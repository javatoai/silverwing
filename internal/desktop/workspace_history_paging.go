package desktop

import (
	"errors"
	"fmt"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo/ui"
)

func (state *workspaceHistoryState) resetHistoryPages() {
	state.Commits, state.CommitsLoaded = nil, false
	state.HistoryHeadSHA, state.HistoryBaselineSHA, state.HistoryMessageSHA = "", "", ""
	state.HistoryNextOffset, state.HistoryHasMore, state.HistorySummaries = 0, false, false
	state.HistoryMessages, state.HistoryMessageErrors, state.HistoryMessageOrder = nil, nil, nil
	state.ExpandedCommits = make(map[string]bool)
	state.HistoryList, state.CommitPickerList = ui.ListState{}, ui.ListState{}
	state.StartCommit, state.EndCommit, state.CommitHashes = "", "", nil
	state.HistoryPageError = ""
	state.HistoryPageDiagnostic = workspaceHistoryDiagnosticState{}
}

// A new history snapshot drops the selected range as well as everything owned
// by that comparison. Queued debounce callbacks and in-flight read callbacks
// must not restore a result from the previous snapshot.
func (s *app) resetWorkspaceHistorySnapshot(state *workspaceHistoryState) {
	s.cancelWorkspaceHistoryRead()
	s.cancelWorkspaceReviewDebounce(state)
	state.CommitSelectionGeneration++
	state.resetHistoryPages()
	if state.Mode == "review" {
		s.clearWorkspaceReviewResult(state)
		state.InitialChange = nil
		state.CommitPickerOpen = true
	} else {
		state.Error = ""
	}
}

func (state *workspaceHistoryState) acceptHistoryPage(page workspace.WorkspaceGitHistoryPage) error {
	if page.Offset < 0 || page.NextOffset != page.Offset+len(page.Commits) || page.HasMore && len(page.Commits) == 0 ||
		page.Offset != len(state.Commits) || page.Offset > 0 && (page.HeadSHA != state.HistoryHeadSHA || page.BaselineSHA != state.HistoryBaselineSHA) {
		return errors.New("提交历史分页身份或位置已变化，请刷新后重新选择")
	}
	seen := make(map[string]bool, len(state.Commits)+len(page.Commits))
	for _, commit := range state.Commits {
		seen[commit.FullHash] = true
	}
	for _, commit := range page.Commits {
		if commit.FullHash == "" || seen[commit.FullHash] {
			return errors.New("提交历史页包含重复或无效提交，未追加此页")
		}
		seen[commit.FullHash] = true
	}
	state.Commits = append(state.Commits, page.Commits...)
	state.HistoryHeadSHA, state.HistoryBaselineSHA = page.HeadSHA, page.BaselineSHA
	state.HistoryNextOffset, state.HistoryHasMore, state.HistorySummaries = page.NextOffset, page.HasMore, page.SummariesOnly
	state.CommitsLoaded, state.HistoryLoading, state.Loading = true, false, false
	state.HistoryPageError = ""
	return nil
}

func (state *workspaceHistoryState) historyPageRequest() workspace.WorkspaceGitHistoryPageRequest {
	return workspace.WorkspaceGitHistoryPageRequest{HeadSHA: state.HistoryHeadSHA, BaselineSHA: state.HistoryBaselineSHA, Offset: state.HistoryNextOffset, Limit: workspace.WorkspaceGitHistoryPageSize}
}

func (s *app) readWorkspaceHistoryPage(state *workspaceHistoryState, reset bool) {
	if state == nil || state != s.workspaceUnit.state.workspaceHistory || !state.Open || state.HistoryLoading && !reset {
		return
	}
	if reset {
		s.resetWorkspaceHistorySnapshot(state)
	} else {
		s.cancelWorkspaceHistoryPurpose(workspaceHistoryPage)
	}
	state.HistoryPageError, state.HistoryLoading, state.Loading = "", true, true
	baseline := state.BaselineRef
	if state.AllHistory {
		baseline = ""
	}
	s.act("workspace.history", state.TaskID, state.ServiceID, state.ModuleID, state.Path, "", map[string]any{
		"baselineRef": baseline, "allHistory": state.AllHistory, "paged": true, "historyPage": state.historyPageRequest(),
	})
}

func (s *app) readWorkspaceCommitMessage(state *workspaceHistoryState, sha string) {
	if state == nil || state != s.workspaceUnit.state.workspaceHistory || !state.Open || !state.CommitsLoaded || !state.HistorySummaries ||
		state.HistoryHeadSHA == "" || !state.AllHistory && state.HistoryBaselineSHA == "" {
		return
	}
	if _, ok := state.HistoryMessages[sha]; ok {
		return
	}
	if _, found := workspaceReviewCommitByHash(state.Commits, sha); !found {
		return
	}
	s.cancelWorkspaceHistoryPurpose(workspaceHistoryMessage)
	state.HistoryMessageSHA = sha
	if state.HistoryMessageErrors != nil {
		delete(state.HistoryMessageErrors, sha)
	}
	baseline := state.BaselineRef
	if state.AllHistory {
		baseline = ""
	}
	s.act("workspace.history", state.TaskID, state.ServiceID, state.ModuleID, state.Path, "", map[string]any{
		"baselineRef": baseline, "allHistory": state.AllHistory, "paged": true, "historyPage": state.historyPageRequest(), "messageSha": sha,
	})
}

func (s *app) retryWorkspaceHistoryPage(state *workspaceHistoryState) {
	if state == nil || state.HistoryLoading {
		return
	}
	// A failed continuation can reuse its anchored offset without discarding
	// the current comparison. First-page/stale snapshots need a fresh reset.
	reset := !state.CommitsLoaded || !state.HistorySummaries || state.HistoryHeadSHA == "" || !state.AllHistory && state.HistoryBaselineSHA == ""
	s.readWorkspaceHistoryPage(state, reset)
}

func (state *workspaceHistoryState) rememberHistoryMessage(message workspace.WorkspaceGitCommitMessage) {
	if state.HistoryMessages == nil {
		state.HistoryMessages = make(map[string]workspace.WorkspaceGitCommitMessage)
	}
	_, existed := state.HistoryMessages[message.SHA]
	state.HistoryMessages[message.SHA] = message
	if existed {
		return
	}
	state.HistoryMessageOrder = append(state.HistoryMessageOrder, message.SHA)
	// Detail ownership is per dialog/snapshot; at most sixteen 256 KiB messages.
	for len(state.HistoryMessageOrder) > 16 {
		oldest := state.HistoryMessageOrder[0]
		state.HistoryMessageOrder = state.HistoryMessageOrder[1:]
		delete(state.HistoryMessages, oldest)
		delete(state.ExpandedCommits, oldest)
	}
}

func (state *workspaceHistoryState) historyCommitText(commit domain.WorkspaceGitCommit) (message, notice string) {
	if state.ExpandedCommits[commit.FullHash] && state.HistorySummaries {
		if detail, ok := state.HistoryMessages[commit.FullHash]; ok {
			return detail.Message, detail.Notice
		}
		if state.HistoryMessageSHA == commit.FullHash {
			return commit.Message, "正在读取完整提交信息…"
		}
		if err := state.HistoryMessageErrors[commit.FullHash]; err != "" {
			return commit.Message, "提交信息未完整读取：" + err
		}
	}
	return commit.Message, ""
}

func workspaceHistoryPageActionMatches(state *workspaceHistoryState, action application.Action) bool {
	var request workspace.WorkspaceGitHistoryPageRequest
	if decodeWorkspaceValue(action.Payload["historyPage"], &request) != nil {
		return false
	}
	if sha := stringPayloadValue(action.Payload["messageSha"]); sha != "" {
		return state.HistoryMessageSHA == sha && request.HeadSHA == state.HistoryHeadSHA && request.BaselineSHA == state.HistoryBaselineSHA
	}
	return state.HistoryLoading && request.Offset == len(state.Commits) &&
		(request.Offset == 0 && request.HeadSHA == "" || request.HeadSHA == state.HistoryHeadSHA && request.BaselineSHA == state.HistoryBaselineSHA)
}

func (s *app) renderWorkspaceHistoryMore(c *ui.Context, p palette, state *workspaceHistoryState, key string) {
	if !state.HistorySummaries {
		return
	}
	ui.Row(c).FillWidth().Shrink(0).Gap(8).AlignItems(ui.Center).Children(func() {
		text := fmt.Sprintf("已读取 %d 条", len(state.Commits))
		if state.HistoryHasMore {
			text += " · 还有更多提交"
		}
		label(c, text, 10, p.muted).Grow(1)
		if state.HistoryHasMore && action(c, key+"-history-more", "chevron-down", "加载更多", p, state.HistoryLoading) {
			s.readWorkspaceHistoryPage(state, false)
		}
	})
}
