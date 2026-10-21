package workspace

import (
	"context"
	"errors"
	"testing"
)

func TestParseRepositoryPorcelain(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		want       RepositoryStatus
	}{
		{"clean", "", RepositoryStatus{}},
		{"short lines ignored", "\nx\n", RepositoryStatus{}},
		{"staged", "A  added.txt\n", RepositoryStatus{Staged: true}},
		{"unstaged", " M modified.txt\n", RepositoryStatus{Unstaged: true}},
		{"untracked", "?? new.txt\r\n", RepositoryStatus{Untracked: true}},
		{"both columns", "MM both.txt\n", RepositoryStatus{Staged: true, Unstaged: true}},
		{"conflict", "UU conflict.txt\n", RepositoryStatus{Staged: true, Unstaged: true}},
		{"rename path content", "R  old name -> new name\n", RepositoryStatus{Staged: true}},
		{"all with CRLF and Unicode", "A  added.txt\r\n D removed.txt\r\n?? \"中文 文件.txt\"\r\n", RepositoryStatus{Staged: true, Unstaged: true, Untracked: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseRepositoryPorcelain(tc.text); got != tc.want {
				t.Fatalf("porcelain status=%+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestRepositoryStatusConsumersPreserveEnrichment(t *testing.T) {
	repo := newCompareTestRepo(t)
	writeCompareFile(t, repo.path, "tracked.txt", "base\n")
	commitCompare(t, repo.path, "synthetic initial")
	writeCompareFile(t, repo.path, "tracked.txt", "unstaged\n")
	writeCompareFile(t, repo.path, "staged.txt", "staged\n")
	gitCompare(t, repo.path, "add", "staged.txt")
	writeCompareFile(t, repo.path, "untracked.txt", "untracked\n")
	git := NewGitClient(nil, "git")
	want := RepositoryStatus{Staged: true, Unstaged: true, Untracked: true, UnpushedCommits: 1}
	for _, tc := range []struct {
		name string
		run  func() (RepositoryStatus, error)
	}{
		{"ordinary status", func() (RepositoryStatus, error) { return git.Status(repo.path) }},
		{"read-only delete risk", func() (RepositoryStatus, error) {
			return deleteRiskStatusContext(context.Background(), git, repo.path)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.run()
			if err != nil || got != want {
				t.Fatalf("consumer status=%+v error=%v, want %+v", got, err, want)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := deleteRiskStatusContext(ctx, git, repo.path); !errors.Is(err, context.Canceled) {
		t.Fatalf("delete-risk read lost request cancellation: %v", err)
	}
}
