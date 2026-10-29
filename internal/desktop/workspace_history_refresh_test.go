package desktop

import (
	"context"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func TestWorkspaceHistoryRefreshClearsComparisonAndRetiresSessionOnce(t *testing.T) {
	s, state, backend := newWorkspaceHistoryJobsFixture(t)
	oldPreview := startWorkspaceHistoryComparison(t, s, state, backend, "workspace.diff-preview")
	state.Preview = &workspace.WorkspaceGitFilePreview{Content: &workspace.WorkspaceFilePreviewContent{Text: "old range code"}}
	state.ResultBaselineRef, state.Notice = "old-baseline-label", "old-comparison-notice"
	state.PrefetchVisible = append([]workspace.CompareChange(nil), state.Files...)
	state.PrefetchFrameVisible = append([]workspace.CompareChange(nil), state.Files...)
	prefetchCtx, cancel := context.WithCancel(context.Background())
	state.Prefetch, state.PrefetchKey = &workspaceReviewPrefetchJob{cancel: cancel}, "old-prefetch"
	state.CommitDebounce = time.AfterFunc(time.Hour, func() {})
	previousSelectionGeneration := state.CommitSelectionGeneration
	s.refreshWorkspaceReview(state)
	firstPage := waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
	if len(state.CommitHashes) != 0 || state.StartCommit != "" || state.EndCommit != "" || !state.CommitPickerOpen || len(state.Commits) != 0 || state.CommitsLoaded {
		t.Fatal("refresh retained the old history snapshot or selected range")
	}
	if state.Preview != nil || len(state.Files) != 0 || state.SelectedPath != "" || state.SelectedOldPath != "" || state.SelectedKind != "" ||
		state.BaseCommit != "" || state.TargetCommit != "" || state.DiffSessionID != "" || state.ResultBaselineRef != "" || state.Notice != "" || state.FilesLoading || state.PreviewLoading {
		t.Fatal("refresh displayed comparison data after dropping its range")
	}
	if state.Prefetch != nil || state.PrefetchKey != "" || len(state.PrefetchVisible) != 0 || len(state.PrefetchFrameVisible) != 0 || prefetchCtx.Err() == nil ||
		state.CommitDebounce != nil || state.CommitSelectionGeneration <= previousSelectionGeneration || oldPreview.read.ctx.Err() == nil {
		t.Fatal("refresh retained a prefetch, debounce callback or comparison owner")
	}
	select {
	case id := <-backend.releases:
		if id != "original-session" {
			t.Fatalf("refresh released the wrong session: %q", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("refresh did not release its old comparison session")
	}
	finishWorkspaceHistoryRead(t, s, oldPreview, workspace.WorkspaceGitFilePreview{Content: &workspace.WorkspaceFilePreviewContent{Text: "late old range"}})
	if state.Preview != nil || !state.HistoryLoading {
		t.Fatal("old comparison callback restored stale code or interrupted the new page")
	}

	// The first-page actions have identical payloads. A second refresh must
	// reject the first callback by owner/generation, rather than only by offset.
	s.refreshWorkspaceReview(state)
	page := waitWorkspaceHistoryRead(t, s, backend, "workspace.history")
	finishWorkspaceHistoryRead(t, s, firstPage, workspace.WorkspaceGitHistoryPage{HeadSHA: "stale", BaselineSHA: "stale-base", NextOffset: 1,
		Commits: []domain.WorkspaceGitCommit{{FullHash: "stale-commit"}}, SummariesOnly: true})
	if len(state.Commits) != 0 || !state.HistoryLoading || state.Preview != nil {
		t.Fatal("superseded first-page callback restored the retired snapshot")
	}
	select {
	case id := <-backend.releases:
		t.Fatalf("the retired session was released more than once: %q", id)
	default:
	}
	finishWorkspaceHistoryRead(t, s, page, workspace.WorkspaceGitHistoryPage{HeadSHA: "new-head", BaselineSHA: "new-baseline", NextOffset: 2,
		Commits: []domain.WorkspaceGitCommit{{FullHash: "new-two", ParentHashes: []string{"new-one"}}, {FullHash: "new-one"}}, SummariesOnly: true})
	if state.HistoryLoading || len(state.Commits) != 2 || state.HistoryHeadSHA != "new-head" || len(state.CommitHashes) != 0 || state.Preview != nil {
		t.Fatal("the new page inherited old selection or comparison data")
	}
	s.selectWorkspaceReviewCommit(state, "new-two")
	s.readWorkspaceReviewFiles(state)
	comparison := waitWorkspaceHistoryRead(t, s, backend, "workspace.diff-files")
	finishWorkspaceHistoryComparison(t, s, state, backend, comparison)
	if len(state.CommitHashes) != 1 || state.CommitHashes[0] != "new-two" || state.DiffSessionID != "new-session" {
		t.Fatal("a fresh range could not establish its own comparison session")
	}
}
