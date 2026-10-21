package workspace

import (
	"container/list"
	"context"
	"reflect"
	"strings"
	"testing"
	"time"
	"unsafe"
)

func TestGitComparePrefetchMatchesForegroundAndKeepsSnapshot(t *testing.T) {
	repo := newCompareTestRepo(t)
	for _, path := range []string{"a.txt", "b.txt", "deleted.txt", "old.txt", "spaced file.txt"} {
		writeCompareFile(t, repo.path, path, "before\n")
	}
	commitCompare(t, repo.path, "base")
	gitCompare(t, repo.path, "checkout", "-b", "feature")
	for _, path := range []string{"a.txt", "b.txt", "spaced file.txt"} {
		writeCompareFile(t, repo.path, path, "after\n")
	}
	writeCompareFile(t, repo.path, "added.txt", "added\n")
	gitCompare(t, repo.path, "rm", "deleted.txt")
	gitCompare(t, repo.path, "mv", "old.txt", "renamed.txt")
	service, ws := repo.serviceWorkspace("feature")
	manager := NewWorkspaceGitDiffSessionManager()
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	request := CompareRequest{Mode: CompareUncommitted}
	files, err := service.FilesSessionContext(context.Background(), manager, "task", "config", ws, request, WorkspaceMainBranch{})
	if err != nil {
		t.Fatal(err)
	}
	request.ExpectedBaseCommit, request.ExpectedTargetCommit = files.BaseCommit, files.TargetCommit
	before := snapshotCompareRepository(t, repo.path)
	if err := service.PrefetchSessionContext(context.Background(), manager, files.SessionID, "task", "config", ws, request, files.Files[0], files.Files, WorkspaceMainBranch{}); err != nil {
		t.Fatal(err)
	}
	session := manager.sessions[files.SessionID]
	for _, change := range files.Files {
		cached, ok := session.cached(change)
		if !ok {
			t.Fatalf("prefetch omitted %s (%s)", change.Path, change.Kind)
		}
		want, err := session.state.preview(context.Background(), change)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(cached, want) {
			t.Fatalf("batch preview differs from single-file preview for %s", change.Path)
		}
	}
	if after := snapshotCompareRepository(t, repo.path); after != before {
		t.Fatal("prefetch mutated source repository")
	}
	writeCompareFile(t, repo.path, files.Files[0].Path, "later content\n")
	preview, err := service.PreviewSessionContext(context.Background(), manager, files.SessionID, "task", "config", ws, request, files.Files[0], WorkspaceMainBranch{})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Content == nil || preview.Content.Text != "after\n" {
		t.Fatalf("prefetched preview lost pinned snapshot: %+v", preview.Content)
	}
	for _, owner := range []string{"other-task", "task"} {
		bad := request
		if owner == "task" {
			bad.ExpectedTargetCommit = "changed"
		}
		if err := service.PrefetchSessionContext(context.Background(), manager, files.SessionID, owner, "config", ws, bad, files.Files[0], files.Files, WorkspaceMainBranch{}); err == nil {
			t.Fatal("prefetch accepted stale owner/snapshot")
		}
	}
	manager.Retire(files.SessionID)
	if err := service.PrefetchSessionContext(context.Background(), manager, files.SessionID, "task", "config", ws, request, files.Files[0], files.Files, WorkspaceMainBranch{}); err == nil {
		t.Fatal("prefetch accepted retired session")
	}
}

func TestGitCompareCacheBudgetIncludesAllocatedRows(t *testing.T) {
	content := &WorkspaceFilePreviewContent{Text: strings.Repeat("\n", 64*1024)}
	comparison := singleFileComparison(content, PreviewAdded)
	preview := WorkspaceGitFilePreview{Content: content, Comparison: comparison}
	minimum := len(content.Text) + cap(comparison.Rows)*int(unsafe.Sizeof(WorkspaceFileComparisonRow{})) + len(comparison.Rows)*int(unsafe.Sizeof(WorkspaceFileComparisonLine{}))
	if got := workspaceGitFilePreviewCacheSize(preview); got < minimum {
		t.Fatalf("cache accounts for %d bytes but row allocations alone require at least %d", got, minimum)
	}
}

func TestGitCompareForegroundCancelsPrefetchAndPinsCurrent(t *testing.T) {
	prefetch, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := &compareSession{cache: map[string]*comparePreviewCacheEntry{}, cacheLRU: list.New(), readGate: make(chan struct{}, 1), prefetchCancel: cancel}
	session.readGate <- struct{}{}
	current := CompareChange{Path: "current.txt", Kind: "MODIFIED"}
	finished := make(chan error, 1)
	go func() {
		end, err := session.beginForeground(context.Background(), current)
		if err == nil {
			end()
		}
		finished <- err
	}()
	select {
	case <-prefetch.Done():
	case <-time.After(time.Second):
		t.Fatal("foreground did not cancel prefetch")
	}
	<-session.readGate
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("foreground did not acquire released gate")
	}
	session.cachePreview(current, WorkspaceGitFilePreview{Content: &WorkspaceFilePreviewContent{Text: "current"}})
	for i := 0; i < comparePreviewCacheFiles+5; i++ {
		session.cachePreview(CompareChange{Path: strings.Repeat("a", i+1), Kind: "ADDED"}, WorkspaceGitFilePreview{Content: &WorkspaceFilePreviewContent{Text: "neighbor"}})
	}
	if _, ok := session.cached(current); !ok {
		t.Fatal("background window evicted foreground file")
	}
	if len(session.cache) != comparePreviewCacheFiles || session.foreground != 0 {
		t.Fatalf("cache/gate not settled: files=%d foreground=%d", len(session.cache), session.foreground)
	}
}
