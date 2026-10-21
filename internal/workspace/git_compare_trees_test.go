package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImmutableTreeCompareReusesRenameBinaryAndBoundedReaders(t *testing.T) {
	repo := newCompareTestRepo(t)
	writeCompareFile(t, repo.path, "old.go", "package old\nconst Keep = 1\nconst Other = 2\n")
	commitCompare(t, repo.path, "base")
	base := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
	gitCompare(t, repo.path, "mv", "old.go", "new.go")
	if err := os.WriteFile(filepath.Join(repo.path, "binary.bin"), []byte{0, 1, 2, 3}, 0600); err != nil {
		t.Fatal(err)
	}
	writeCompareFile(t, repo.path, "large.txt", strings.Repeat("line of code\n", MaxWorkspaceFilePreviewBytes/8))
	commitCompare(t, repo.path, "rename and bounded files")
	target := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
	writeCompareFile(t, repo.path, "untracked.txt", "do not read this\n")
	before := snapshotCompareRepository(t, repo.path)
	reader, _ := repo.serviceWorkspace("master")
	files, _, err := reader.CompareTreesContext(context.Background(), repo.path, base, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertComparePaths(t, files.Files, "binary.bin", "large.txt", "new.go")
	for _, change := range files.Files {
		_, preview, err := reader.CompareTreesContext(context.Background(), repo.path, base, target, &change)
		if err != nil || preview == nil {
			t.Fatalf("read %s: %v", change.Path, err)
		}
		if change.Path == "new.go" && (change.OldPath != "old.go" || preview.Content == nil || !strings.Contains(preview.Content.Text, "package old")) {
			t.Fatal("rename lost source contents")
		}
		if change.Path == "large.txt" && preview.Content != nil && len(preview.Content.Text) > MaxWorkspaceFilePreviewBytes {
			t.Fatal("large preview exceeded bound")
		}
	}
	if after := snapshotCompareRepository(t, repo.path); after != before {
		t.Fatal("immutable diff touched checkout or Git refs")
	}
	for _, sha := range []string{"HEAD", base[:10], "--all"} {
		if _, _, err := reader.CompareTreesContext(context.Background(), repo.path, sha, target, nil); err == nil {
			t.Fatal("reader accepted mutable revision")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := reader.CompareTreesContext(ctx, repo.path, base, target, nil); err == nil {
		t.Fatal("cancelled reader ran Git")
	}
}
