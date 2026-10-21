package desktop

import (
	"reflect"
	"testing"

	"silverwing-go/internal/domain"
)

func TestWorkspaceReviewSelectionExtendsAndCollapsesAncestryRange(t *testing.T) {
	// The slice is deliberately not chronological; ancestry decides membership,
	// while the helper preserves the backend's display order in its result.
	commits := []domain.WorkspaceGitCommit{
		{FullHash: "c", ParentHashes: []string{"b"}},
		{FullHash: "a", ParentHashes: []string{"base"}},
		{FullHash: "b", ParentHashes: []string{"a"}},
	}
	start, end, selected, err := workspaceReviewNextCommitRange(commits, "", "", "c", nil)
	if err != nil || start != "c" || end != "c" || !reflect.DeepEqual(selected, []string{"c"}) {
		t.Fatalf("select C = (%q, %q, %v, %v), want singleton C", start, end, selected, err)
	}
	start, end, selected, err = workspaceReviewNextCommitRange(commits, start, end, "a", selected)
	if err != nil || start != "a" || end != "c" || !reflect.DeepEqual(selected, []string{"c", "a", "b"}) {
		t.Fatalf("extend C to A = (%q, %q, %v, %v), want backend display order C,A,B", start, end, selected, err)
	}
	start, end, selected, err = workspaceReviewNextCommitRange(commits, start, end, "b", selected)
	if err != nil || start != "b" || end != "b" || !reflect.DeepEqual(selected, []string{"b"}) {
		t.Fatalf("collapse range on B = (%q, %q, %v, %v), want singleton B", start, end, selected, err)
	}
	start, end, selected, err = workspaceReviewNextCommitRange(commits, start, end, "b", selected)
	if err != nil || start != "" || end != "" || len(selected) != 0 {
		t.Fatalf("clear singleton B = (%q, %q, %v, %v), want empty", start, end, selected, err)
	}
}

func TestWorkspaceReviewSelectionRejectsUnrelatedBranchesWithoutChangingSelection(t *testing.T) {
	commits := []domain.WorkspaceGitCommit{
		{FullHash: "left", ParentHashes: []string{"base"}},
		{FullHash: "right", ParentHashes: []string{"base"}},
	}
	start, end, selected, err := workspaceReviewNextCommitRange(commits, "left", "left", "right", []string{"left"})
	if err == nil {
		t.Fatal("expected unrelated branches to be rejected")
	}
	if start != "left" || end != "left" || !reflect.DeepEqual(selected, []string{"left"}) {
		t.Fatalf("unrelated selection changed range = (%q, %q, %v), want original left", start, end, selected)
	}
}

func TestWorkspaceReviewCommitRangeIncludesRootAndMergeSideParents(t *testing.T) {
	commits := []domain.WorkspaceGitCommit{
		{FullHash: "merge", ParentHashes: []string{"first", "side"}},
		{FullHash: "first", ParentHashes: []string{"root"}},
		{FullHash: "side", ParentHashes: []string{"root"}},
		{FullHash: "root"},
	}
	hashes, err := workspaceReviewCommitRange(commits, "root", "merge")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(hashes, []string{"merge", "first", "side", "root"}) {
		t.Fatalf("root..merge = %v, want all reachable commits after root", hashes)
	}
	rootOnly, err := workspaceReviewCommitRange(commits, "root", "root")
	if err != nil || !reflect.DeepEqual(rootOnly, []string{"root"}) {
		t.Fatalf("root singleton = %v, %v; want root", rootOnly, err)
	}
}

func TestWorkspaceReviewRootCommitSnapshotAllowsEmptyBase(t *testing.T) {
	state := &workspaceHistoryState{
		CompareMode:   workspaceReviewCommits,
		CommitHashes:  []string{"root"},
		StartCommit:   "root",
		EndCommit:     "root",
		TargetCommit:  "root-sha",
		DiffSessionID: "root-session",
	}
	if !workspaceReviewSnapshotsReady(state) {
		t.Fatal("root commit with an empty-tree base should have a ready snapshot")
	}
	payload := map[string]any{"compare": map[string]any{
		"mode":                 workspaceReviewCommits,
		"commitHashes":         []string{"root"},
		"startCommit":          "root",
		"endCommit":            "root",
		"expectedBaseCommit":   "",
		"expectedTargetCommit": "root-sha",
	}}
	if !workspaceComparePayloadMatches(state, payload) {
		t.Fatal("root preview must accept and preserve the empty base snapshot identity")
	}
	for _, sessionID := range []string{"", " \t "} {
		withoutSession := *state
		withoutSession.DiffSessionID = sessionID
		if workspaceReviewSnapshotsReady(&withoutSession) {
			t.Fatal("root preview accepted a missing session")
		}
	}
}
