package workspace

import (
	"context"
	"strings"
	"testing"
)

func TestGitCompareWorkingTreeIncludesIgnoredTrackedFiles(t *testing.T) {
	repo := newCompareTestRepo(t)
	writeCompareFile(t, repo.path, ".gitignore", "*.ignored\n")
	writeCompareFile(t, repo.path, "base.txt", "base\n")
	commitCompare(t, repo.path, "base")
	main := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
	gitCompare(t, repo.path, "checkout", "-b", "feature")
	writeCompareFile(t, repo.path, "committed.ignored", "committed\n")
	gitCompare(t, repo.path, "add", "-f", "committed.ignored")
	gitCompare(t, repo.path, "commit", "-m", "track ignored file")
	gitCompare(t, repo.path, "update-ref", "refs/remotes/origin/main", main)
	writeCompareFile(t, repo.path, "staged.ignored", "staged\n")
	gitCompare(t, repo.path, "add", "-f", "staged.ignored")
	writeCompareFile(t, repo.path, "staged.ignored", "working tree after stage\n")
	writeCompareFile(t, repo.path, "untracked.ignored", "must remain ignored\n")
	service, ws := repo.serviceWorkspace("feature")
	mainBranch := WorkspaceMainBranch{DisplayRef: "origin/main", LocalRef: "origin/main"}
	before := snapshotCompareRepository(t, repo.path)
	for _, tc := range []struct {
		name    string
		request CompareRequest
		want    []string
	}{
		{"local", CompareRequest{Mode: CompareUncommitted}, []string{"staged.ignored"}},
		{"mainline", CompareRequest{Mode: CompareMainline, IncludeWorkingTree: true}, []string{"committed.ignored", "staged.ignored"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files, err := service.FilesContext(context.Background(), ws, tc.request, mainBranch)
			if err != nil {
				t.Fatal(err)
			}
			assertComparePaths(t, files.Files, tc.want...)
			for _, change := range files.Files {
				if change.Path != "staged.ignored" {
					continue
				}
				preview, err := service.PreviewContext(context.Background(), ws, tc.request, change, mainBranch)
				if err != nil {
					t.Fatal(err)
				}
				if preview.Content == nil || preview.Content.Text != "working tree after stage\n" {
					t.Fatalf("ignored tracked file did not use the working tree: %+v", preview)
				}
			}
		})
	}
	if after := snapshotCompareRepository(t, repo.path); after != before {
		t.Fatal("review modified the source repository")
	}
}

func TestGitCompareZeroLengthHunkKeepsExactContext(t *testing.T) {
	for _, tc := range []struct {
		name, old, current, patch string
		wantChangeAt              int
		wantKind                  WorkspaceFileComparisonLineKind
	}{
		{"insert middle", "a\nb\n", "a\nx\nb\n", "@@ -1,0 +2 @@\n+x\n", 1, PreviewAdded},
		{"delete middle", "a\nx\nb\n", "a\nb\n", "@@ -2 +1,0 @@\n-x\n", 1, PreviewDeleted},
		{"insert end", "a\nb\n", "a\nb\nx\n", "@@ -2,0 +3 @@\n+x\n", 2, PreviewAdded},
		{"delete end", "a\nb\nx\n", "a\nb\n", "@@ -3 +2,0 @@\n-x\n", 2, PreviewDeleted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			comparison := buildFileComparison(&WorkspaceFilePreviewContent{Text: tc.old}, &WorkspaceFilePreviewContent{Text: tc.current}, parseDiffHunks(tc.patch))
			if comparison == nil {
				t.Fatal("ordinary insertion/deletion returned no comparison")
			}
			oldLines, newLines := []string{}, []string{}
			for i, row := range comparison.Rows {
				if row.OldLine != nil {
					oldLines = append(oldLines, row.OldLine.Text)
				}
				if row.NewLine != nil {
					newLines = append(newLines, row.NewLine.Text)
				}
				if i == tc.wantChangeAt {
					if tc.wantKind == PreviewAdded && (row.OldLine != nil || row.NewLine == nil || row.NewLine.Kind != PreviewAdded || row.NewLine.Text != "x") {
						t.Fatalf("inserted row = %+v", row)
					}
					if tc.wantKind == PreviewDeleted && (row.NewLine != nil || row.OldLine == nil || row.OldLine.Kind != PreviewDeleted || row.OldLine.Text != "x") {
						t.Fatalf("deleted row = %+v", row)
					}
				} else if row.OldLine == nil || row.NewLine == nil || row.OldLine.Text != row.NewLine.Text || row.OldLine.Kind != PreviewContext || row.NewLine.Kind != PreviewContext {
					t.Fatalf("unchanged row %d was misaligned: %+v", i, row)
				}
			}
			if strings.Join(oldLines, "\n")+"\n" != tc.old || strings.Join(newLines, "\n")+"\n" != tc.current {
				t.Fatal("comparison lost original lines")
			}
		})
	}
}
