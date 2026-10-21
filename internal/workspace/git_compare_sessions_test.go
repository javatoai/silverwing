package workspace

import (
	"container/list"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGitCompareSessionUsesFixedSnapshotAndValidatesIdentity(t *testing.T) {
	repo := newCompareTestRepo(t)
	writeCompareFile(t, repo.path, "src/a.txt", "base\n")
	commitCompare(t, repo.path, "base")
	gitCompare(t, repo.path, "checkout", "-b", "feature")
	writeCompareFile(t, repo.path, "src/a.txt", "snapshot\n")
	service, ws := repo.serviceWorkspace("feature")
	manager := NewWorkspaceGitDiffSessionManager()
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	request := CompareRequest{Mode: CompareUncommitted}
	files, err := service.FilesSessionContext(context.Background(), manager, "task-one", "config-one", ws, request, WorkspaceMainBranch{})
	if err != nil {
		t.Fatal(err)
	}
	if len(files.Files) != 1 || files.SessionID == "" {
		t.Fatalf("session result = %+v", files)
	}
	request.ExpectedBaseCommit, request.ExpectedTargetCommit = files.BaseCommit, files.TargetCommit
	writeCompareFile(t, repo.path, "src/a.txt", "later working tree\n")

	preview, err := service.PreviewSessionContext(context.Background(), manager, files.SessionID, "task-one", "config-one", ws, request, files.Files[0], WorkspaceMainBranch{})
	if err != nil {
		t.Fatal(err)
	}
	if preview.Content == nil || preview.Content.Text != "snapshot\n" {
		t.Fatalf("preview read a different snapshot: %+v", preview.Content)
	}
	if _, err := service.PreviewSessionContext(context.Background(), manager, files.SessionID, "task-two", "config-one", ws, request, files.Files[0], WorkspaceMainBranch{}); err == nil {
		t.Fatal("preview accepted a different task identity")
	}
	wrongSnapshot := request
	wrongSnapshot.ExpectedTargetCommit = "worktree:other-snapshot"
	if _, err := service.PreviewSessionContext(context.Background(), manager, files.SessionID, "task-one", "config-one", ws, wrongSnapshot, files.Files[0], WorkspaceMainBranch{}); err == nil {
		t.Fatal("preview accepted a different snapshot identity")
	}
	if _, err := service.PreviewSessionContext(context.Background(), manager, files.SessionID, "task-one", "changed-config", ws, request, files.Files[0], WorkspaceMainBranch{}); err == nil {
		t.Fatal("preview accepted a different configuration identity")
	}
	if _, err := service.PreviewSessionContext(context.Background(), manager, files.SessionID, "task-one", "config-one", ws, request, CompareChange{Path: "src/other.txt", Kind: string("MODIFIED")}, WorkspaceMainBranch{}); err == nil {
		t.Fatal("preview accepted a file outside the session")
	}
}

func TestGitCompareSessionRootCommitUsesEmptyTree(t *testing.T) {
	repo := newCompareTestRepo(t)
	writeCompareFile(t, repo.path, "root.txt", "root\n")
	commitCompare(t, repo.path, "root")
	root := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
	gitCompare(t, repo.path, "branch", "-m", "feature")
	service, ws := repo.serviceWorkspace("feature")
	manager := NewWorkspaceGitDiffSessionManager()
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	request := CompareRequest{Mode: CompareCommits, StartCommit: root, EndCommit: root}
	before := snapshotCompareRepository(t, repo.path)
	files, err := service.FilesSessionContext(context.Background(), manager, "task", "config", ws, request, WorkspaceMainBranch{})
	if err != nil {
		t.Fatal(err)
	}
	if files.SessionID == "" || files.BaseCommit != "" || files.TargetCommit != root {
		t.Fatalf("root session = %+v; want a valid session, empty base and root target", files)
	}
	assertComparePaths(t, files.Files, "root.txt")
	request.ExpectedBaseCommit, request.ExpectedTargetCommit = files.BaseCommit, files.TargetCommit
	preview, err := service.PreviewSessionContext(context.Background(), manager, files.SessionID, "task", "config", ws, request, files.Files[0], WorkspaceMainBranch{})
	if err != nil || preview.Content == nil || preview.Content.Text != "root\n" {
		t.Fatalf("root session preview = %+v, %v; want the added root file", preview, err)
	}
	manager.Retire(files.SessionID)
	if after := snapshotCompareRepository(t, repo.path); after != before {
		t.Fatal("root session review changed source files, index, refs, or object database")
	}
}

func TestGitCompareSessionCacheIsBoundedAndSkipsOversize(t *testing.T) {
	session := &compareSession{cache: map[string]*comparePreviewCacheEntry{}, cacheLRU: list.New()}
	for index := 0; index < comparePreviewCacheFiles+1; index++ {
		change := CompareChange{Path: filepath.ToSlash(filepath.Join("src", strings.Repeat("a", index+1)+".txt")), Kind: "MODIFIED"}
		session.cachePreview(change, WorkspaceGitFilePreview{Content: &WorkspaceFilePreviewContent{Text: "ok"}})
	}
	if len(session.cache) != comparePreviewCacheFiles || session.cacheLRU.Len() != comparePreviewCacheFiles {
		t.Fatalf("cache size = %d / %d, want %d entries", len(session.cache), session.cacheLRU.Len(), comparePreviewCacheFiles)
	}
	if session.cacheSize > comparePreviewCacheBytes {
		t.Fatalf("cache bytes = %d, over %d", session.cacheSize, comparePreviewCacheBytes)
	}
	change := CompareChange{Path: "large.txt", Kind: "MODIFIED"}
	session.cachePreview(change, WorkspaceGitFilePreview{Content: &WorkspaceFilePreviewContent{Text: strings.Repeat("x", comparePreviewCacheBytes+1)}})
	if _, found := session.cached(change); found {
		t.Fatal("oversize preview was cached")
	}

	byteSession := &compareSession{cache: map[string]*comparePreviewCacheEntry{}, cacheLRU: list.New()}
	threeMiB := strings.Repeat("x", 3*1024*1024)
	for index := 0; index < 12; index++ {
		change := CompareChange{Path: filepath.ToSlash(filepath.Join("src", strings.Repeat("b", index+1)+".txt")), Kind: "MODIFIED"}
		byteSession.cachePreview(change, WorkspaceGitFilePreview{Content: &WorkspaceFilePreviewContent{Text: threeMiB}})
	}
	if len(byteSession.cache) != 10 || byteSession.cacheLRU.Len() != 10 || byteSession.cacheSize > comparePreviewCacheBytes {
		t.Fatalf("byte-budget eviction retained %d entries / %d bytes, want 10 entries within %d bytes", len(byteSession.cache), byteSession.cacheSize, comparePreviewCacheBytes)
	}
	oldest := CompareChange{Path: filepath.ToSlash(filepath.Join("src", "b.txt")), Kind: "MODIFIED"}
	if _, found := byteSession.cached(oldest); found {
		t.Fatal("byte-budget eviction retained the oldest preview")
	}

	canceledSession := &compareSession{state: &compareState{root: t.TempDir(), service: &WorkspaceGitDiffService{}}}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := canceledSession.preview(canceled, CompareChange{Path: "src/a.txt", Kind: "MODIFIED", baseBlob: strings.Repeat("a", 40), targetBlob: strings.Repeat("b", 40)}); err == nil {
		t.Fatal("canceled preview unexpectedly succeeded")
	}
	if _, found := canceledSession.cached(CompareChange{Path: "src/a.txt", Kind: "MODIFIED"}); found {
		t.Fatal("failed preview was cached")
	}
}

func TestGitCompareSessionUsesAllScopesWithoutMutatingSourceRepository(t *testing.T) {
	repo := newCompareTestRepo(t)
	writeCompareFile(t, repo.path, "base.txt", "base\n")
	commitCompare(t, repo.path, "base")
	main := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
	gitCompare(t, repo.path, "checkout", "-b", "feature")
	writeCompareFile(t, repo.path, "a.txt", "A\n")
	commitCompare(t, repo.path, "commit A")
	commitA := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
	writeCompareFile(t, repo.path, "b.txt", "B\n")
	commitCompare(t, repo.path, "commit B")
	commitB := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
	gitCompare(t, repo.path, "update-ref", "refs/remotes/origin/master", main)
	writeCompareFile(t, repo.path, "base.txt", "dirty\n")
	writeCompareFile(t, repo.path, "staged.txt", "staged\n")
	gitCompare(t, repo.path, "add", "staged.txt")
	writeCompareFile(t, repo.path, "untracked.txt", "untracked\n")

	service, ws := repo.serviceWorkspace("feature")
	manager := NewWorkspaceGitDiffSessionManager()
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	mainBranch := WorkspaceMainBranch{DisplayRef: "origin/master", LocalRef: "origin/master"}
	cases := []struct {
		name    string
		request CompareRequest
		want    []string
	}{
		{name: "overall", request: CompareRequest{Mode: CompareMainline}, want: []string{"a.txt", "b.txt"}},
		{name: "uncommitted", request: CompareRequest{Mode: CompareUncommitted}, want: []string{"base.txt", "staged.txt", "untracked.txt"}},
		{name: "commit range", request: CompareRequest{Mode: CompareCommits, StartCommit: commitA, EndCommit: commitB}, want: []string{"a.txt", "b.txt"}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			before := snapshotCompareRepository(t, repo.path)
			files, err := service.FilesSessionContext(context.Background(), manager, "task", "config", ws, testCase.request, mainBranch)
			if err != nil {
				t.Fatal(err)
			}
			assertComparePaths(t, files.Files, testCase.want...)
			testCase.request.ExpectedBaseCommit = files.BaseCommit
			testCase.request.ExpectedTargetCommit = files.TargetCommit
			preview, err := service.PreviewSessionContext(context.Background(), manager, files.SessionID, "task", "config", ws, testCase.request, files.Files[0], mainBranch)
			if err != nil {
				t.Fatal(err)
			}
			if preview.Comparison == nil {
				t.Fatalf("scope %s returned no code comparison: %+v", testCase.name, preview)
			}
			manager.Retire(files.SessionID)
			if after := snapshotCompareRepository(t, repo.path); after != before {
				t.Fatalf("%s review changed source files, index, refs, or object database", testCase.name)
			}
		})
	}
}

func TestGitCompareSessionCacheRejectsBranchChange(t *testing.T) {
	repo := newCompareTestRepo(t)
	writeCompareFile(t, repo.path, "src/a.txt", "base\n")
	commitCompare(t, repo.path, "base")
	gitCompare(t, repo.path, "checkout", "-b", "feature")
	writeCompareFile(t, repo.path, "src/a.txt", "feature\n")
	service, ws := repo.serviceWorkspace("feature")
	manager := NewWorkspaceGitDiffSessionManager()
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	request := CompareRequest{Mode: CompareUncommitted}
	files, err := service.FilesSessionContext(context.Background(), manager, "task", "config", ws, request, WorkspaceMainBranch{})
	if err != nil {
		t.Fatal(err)
	}
	request.ExpectedBaseCommit, request.ExpectedTargetCommit = files.BaseCommit, files.TargetCommit
	if _, err := service.PreviewSessionContext(context.Background(), manager, files.SessionID, "task", "config", ws, request, files.Files[0], WorkspaceMainBranch{}); err != nil {
		t.Fatal(err)
	}
	gitCompare(t, repo.path, "branch", "other")
	gitCompare(t, repo.path, "checkout", "other")
	if _, err := service.PreviewSessionContext(context.Background(), manager, files.SessionID, "task", "config", ws, request, files.Files[0], WorkspaceMainBranch{}); err == nil {
		t.Fatal("cached preview accepted a repository after its active branch changed")
	}
}

func TestGitCompareSessionRetireWaitsForReadersBeforeRemovingSnapshot(t *testing.T) {
	manager := NewWorkspaceGitDiffSessionManager()
	temporary := t.TempDir()
	marker := filepath.Join(temporary, "marker")
	if err := os.WriteFile(marker, []byte("session"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := &compareState{tempDir: temporary}
	id, err := manager.register("owner", state, nil)
	if err != nil {
		t.Fatal(err)
	}
	readCtx, session, release, err := manager.acquire(context.Background(), id, "owner")
	if err != nil {
		t.Fatal(err)
	}
	manager.Retire(id)
	select {
	case <-readCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("retiring session did not cancel its active read")
	}
	manager.mu.Lock()
	wasClosed := session.closed
	manager.mu.Unlock()
	if wasClosed {
		t.Fatal("session closed while a reader still held a reference")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("snapshot removed before reader release: %v", err)
	}
	release()
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("snapshot still exists after final reader release: %v", err)
	}
	if err := manager.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestGitCompareSessionCloseWaitsUntilActiveReaderReleases(t *testing.T) {
	manager := NewWorkspaceGitDiffSessionManager()
	temporary := t.TempDir()
	state := &compareState{tempDir: temporary}
	id, err := manager.register("owner", state, nil)
	if err != nil {
		t.Fatal(err)
	}
	readCtx, _, release, err := manager.acquire(context.Background(), id, "owner")
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() { closed <- manager.Close(context.Background()) }()
	select {
	case <-readCtx.Done():
	case <-time.After(time.Second):
		t.Fatal("manager close did not cancel the active reader")
	}
	select {
	case err := <-closed:
		t.Fatalf("manager close returned before reader release: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	release()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("manager close did not finish after reader release")
	}
	if _, err := os.Stat(temporary); !os.IsNotExist(err) {
		t.Fatalf("snapshot still exists after manager close: %v", err)
	}
}

func TestGitCompareSessionManagerCloseJoinsConcurrentRetireCleanup(t *testing.T) {
	manager := NewWorkspaceGitDiffSessionManager()
	cleanupStarted := make(chan struct{})
	allowCleanup := make(chan struct{})
	manager.closeStateFn = func(state *compareState) {
		close(cleanupStarted)
		<-allowCleanup
		state.close()
	}
	id, err := manager.register("owner", &compareState{tempDir: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	retireDone := make(chan struct{})
	go func() {
		manager.Retire(id)
		close(retireDone)
	}()
	select {
	case <-cleanupStarted:
	case <-time.After(time.Second):
		t.Fatal("retire did not start cleanup")
	}
	closeDone := make(chan error, 1)
	go func() { closeDone <- manager.Close(context.Background()) }()
	select {
	case err := <-closeDone:
		t.Fatalf("manager close returned during concurrent cleanup: %v", err)
	case <-time.After(25 * time.Millisecond):
	}
	close(allowCleanup)
	select {
	case <-retireDone:
	case <-time.After(time.Second):
		t.Fatal("retire did not finish after cleanup was released")
	}
	select {
	case err := <-closeDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("manager close did not finish after cleanup completed")
	}
}
