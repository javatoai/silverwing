package desktop

import (
	"errors"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func workspaceHistoryContinuationFixture() workspace.WorkspaceGitHistoryPage {
	return workspace.WorkspaceGitHistoryPage{HeadSHA: "head", BaselineSHA: "baseline", Offset: 3, NextOffset: 4,
		Commits: []domain.WorkspaceGitCommit{{FullHash: "older"}}, SummariesOnly: true}
}

func workspaceHistoryReviewTester(s *app) *ui.Tester {
	return ui.NewTester(func(c *ui.Context) { s.renderWorkspaceReviewPage(c, setTheme(c, false)) }, 1100, 760)
}

func TestWorkspaceHistoryExpandDuringPageReadsAnchoredMessage(t *testing.T) {
	for _, view := range []string{"history", "review"} {
		for _, first := range []string{"page", "message"} {
			t.Run(view+"/"+first+"-first", func(t *testing.T) {
				s, state, backend := newWorkspaceHistoryJobsFixture(t)
				state.Mode = view
				state.Commits[0].Message = "summary three"
				var comparison workspaceHistoryPendingRead
				if view == "review" {
					comparison = startWorkspaceHistoryComparison(t, s, state, backend, "workspace.diff-preview")
				}
				s.readWorkspaceHistoryPage(state, false)
				page := waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
				tester := ui.NewTester(func(c *ui.Context) {
					ui.Column(c).Fill().Children(func() {
						if view == "history" {
							s.renderWorkspaceCommitHistoryRow(c, setTheme(c, false), state, 0, state.Commits[0])
						} else {
							s.renderWorkspaceReviewCommitOption(c, setTheme(c, false), state, "fixture", state.Commits[0])
						}
					})
				}, 700, 300)
				button := "展开"
				if view == "history" {
					button = "展开提交信息"
				}
				if err := tester.Click(button); err != nil {
					t.Fatal(err)
				}
				message := waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
				if stringPayloadValue(message.read.action.Payload["messageSha"]) != "three" || !state.ExpandedCommits["three"] ||
					state.HistoryMessageSHA != "three" || page.read.ctx.Err() != nil || !state.HistoryLoading || !tester.HasText("正在读取完整提交信息") {
					t.Fatal("expanding a loaded row while paging lost its message request/loading state or cancelled the page")
				}
				var anchor workspace.WorkspaceGitHistoryPageRequest
				if decodeWorkspaceValue(message.read.action.Payload["historyPage"], &anchor) != nil || anchor.HeadSHA != "head" || anchor.BaselineSHA != "baseline" {
					t.Fatal("message read was not bound to the already loaded snapshot")
				}
				body := workspace.WorkspaceGitCommitMessage{SHA: "three", Message: "complete commit body\nsecond line"}
				if first == "page" {
					finishWorkspaceHistoryRead(t, s, page, workspaceHistoryContinuationFixture())
					if message.read.ctx.Err() != nil || state.HistoryMessageSHA != "three" {
						t.Fatal("page completion displaced the pending message")
					}
					finishWorkspaceHistoryRead(t, s, message, body)
				} else {
					finishWorkspaceHistoryRead(t, s, message, body)
					if page.read.ctx.Err() != nil || !state.HistoryLoading {
						t.Fatal("message completion displaced the pending page")
					}
					finishWorkspaceHistoryRead(t, s, page, workspaceHistoryContinuationFixture())
				}
				tester.Frame()
				if !tester.HasText("complete commit body") || !tester.HasText("second line") || state.HistoryMessageSHA != "" || len(state.Commits) != 4 {
					t.Fatal("the expanded row did not show the completed body after both read orders")
				}
				if view == "review" {
					if comparison.read.ctx.Err() != nil {
						t.Fatal("page/message reads cancelled the independent comparison")
					}
					finishWorkspaceHistoryComparison(t, s, state, backend, comparison)
				}
			})
		}
	}
}

func TestWorkspaceHistoryMessageAdmissionStillRequiresSnapshotAndMembership(t *testing.T) {
	for _, missing := range []string{"first-page", "head", "baseline", "summaries", "membership", "closed"} {
		t.Run(missing, func(t *testing.T) {
			s, state, backend := newWorkspaceHistoryJobsFixture(t)
			state.HistoryLoading = true
			sha := "three"
			switch missing {
			case "first-page":
				state.CommitsLoaded = false
			case "head":
				state.HistoryHeadSHA = ""
			case "baseline":
				state.HistoryBaselineSHA = ""
			case "summaries":
				state.HistorySummaries = false
			case "membership":
				sha = "not-loaded"
			case "closed":
				state.Open = false
			}
			s.readWorkspaceCommitMessage(state, sha)
			if state.HistoryMessageSHA != "" || s.workspaceUnit.state.workspaceHistoryJobs != nil {
				t.Fatal("an unanchored, closed or nonmember row acquired a message owner")
			}
			select {
			case read := <-backend.reads:
				t.Fatalf("inadmissible message reached backend: %+v", read.action)
			default:
			}
		})
	}
	t.Run("all-history-empty-baseline", func(t *testing.T) {
		s, state, backend := newWorkspaceHistoryJobsFixture(t)
		state.Mode, state.AllHistory, state.HistoryBaselineSHA = "history", true, ""
		s.readWorkspaceHistoryPage(state, false)
		page := waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
		s.readWorkspaceCommitMessage(state, "three")
		message := waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
		if !workspaceHistoryAllScope(message.read.action) || stringPayloadValue(message.read.action.Payload["baselineRef"]) != "" || page.read.ctx.Err() != nil {
			t.Fatal("all-history message rejected its legal empty baseline or cancelled paging")
		}
		finishWorkspaceHistoryRead(t, s, message, workspace.WorkspaceGitCommitMessage{SHA: "three", Message: "all-history body"})
		result := workspaceHistoryContinuationFixture()
		result.BaselineSHA = ""
		finishWorkspaceHistoryRead(t, s, page, result)
		if state.HistoryMessages["three"].Message != "all-history body" || state.HistoryBaselineSHA != "" {
			t.Fatal("all-history completion lost the detail or synthesized a baseline SHA")
		}
	})
}

func TestWorkspaceHistoryPageStartAndSuccessPreserveComparisonFailure(t *testing.T) {
	for _, first := range []string{"comparison-error", "page-start"} {
		t.Run(first, func(t *testing.T) {
			s, state, backend := newWorkspaceHistoryJobsFixture(t)
			comparison := startWorkspaceHistoryComparison(t, s, state, backend, "workspace.diff-preview")
			var page workspaceHistoryPendingRead
			if first == "page-start" {
				s.readWorkspaceHistoryPage(state, false)
				page = waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
			}
			finishWorkspaceHistoryReadResult(t, s, comparison, nil, errors.New("comparison read failed"))
			if first == "comparison-error" {
				s.readWorkspaceHistoryPage(state, false)
				page = waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
			}
			if state.Error != "comparison read failed" || state.HistoryPageError != "" || state.PreviewLoading || state.SelectedPath == "" {
				t.Fatal("page start erased the failed comparison or changed its loading state")
			}
			finishWorkspaceHistoryRead(t, s, page, workspaceHistoryContinuationFixture())
			if state.Error != "comparison read failed" || state.HistoryPageError != "" || state.Preview != nil {
				t.Fatal("page success erased the failed comparison")
			}
			tester := workspaceHistoryReviewTester(s)
			if !tester.HasText("comparison read failed") || !tester.HasText("重试差异") || tester.HasText("等待差异结果") {
				t.Fatal("failed comparison became an idle waiting screen without diagnostics/retry")
			}
			if err := tester.Click("重试差异"); err != nil {
				t.Fatal(err)
			}
			retry := waitWorkspaceHistoryRead(t, s, backend, "workspace.diff-files")
			if state.Error != "" || !state.FilesLoading || state.CommitHashes[0] != "one" {
				t.Fatal("comparison retry did not keep its selection and clear its own error")
			}
			finishWorkspaceHistoryComparison(t, s, state, backend, retry)
		})
	}
}

func TestWorkspaceHistoryComparisonSuccessPreservesPageFailure(t *testing.T) {
	for _, first := range []string{"page-error", "comparison-success"} {
		t.Run(first, func(t *testing.T) {
			s, state, backend := newWorkspaceHistoryJobsFixture(t)
			comparison := startWorkspaceHistoryComparison(t, s, state, backend, "workspace.diff-preview")
			s.readWorkspaceHistoryPage(state, false)
			page := waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
			if first == "page-error" {
				finishWorkspaceHistoryReadResult(t, s, page, nil, errors.New("next page failed"))
				finishWorkspaceHistoryComparison(t, s, state, backend, comparison)
			} else {
				finishWorkspaceHistoryComparison(t, s, state, backend, comparison)
				finishWorkspaceHistoryReadResult(t, s, page, nil, errors.New("next page failed"))
			}
			preview, session := state.Preview, state.DiffSessionID
			if state.Error != "" || state.HistoryPageError != "next page failed" || preview == nil || state.HistoryLoading {
				t.Fatal("comparison completion erased the page failure or page failure damaged the comparison")
			}
			tester := workspaceHistoryReviewTester(s)
			if !tester.HasText("next page failed") || !tester.HasText("重试提交历史") {
				t.Fatal("page failure has no visible diagnostic and retry")
			}
			if err := tester.Click("重试提交历史"); err != nil {
				t.Fatal(err)
			}
			retry := waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
			if state.HistoryPageError != "" || state.Preview != preview || state.DiffSessionID != session || len(state.CommitHashes) != 1 {
				t.Fatal("continuation retry discarded the independent comparison or kept its old error")
			}
			finishWorkspaceHistoryRead(t, s, retry, workspaceHistoryContinuationFixture())
			s.finishWorkspaceHistoryJob(page.job, application.ActionResult{}, errors.New("late page error"))
			s.finishWorkspaceHistoryJob(comparison.job, application.ActionResult{}, errors.New("late comparison error"))
			if state.HistoryPageError != "" || state.Error != "" || state.Preview != preview {
				t.Fatal("retired callbacks changed the successful retry or the independent comparison")
			}
		})
	}
}

func TestWorkspaceHistoryRetriesClearOnlyTheirPurposeError(t *testing.T) {
	s, state, backend := newWorkspaceHistoryJobsFixture(t)
	comparison := startWorkspaceHistoryComparison(t, s, state, backend, "workspace.diff-preview")
	s.readWorkspaceHistoryPage(state, false)
	page := waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
	finishWorkspaceHistoryReadResult(t, s, comparison, nil, errors.New("comparison failed"))
	finishWorkspaceHistoryReadResult(t, s, page, nil, errors.New("page failed"))
	tester := workspaceHistoryReviewTester(s)
	if !tester.HasText("comparison failed") || !tester.HasText("page failed") {
		t.Fatal("one purpose's failure hid the other diagnostic")
	}
	if err := tester.Click("重试差异"); err != nil {
		t.Fatal(err)
	}
	retryComparison := waitWorkspaceHistoryRead(t, s, backend, "workspace.diff-files")
	if state.Error != "" || state.HistoryPageError != "page failed" {
		t.Fatal("comparison retry cleared the page error")
	}
	finishWorkspaceHistoryReadResult(t, s, retryComparison, nil, errors.New("comparison retry failed"))
	tester.Frame()
	if err := tester.Click("重试提交历史"); err != nil {
		t.Fatal(err)
	}
	retryPage := waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
	if state.HistoryPageError != "" || state.Error != "comparison retry failed" {
		t.Fatal("page retry cleared the comparison error")
	}
	finishWorkspaceHistoryRead(t, s, retryPage, workspaceHistoryContinuationFixture())
	if state.Error != "comparison retry failed" || state.HistoryPageError != "" {
		t.Fatal("successful page retry cleared the still-failed comparison")
	}
}

func TestWorkspaceHistoryChangedSnapshotResetsErrorsAndRetriesFirstPage(t *testing.T) {
	s, state, backend := newWorkspaceHistoryJobsFixture(t)
	comparison := startWorkspaceHistoryComparison(t, s, state, backend, "workspace.diff-preview")
	state.Error = "previous comparison failure"
	s.readWorkspaceHistoryPage(state, false)
	page := waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
	finishWorkspaceHistoryReadResult(t, s, page, nil, workspace.ErrWorkspaceGitHistoryChanged)
	if state.Error != "" || state.HistoryPageError == "" || len(state.CommitHashes) != 0 || state.DiffSessionID != "" || comparison.read.ctx.Err() == nil {
		t.Fatal("invalidated snapshot kept its old comparison/error or lost the page diagnostic")
	}
	s.retryWorkspaceHistoryPage(state)
	retry := waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
	var request workspace.WorkspaceGitHistoryPageRequest
	if decodeWorkspaceValue(retry.read.action.Payload["historyPage"], &request) != nil || request.Offset != 0 || request.HeadSHA != "" || request.BaselineSHA != "" || state.HistoryPageError != "" {
		t.Fatal("stale-snapshot retry did not start a clean first page")
	}
	finishWorkspaceHistoryRead(t, s, comparison, workspace.WorkspaceGitFilePreview{})
	finishWorkspaceHistoryRead(t, s, retry, workspace.WorkspaceGitHistoryPage{HeadSHA: "fresh-head", BaselineSHA: "fresh-base", NextOffset: 1,
		Commits: []domain.WorkspaceGitCommit{{FullHash: "fresh"}}, SummariesOnly: true})
	if state.Error != "" || state.HistoryPageError != "" || state.Preview != nil || state.HistoryHeadSHA != "fresh-head" {
		t.Fatal("new snapshot did not recover cleanly or accepted the old comparison")
	}
}

func TestWorkspaceHistoryDialogPageErrorKeepsRowsAndOffersValidRetry(t *testing.T) {
	for _, stage := range []string{"first-page", "continuation"} {
		t.Run(stage, func(t *testing.T) {
			s, state, backend := newWorkspaceHistoryJobsFixture(t)
			state.Mode = "history"
			state.Commits[0].Message = "retained summary"
			s.readWorkspaceHistoryPage(state, stage == "first-page")
			page := waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
			finishWorkspaceHistoryReadResult(t, s, page, nil, errors.New("history dialog page failed"))
			tester := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Children(func() { s.renderWorkspaceCommitHistoryDialog(c, setTheme(c, false), state) })
			}, 1100, 760)
			if !tester.HasText("history dialog page failed") || !tester.HasText("重试提交历史") || tester.HasText("主分支之后没有提交。") {
				t.Fatal("history dialog hid a page failure or presented it as a complete empty history")
			}
			if stage == "continuation" && !tester.HasText("retained summary") {
				t.Fatal("failed continuation removed already loaded history rows")
			}
			if err := tester.Click("重试提交历史"); err != nil {
				t.Fatal(err)
			}
			retry := waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
			var request workspace.WorkspaceGitHistoryPageRequest
			if decodeWorkspaceValue(retry.read.action.Payload["historyPage"], &request) != nil || state.HistoryPageError != "" || !state.HistoryLoading {
				t.Fatal("dialog retry did not register a valid page owner and clear its own error")
			}
			result := workspaceHistoryContinuationFixture()
			if stage == "first-page" {
				if request.Offset != 0 || request.HeadSHA != "" || request.BaselineSHA != "" {
					t.Fatal("first-page failure retried with stale anchors")
				}
				result.Offset, result.NextOffset = 0, 1
			} else if request.Offset != 3 || request.HeadSHA != "head" || request.BaselineSHA != "baseline" {
				t.Fatal("continuation failure lost its anchored retry offset")
			}
			result.Commits[0].Message = "recovered history row"
			finishWorkspaceHistoryRead(t, s, retry, result)
			tester.Frame()
			if tester.HasText("history dialog page failed") || !tester.HasText("recovered history row") || state.Error != "" || state.HistoryPageError != "" {
				t.Fatal("successful dialog retry retained an obsolete page diagnostic or lost recovered rows")
			}
		})
	}
}

func TestWorkspaceHistoryNonPagedResultsAndErrorsKeepLegacyContract(t *testing.T) {
	s, state, backend := newWorkspaceHistoryJobsFixture(t)
	state.Mode, state.Loading, state.HistoryLoading = "history", true, true
	action := application.Action{Kind: "workspace.history", TaskID: state.TaskID, ServiceID: state.ServiceID, ModuleID: state.ModuleID, Path: state.Path,
		Payload: map[string]any{"baselineRef": state.BaselineRef, "allHistory": false}}
	s.dispatch(action)
	failed := waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
	finishWorkspaceHistoryReadResult(t, s, failed, nil, errors.New("legacy read failed"))
	if state.Error != "legacy read failed" || state.HistoryPageError != "" || state.Loading || state.HistoryLoading {
		t.Fatal("nonpaged error no longer follows the legacy Error/loading contract")
	}
	state.Loading, state.HistoryLoading = true, true
	s.dispatch(action)
	retry := waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
	if workspacePayloadBool(retry.read.action.Payload, "paged") {
		t.Fatal("legacy caller was silently changed to a paged action")
	}
	finishWorkspaceHistoryRead(t, s, retry, []domain.WorkspaceGitCommit{{FullHash: "legacy", Message: "subject\ncomplete legacy body"}})
	if state.Error != "" || state.HistoryPageError != "" || state.HistoryLoading || len(state.Commits) != 1 || state.Commits[0].Message != "subject\ncomplete legacy body" {
		t.Fatal("nonpaged complete array/body or successful error recovery changed")
	}
}
