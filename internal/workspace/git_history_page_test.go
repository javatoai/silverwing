package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestGitHistoryPagesAnchorSHAsAndPreserveLegacyFullMessages(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "missing"))
	repo := newCompareTestRepo(t)
	gitCompare(t, repo.path, "commit", "--allow-empty", "-m", "baseline")
	base := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
	gitCompare(t, repo.path, "update-ref", "refs/remotes/origin/main", base)
	for index := 0; index < 7; index++ {
		gitCompare(t, repo.path, "commit", "--allow-empty", "-m", fmt.Sprintf("subject-%d", index), "-m", "full body only read when expanded")
	}
	head := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
	reader := NewWorkspaceGitHistoryService(NewGitClient(nil, "git"))
	_, trace := performanceGitTrace(t)
	request := WorkspaceGitHistoryPageRequest{Limit: 2}
	seen := map[string]bool{}
	var first WorkspaceGitHistoryPage
	pages := 0
	for {
		page, err := reader.ReadPageContext(context.Background(), repo.path, "origin/main", request)
		if err != nil {
			t.Fatal(err)
		}
		if pages == 0 {
			first = page
		}
		pages++
		if page.HeadSHA != head || page.BaselineSHA != base || len(page.Commits) > 2 || !page.SummariesOnly {
			t.Fatalf("unbounded or unanchored page: %+v", page)
		}
		for _, commit := range page.Commits {
			if seen[commit.FullHash] || strings.Contains(commit.Message, "full body") {
				t.Fatalf("duplicate/full-message row: %+v", commit)
			}
			seen[commit.FullHash] = true
		}
		if !page.HasMore {
			break
		}
		request = WorkspaceGitHistoryPageRequest{HeadSHA: head, BaselineSHA: base, Offset: page.NextOffset, Limit: 2}
	}
	if len(seen) != 7 || pages != 4 {
		t.Fatalf("history silently truncated: %d commits across %d pages", len(seen), pages)
	}
	logCommands := 0
	for _, args := range trace() {
		if slices.Contains(args, "log") {
			logCommands++
			if !slices.Contains(args, "--max-count=3") || !slices.Contains(args, base+".."+head) {
				t.Fatalf("history page used moving refs or unbounded count: %v", args)
			}
		}
	}
	if logCommands != pages {
		t.Fatalf("log command count = %d, want one per %d pages", logCommands, pages)
	}
	detail, err := reader.ReadMessageContext(context.Background(), repo.path, "origin/main", WorkspaceGitHistoryPageRequest{HeadSHA: head, BaselineSHA: base}, first.Commits[0].FullHash)
	if err != nil || detail.Truncated || !strings.Contains(detail.Message, "full body only read when expanded") {
		t.Fatalf("expanded message lost the body: %+v, %v", detail, err)
	}
	legacy, err := reader.ReadContext(context.Background(), repo.path, "origin/main")
	if err != nil || len(legacy) != len(seen) || !strings.Contains(legacy[0].Message, "full body") {
		t.Fatalf("legacy full-message contract changed: %d, %v", len(legacy), err)
	}
	if _, err := reader.ReadMessageContext(context.Background(), repo.path, "origin/main", WorkspaceGitHistoryPageRequest{HeadSHA: head, BaselineSHA: base}, base); err == nil {
		t.Fatal("message reader accepted a commit excluded by the baseline")
	}
	gitCompare(t, repo.path, "commit", "--allow-empty", "-m", "external HEAD move")
	if _, err := reader.ReadPageContext(context.Background(), repo.path, "origin/main", WorkspaceGitHistoryPageRequest{HeadSHA: head, BaselineSHA: base, Offset: 2, Limit: 2}); !errors.Is(err, ErrWorkspaceGitHistoryChanged) {
		t.Fatalf("moving HEAD reused a stale page: %v", err)
	}
	newHead := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
	gitCompare(t, repo.path, "update-ref", "refs/remotes/origin/main", newHead)
	if _, err := reader.ReadPageContext(context.Background(), repo.path, "origin/main", WorkspaceGitHistoryPageRequest{HeadSHA: newHead, BaselineSHA: base, Offset: 2, Limit: 2}); !errors.Is(err, ErrWorkspaceGitHistoryChanged) {
		t.Fatalf("moving baseline reused a stale page: %v", err)
	}
}

func TestGitHistoryMessageBudgetIsExplicitAndPageParserRejectsPartialOutput(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "missing"))
	repo := newCompareTestRepo(t)
	messagePath := filepath.Join(t.TempDir(), "message.txt")
	message := "large subject\n\n" + strings.Repeat("body ", historyMessageOutputBytes/4)
	if err := os.WriteFile(messagePath, []byte(message), 0600); err != nil {
		t.Fatal(err)
	}
	gitCompare(t, repo.path, "commit", "--allow-empty", "-F", messagePath)
	reader := NewWorkspaceGitHistoryService(NewGitClient(nil, "git"))
	page, err := reader.ReadPageContext(context.Background(), repo.path, "", WorkspaceGitHistoryPageRequest{Limit: 2})
	if err != nil || len(page.Commits) != 1 || page.HasMore {
		t.Fatalf("long body broke bounded subject page: %+v, %v", page, err)
	}
	detail, err := reader.ReadMessageContext(context.Background(), repo.path, "", WorkspaceGitHistoryPageRequest{HeadSHA: page.HeadSHA}, page.Commits[0].FullHash)
	if err != nil || !detail.Truncated || detail.Notice == "" || len(detail.Message) > historyMessageOutputBytes {
		t.Fatalf("message limit became complete output: bytes=%d truncated=%v notice=%q err=%v", len(detail.Message), detail.Truncated, detail.Notice, err)
	}
	for _, output := range []string{"incomplete", strings.Repeat("a", 40) + "\x00parent\x00"} {
		if _, err := parseGitHistoryPage(output, 3); err == nil {
			t.Fatalf("accepted partial Git output %q", output)
		}
	}
	for _, request := range []WorkspaceGitHistoryPageRequest{{Offset: -1}, {Offset: 10}, {HeadSHA: "HEAD"}, {Limit: 201}} {
		if _, err := reader.ReadPageContext(context.Background(), repo.path, "", request); err == nil {
			t.Fatalf("accepted invalid page request: %+v", request)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := reader.ReadPageContext(ctx, repo.path, "", WorkspaceGitHistoryPageRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("page ignored cancellation: %v", err)
	}
}
