package desktop

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func workspaceReviewPickerLongError() string {
	var text strings.Builder
	for line := 1; line <= 60; line++ {
		fmt.Fprintf(&text, "fatal: history diagnostic %02d: cannot read object %04x\n", line, line)
	}
	return text.String()
}

func workspaceReviewOpenErrorPicker(t *testing.T, tester *ui.Tester) {
	t.Helper()
	for _, text := range tester.Texts() {
		if strings.HasPrefix(text, "提交范围：") {
			if err := tester.Click(text); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("commit-picker trigger is missing")
}

func workspaceReviewCheckPickerErrorControls(t *testing.T, tester *ui.Tester, width, height int, hasError bool) {
	t.Helper()
	panel := workspaceReviewPickerVisibleRect(t, tester, "提交范围选择器")
	if panel.X < 0 || panel.Y < 0 || panel.X+panel.W > float32(width)+.5 || panel.Y+panel.H > float32(height)+.5 ||
		panel.W > 560.5 || panel.H > 510.5 || panel.H > float32(height)-23.5 {
		t.Fatalf("picker escaped its bounded viewport: %+v in %dx%d", panel, width, height)
	}
	buttons := map[string]float32{"完成": 30}
	if hasError {
		buttons["重试提交历史"] = 34
	}
	for label, fullHeight := range buttons {
		rect := workspaceReviewPickerVisibleRect(t, tester, label)
		if rect.H < fullHeight-.1 || rect.H > fullHeight+.1 || rect.X < panel.X || rect.Y < panel.Y ||
			rect.X+rect.W > panel.X+panel.W+.5 || rect.Y+rect.H > panel.Y+panel.H+.5 {
			t.Fatalf("%s must be completely visible inside the panel: rect=%+v fullHeight=%g panel=%+v", label, rect, fullHeight, panel)
		}
	}
}

func workspaceReviewScrollFullDiagnostic(t *testing.T, state *workspaceHistoryState, tester *ui.Tester, key string) {
	t.Helper()
	fullText := "提交历史读取失败：" + state.HistoryPageError
	if !tester.HasText(fullText) {
		t.Fatal("the full diagnostic was truncated before presentation")
	}
	viewport := workspaceReviewPickerVisibleRect(t, tester, "提交历史错误详情")
	scroll := state.HistoryPageDiagnostic.scrolls[key]
	if viewport.H > workspaceHistoryPageErrorHeight-16+.1 || scroll == nil || scroll.MaxY <= viewport.H {
		t.Fatalf("the long diagnostic did not receive a bounded scroll viewport: rect=%+v scroll=%+v", viewport, scroll)
	}
	first, last := state.CommitPickerList.Visible()
	tester.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, 100000)
	if scroll.Y <= 0 || scroll.Y < scroll.MaxY-.5 || !tester.HasText(fullText) {
		t.Fatalf("the complete diagnostic could not be read through its final line: %+v", scroll)
	}
	if currentFirst, currentLast := state.CommitPickerList.Visible(); currentFirst != first || currentLast != last {
		t.Fatal("diagnostic scrolling moved the independent commit list")
	}
	tester.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, -100000)
	if scroll.Y != 0 {
		t.Fatal("the diagnostic's first line could not be revisited")
	}
}

func TestWorkspaceReviewPickerLongPageErrorsKeepDiagnosticAndControlsVisible(t *testing.T) {
	for _, stage := range []string{"first-page", "continuation"} {
		t.Run(stage, func(t *testing.T) {
			s, state, backend := newWorkspaceHistoryJobsFixture(t)
			state.Commits = nil
			for index := 0; index < 100; index++ {
				state.Commits = append(state.Commits, domain.WorkspaceGitCommit{
					FullHash: fmt.Sprintf("%040x", index+1), ShortHash: fmt.Sprintf("%07x", index+1), Message: fmt.Sprintf("error-picker-row-%03d", index),
				})
			}
			state.HistoryNextOffset = len(state.Commits)
			state.StartCommit, state.EndCommit = state.Commits[6].FullHash, state.Commits[4].FullHash
			state.CommitHashes = []string{state.Commits[4].FullHash, state.Commits[5].FullHash, state.Commits[6].FullHash}
			if stage == "continuation" {
				state.DiffSessionID, state.BaseCommit, state.TargetCommit = "retained-session", "retained-base", "retained-target"
				state.Files = []workspace.CompareChange{{Path: "current.go", Kind: "MODIFIED"}}
				state.SelectedPath = "current.go"
				state.Preview = &workspace.WorkspaceGitFilePreview{Content: &workspace.WorkspaceFilePreviewContent{Text: "retained comparison"}}
			}
			s.readWorkspaceHistoryPage(state, stage == "first-page")
			failed := waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
			finishWorkspaceHistoryReadResult(t, s, failed, nil, errors.New(workspaceReviewPickerLongError()))
			state.CommitPickerOpen = false
			selected := slices.Clone(state.CommitHashes)
			start, end, preview, session := state.StartCommit, state.EndCommit, state.Preview, state.DiffSessionID
			tester := ui.NewTester(func(c *ui.Context) { s.renderWorkspaceReviewPage(c, setTheme(c, false)) }, 960, 760)
			workspaceReviewOpenErrorPicker(t, tester)
			workspaceReviewCheckPickerErrorControls(t, tester, 960, 760, true)
			if stage == "continuation" {
				workspaceReviewPickerCheckViewport(t, tester, 960, 760)
			}
			workspaceReviewScrollFullDiagnostic(t, state, tester, workspaceReviewKey(state)+"-commit-picker")
			workspaceReviewCheckPickerErrorControls(t, tester, 960, 760, true)
			if err := tester.Click("完成"); err != nil {
				t.Fatal(err)
			}
			if state.CommitPickerOpen || !state.Open || !slices.Equal(state.CommitHashes, selected) || state.Preview != preview || state.DiffSessionID != session {
				t.Fatal("Done changed the comparison or selection instead of closing only the picker")
			}
			pageScroll := state.HistoryPageDiagnostic.scrolls[workspaceReviewKey(state)]
			pickerScroll := state.HistoryPageDiagnostic.scrolls[workspaceReviewKey(state)+"-commit-picker"]
			if pageScroll == nil || pickerScroll == nil || pageScroll == pickerScroll || len(state.HistoryPageDiagnostic.scrolls) != 2 {
				t.Fatal("the closing picker and page banner must own separate scroll handles")
			}
			workspaceReviewOpenErrorPicker(t, tester)
			if err := tester.Click("重试提交历史"); err != nil {
				t.Fatal(err)
			}
			retry := waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
			var request workspace.WorkspaceGitHistoryPageRequest
			if decodeWorkspaceValue(retry.read.action.Payload["historyPage"], &request) != nil || !state.HistoryLoading || state.HistoryPageError != "" || !state.CommitPickerOpen {
				t.Fatal("visible retry did not acquire a page owner and clear only its diagnostic")
			}
			result := workspace.WorkspaceGitHistoryPage{HeadSHA: "head", BaselineSHA: "baseline", Offset: 100, NextOffset: 101,
				Commits: []domain.WorkspaceGitCommit{{FullHash: "recovered", ShortHash: "recover", Message: "recovered row"}}, SummariesOnly: true}
			if stage == "first-page" {
				if request.Offset != 0 || request.HeadSHA != "" || request.BaselineSHA != "" {
					t.Fatal("first-page retry retained stale anchors")
				}
				result.Offset, result.NextOffset = 0, 1
			} else if request.Offset != 100 || request.HeadSHA != "head" || request.BaselineSHA != "baseline" ||
				!slices.Equal(state.CommitHashes, selected) || state.StartCommit != start || state.EndCommit != end || state.Preview != preview || state.DiffSessionID != session {
				t.Fatal("continuation retry lost its anchored offset, selected range, or comparison")
			}
			finishWorkspaceHistoryRead(t, s, retry, result)
			tester.Frame()
			workspaceReviewCheckPickerErrorControls(t, tester, 960, 760, false)
			workspaceReviewPickerCheckViewport(t, tester, 960, 760)
			if state.HistoryPageError != "" || state.HistoryPageDiagnostic.message != "" || state.HistoryLoading ||
				!slices.Equal(state.CommitHashes, selected) || state.Preview != preview || state.DiffSessionID != session {
				t.Fatal("successful retry retained stale diagnostic state or altered the comparison")
			}
		})
	}
}

func TestWorkspaceReviewPickerEmptyLoadingAndErrorsFitSmallWindows(t *testing.T) {
	for _, size := range [][2]int{{420, 260}, {640, 360}} {
		for _, mode := range []string{"empty", "loading", "short-error", "long-error", "continuation-error"} {
			t.Run(fmt.Sprintf("%dx%d/%s", size[0], size[1], mode), func(t *testing.T) {
				count := 0
				if mode == "continuation-error" {
					count = 100
				}
				state, tester := workspaceReviewPickerViewportFixture(t, count, size[0], size[1])
				state.HistoryLoading = mode == "loading"
				if strings.HasSuffix(mode, "error") {
					state.HistoryPageError = "cannot read history"
					if mode != "short-error" {
						state.HistoryPageError = workspaceReviewPickerLongError()
					}
				}
				if mode == "continuation-error" {
					state.HistorySummaries, state.HistoryHasMore = true, true
				}
				workspaceReviewOpenErrorPicker(t, tester)
				workspaceReviewCheckPickerErrorControls(t, tester, size[0], size[1], state.HistoryPageError != "")
				if mode == "loading" && !tester.HasText("正在读取提交") || mode == "empty" && !tester.HasText("主分支之后没有可选提交") {
					t.Fatal("the constrained empty/loading body lost its status")
				}
				if mode == "long-error" || mode == "continuation-error" {
					workspaceReviewScrollFullDiagnostic(t, state, tester, workspaceReviewKey(state)+"-commit-picker")
					workspaceReviewCheckPickerErrorControls(t, tester, size[0], size[1], true)
				}
				if err := tester.Click("完成"); err != nil {
					t.Fatal(err)
				}
				if state.CommitPickerOpen || !state.Open {
					t.Fatal("small-window Done did not close only the picker")
				}
			})
		}
	}
}

func TestWorkspaceReviewPickerLongErrorRetainsVirtualRowBudget(t *testing.T) {
	state, tester := workspaceReviewPickerViewportFixture(t, 10000, 960, 760)
	state.HistorySummaries, state.HistoryHasMore = true, true
	state.HistoryPageError = workspaceReviewPickerLongError()
	workspaceReviewOpenErrorPicker(t, tester)
	workspaceReviewCheckPickerErrorControls(t, tester, 960, 760, true)
	workspaceReviewPickerCheckViewport(t, tester, 960, 760)
	rows := 0
	for _, text := range tester.Texts() {
		if strings.HasPrefix(text, "picker-row-") {
			rows++
		}
	}
	if rows == 0 || rows >= 64 {
		t.Fatalf("the long diagnostic caused full history materialization: rows=%d", rows)
	}
	workspaceReviewScrollFullDiagnostic(t, state, tester, workspaceReviewKey(state)+"-commit-picker")
	state.CommitPickerList.ScrollToEnd()
	tester.Frame()
	last := state.Commits[len(state.Commits)-1]
	workspaceReviewPickerVisibleRect(t, tester, last.ShortHash+" "+last.Message)
	workspaceReviewCheckPickerErrorControls(t, tester, 960, 760, true)
}

func TestWorkspaceHistoryDiagnosticScrollResetsWithItsError(t *testing.T) {
	s := &app{}
	state := &workspaceHistoryState{HistoryPageError: workspaceReviewPickerLongError(), Error: "independent comparison failure"}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { s.renderWorkspaceHistoryPageError(c, setTheme(c, false), state, "diagnostic") })
	}, 640, 360)
	workspaceReviewScrollFullDiagnostic(t, state, tester, "diagnostic")
	fullText := "提交历史读取失败：" + state.HistoryPageError
	if err := tester.Click(fullText); err != nil {
		t.Fatal(err)
	}
	tester.Command("selectAll")
	tester.Command("copy")
	if tester.Clipboard() != fullText || state.HistoryPageError != workspaceReviewPickerLongError() {
		t.Fatal("selecting and copying the diagnostic did not preserve its complete body")
	}
	viewport := workspaceReviewPickerVisibleRect(t, tester, "提交历史错误详情")
	tester.Scroll(viewport.X+viewport.W/2, viewport.Y+viewport.H/2, 0, 100000)
	state.HistoryPageError = "replacement diagnostic"
	tester.Frame()
	scroll := state.HistoryPageDiagnostic.scrolls["diagnostic"]
	if scroll == nil || scroll.Y != 0 || scroll.MaxY != 0 || !tester.HasText("提交历史读取失败：replacement diagnostic") {
		t.Fatal("a replaced diagnostic inherited the prior error's scroll offset")
	}
	state.HistoryPageError = ""
	tester.Frame()
	if tester.HasText("重试提交历史") || state.HistoryPageDiagnostic.message != "" || state.HistoryPageDiagnostic.scrolls != nil || state.Error != "independent comparison failure" {
		t.Fatal("clearing the diagnostic retained its presentation or altered another purpose's error")
	}
}
