package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
)

func TestCompareMainlineCommittedAndOptionalWorkingTree(t *testing.T) {
	repo := newCompareTestRepo(t)
	writeCompareFile(t, repo.path, "base.txt", "base\n")
	commitCompare(t, repo.path, "base")
	main := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
	gitCompare(t, repo.path, "checkout", "-b", "feature")
	writeCompareFile(t, repo.path, "committed.txt", "committed\n")
	commitCompare(t, repo.path, "feature commit")
	gitCompare(t, repo.path, "update-ref", "refs/remotes/origin/main", main)

	writeCompareFile(t, repo.path, "base.txt", "dirty\n")
	writeCompareFile(t, repo.path, "staged.txt", "staged\n")
	gitCompare(t, repo.path, "add", "staged.txt")
	writeCompareFile(t, repo.path, "untracked.txt", "untracked\n")

	service, ws := repo.serviceWorkspace("feature")
	mainBranch := WorkspaceMainBranch{DisplayRef: "origin/main", LocalRef: "origin/main"}
	committed, err := service.FilesContext(context.Background(), ws, CompareRequest{Mode: CompareMainline}, mainBranch)
	if err != nil {
		t.Fatal(err)
	}
	assertComparePaths(t, committed.Files, "committed.txt")
	if committed.TargetCommit != gitCompareOutput(t, repo.path, "rev-parse", "HEAD") {
		t.Fatalf("committed mainline target = %q, want current HEAD", committed.TargetCommit)
	}

	withWorkingTree, err := service.FilesContext(context.Background(), ws, CompareRequest{Mode: CompareMainline, IncludeWorkingTree: true}, mainBranch)
	if err != nil {
		t.Fatal(err)
	}
	assertComparePaths(t, withWorkingTree.Files, "base.txt", "committed.txt", "staged.txt", "untracked.txt")
	if !strings.HasPrefix(withWorkingTree.TargetCommit, "worktree:") {
		t.Fatalf("working-tree snapshot identity = %q, want worktree identity", withWorkingTree.TargetCommit)
	}
}

func TestCompareUncommittedContainsOnlyWorktreeChanges(t *testing.T) {
	repo := newCompareTestRepo(t)
	writeCompareFile(t, repo.path, "base.txt", "base\n")
	commitCompare(t, repo.path, "base")
	gitCompare(t, repo.path, "checkout", "-b", "feature")
	writeCompareFile(t, repo.path, "committed.txt", "committed\n")
	commitCompare(t, repo.path, "feature commit")
	writeCompareFile(t, repo.path, "base.txt", "dirty\n")
	writeCompareFile(t, repo.path, "staged.txt", "staged\n")
	gitCompare(t, repo.path, "add", "staged.txt")
	writeCompareFile(t, repo.path, "untracked.txt", "untracked\n")

	service, ws := repo.serviceWorkspace("feature")
	result, err := service.FilesContext(context.Background(), ws, CompareRequest{Mode: CompareUncommitted}, WorkspaceMainBranch{})
	if err != nil {
		t.Fatal(err)
	}
	assertComparePaths(t, result.Files, "base.txt", "staged.txt", "untracked.txt")
	if result.BaseCommit != gitCompareOutput(t, repo.path, "rev-parse", "HEAD") {
		t.Fatalf("uncommitted base = %q, want HEAD", result.BaseCommit)
	}
}

func TestCompareMissingMainlineDoesNotFetchOrMutateRepository(t *testing.T) {
	repo := newCompareTestRepo(t)
	writeCompareFile(t, repo.path, "base.txt", "base\n")
	commitCompare(t, repo.path, "base")
	gitCompare(t, repo.path, "checkout", "-b", "feature")
	service, ws := repo.serviceWorkspace("feature")
	before := snapshotCompareRepository(t, repo.path)
	_, err := service.FilesContext(context.Background(), ws, CompareRequest{Mode: CompareMainline}, WorkspaceMainBranch{DisplayRef: "origin/main", LocalRef: "origin/main"})
	if err == nil || !strings.Contains(err.Error(), "未执行自动 fetch") {
		t.Fatalf("missing baseline error = %v", err)
	}
	if after := snapshotCompareRepository(t, repo.path); after != before {
		t.Fatal("missing-baseline review changed the source repository")
	}
}

func TestCompareCommitRangeIncludesMiddleAndSupportsLegacyEndpoints(t *testing.T) {
	repo := newCompareTestRepo(t)
	writeCompareFile(t, repo.path, "base.txt", "base\n")
	commitCompare(t, repo.path, "base")
	gitCompare(t, repo.path, "checkout", "-b", "feature")

	writeCompareFile(t, repo.path, "a.txt", "a\n")
	commitCompare(t, repo.path, "A")
	a := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
	writeCompareFile(t, repo.path, "b.txt", "b\n")
	commitCompare(t, repo.path, "B")
	writeCompareFile(t, repo.path, "c.txt", "c\n")
	commitCompare(t, repo.path, "C")
	c := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
	service, ws := repo.serviceWorkspace("feature")

	result, err := service.FilesContext(context.Background(), ws, CompareRequest{Mode: CompareCommits, StartCommit: a, EndCommit: c}, WorkspaceMainBranch{})
	if err != nil {
		t.Fatal(err)
	}
	assertComparePaths(t, result.Files, "a.txt", "b.txt", "c.txt")
	if result.BaseCommit != gitCompareOutput(t, repo.path, "rev-parse", a+"^") || result.TargetCommit != c {
		t.Fatalf("range commits = base %q target %q", result.BaseCommit, result.TargetCommit)
	}

	legacy, err := service.FilesContext(context.Background(), ws, CompareRequest{Mode: CompareCommits, CommitHashes: []string{c, a}}, WorkspaceMainBranch{})
	if err != nil {
		t.Fatal(err)
	}
	assertComparePaths(t, legacy.Files, "a.txt", "b.txt", "c.txt")

	single, err := service.FilesContext(context.Background(), ws, CompareRequest{Mode: CompareCommits, StartCommit: a, EndCommit: a}, WorkspaceMainBranch{})
	if err != nil {
		t.Fatal(err)
	}
	assertComparePaths(t, single.Files, "a.txt")
}

func TestCompareCommitRangeRootAndMergeUseFirstParent(t *testing.T) {
	t.Run("root commit uses empty tree", func(t *testing.T) {
		repo := newCompareTestRepo(t)
		writeCompareFile(t, repo.path, "root.txt", "root\n")
		commitCompare(t, repo.path, "root")
		root := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
		gitCompare(t, repo.path, "branch", "-m", "feature")
		service, ws := repo.serviceWorkspace("feature")
		result, err := service.FilesContext(context.Background(), ws, CompareRequest{Mode: CompareCommits, StartCommit: root, EndCommit: root}, WorkspaceMainBranch{})
		if err != nil {
			t.Fatal(err)
		}
		assertComparePaths(t, result.Files, "root.txt")
		if result.BaseCommit != "" {
			t.Fatalf("root baseline = %q, want empty", result.BaseCommit)
		}
		preview, err := service.PreviewContext(context.Background(), ws, CompareRequest{
			Mode: CompareCommits, StartCommit: root, EndCommit: root,
			ExpectedBaseCommit: result.BaseCommit, ExpectedTargetCommit: result.TargetCommit,
		}, result.Files[0], WorkspaceMainBranch{})
		if err != nil || preview.Content == nil || preview.Content.Text != "root\n" {
			t.Fatalf("root preview = %#v, %v; want the added root file", preview, err)
		}
	})

	t.Run("merge commit compares with first parent", func(t *testing.T) {
		repo := newCompareTestRepo(t)
		writeCompareFile(t, repo.path, "base.txt", "base\n")
		commitCompare(t, repo.path, "base")
		base := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
		gitCompare(t, repo.path, "checkout", "-b", "feature")
		writeCompareFile(t, repo.path, "feature.txt", "feature\n")
		commitCompare(t, repo.path, "feature")
		firstParent := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
		gitCompare(t, repo.path, "checkout", "-b", "side", base)
		writeCompareFile(t, repo.path, "side.txt", "side\n")
		commitCompare(t, repo.path, "side")
		gitCompare(t, repo.path, "checkout", "feature")
		gitCompare(t, repo.path, "merge", "--no-ff", "side", "-m", "merge")
		merge := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
		service, ws := repo.serviceWorkspace("feature")
		result, err := service.FilesContext(context.Background(), ws, CompareRequest{Mode: CompareCommits, StartCommit: merge, EndCommit: merge}, WorkspaceMainBranch{})
		if err != nil {
			t.Fatal(err)
		}
		assertComparePaths(t, result.Files, "side.txt")
		if result.BaseCommit != firstParent {
			t.Fatalf("merge baseline = %q, want first parent %q", result.BaseCommit, firstParent)
		}
	})
}

func TestCompareCommitRangeRejectsIncomparableEndpoints(t *testing.T) {
	repo := newCompareTestRepo(t)
	writeCompareFile(t, repo.path, "base.txt", "base\n")
	commitCompare(t, repo.path, "base")
	base := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
	gitCompare(t, repo.path, "checkout", "-b", "feature")
	writeCompareFile(t, repo.path, "feature.txt", "feature\n")
	commitCompare(t, repo.path, "feature")
	feature := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
	gitCompare(t, repo.path, "checkout", "-b", "side", base)
	writeCompareFile(t, repo.path, "side.txt", "side\n")
	commitCompare(t, repo.path, "side")
	side := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
	gitCompare(t, repo.path, "checkout", "feature")
	gitCompare(t, repo.path, "merge", "--no-ff", "side", "-m", "merge")
	service, ws := repo.serviceWorkspace("feature")
	_, err := service.FilesContext(context.Background(), ws, CompareRequest{Mode: CompareCommits, StartCommit: feature, EndCommit: side}, WorkspaceMainBranch{})
	if err == nil || !strings.Contains(err.Error(), "没有祖先关系") {
		t.Fatalf("incomparable range error = %v", err)
	}
}

func TestCompareCommitRangeHasNo64CommitLimit(t *testing.T) {
	repo := newCompareTestRepo(t)
	writeCompareFile(t, repo.path, "base.txt", "base\n")
	commitCompare(t, repo.path, "base")
	gitCompare(t, repo.path, "checkout", "-b", "feature")
	var first string
	for i := 0; i < 70; i++ {
		path := fmt.Sprintf("changes/%02d.txt", i)
		writeCompareFile(t, repo.path, path, fmt.Sprintf("%d\n", i))
		commitCompare(t, repo.path, fmt.Sprintf("commit %d", i))
		if i == 0 {
			first = gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
		}
	}
	last := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
	service, ws := repo.serviceWorkspace("feature")
	result, err := service.FilesContext(context.Background(), ws, CompareRequest{Mode: CompareCommits, StartCommit: first, EndCommit: last}, WorkspaceMainBranch{})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Files) != 70 {
		t.Fatalf("range files = %d, want 70", len(result.Files))
	}
}

func TestCompareSimulationConflictLeavesSourceRepositoryUnchanged(t *testing.T) {
	repo := newCompareTestRepo(t)
	writeCompareFile(t, repo.path, "same.txt", "base\n")
	commitCompare(t, repo.path, "base")
	gitCompare(t, repo.path, "checkout", "-b", "feature")
	writeCompareFile(t, repo.path, "same.txt", "feature\n")
	commitCompare(t, repo.path, "feature change")
	feature := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
	gitCompare(t, repo.path, "checkout", "main")
	writeCompareFile(t, repo.path, "same.txt", "mainline\n")
	commitCompare(t, repo.path, "mainline change")
	mainline := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
	gitCompare(t, repo.path, "update-ref", "refs/remotes/origin/main", mainline)
	gitCompare(t, repo.path, "checkout", "feature")

	before := snapshotCompareRepository(t, repo.path)
	service, ws := repo.serviceWorkspace("feature")
	_, err := service.FilesContext(context.Background(), ws, CompareRequest{
		Mode: CompareCommits, StartCommit: feature, EndCommit: feature, SimulateOnMainline: true,
	}, WorkspaceMainBranch{DisplayRef: "origin/main", LocalRef: "origin/main"})
	if err == nil || !strings.Contains(err.Error(), "无法模拟应用到主分支") {
		t.Fatalf("simulation error = %v", err)
	}
	after := snapshotCompareRepository(t, repo.path)
	if before != after {
		t.Fatalf("source repository changed during isolated simulation\nbefore: %#v\nafter: %#v", before, after)
	}
}

func TestCompareSimulationAppliesNetRangeToLocalMainline(t *testing.T) {
	repo := newCompareTestRepo(t)
	writeCompareFile(t, repo.path, "base.txt", "base\n")
	commitCompare(t, repo.path, "base")
	gitCompare(t, repo.path, "checkout", "-b", "feature")
	writeCompareFile(t, repo.path, "feature.txt", "feature\n")
	commitCompare(t, repo.path, "feature")
	feature := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
	gitCompare(t, repo.path, "checkout", "main")
	writeCompareFile(t, repo.path, "mainline.txt", "mainline\n")
	commitCompare(t, repo.path, "mainline")
	mainline := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
	gitCompare(t, repo.path, "update-ref", "refs/remotes/origin/main", mainline)
	gitCompare(t, repo.path, "checkout", "feature")

	before := snapshotCompareRepository(t, repo.path)
	service, ws := repo.serviceWorkspace("feature")
	result, err := service.FilesContext(context.Background(), ws, CompareRequest{
		Mode: CompareCommits, StartCommit: feature, EndCommit: feature, SimulateOnMainline: true,
	}, WorkspaceMainBranch{DisplayRef: "origin/main", LocalRef: "origin/main"})
	if err != nil {
		t.Fatal(err)
	}
	assertComparePaths(t, result.Files, "feature.txt")
	if result.BaseCommit != mainline || !strings.HasPrefix(result.TargetCommit, "tree:") {
		t.Fatalf("simulation snapshots = base %q target %q", result.BaseCommit, result.TargetCommit)
	}
	if before != snapshotCompareRepository(t, repo.path) {
		t.Fatal("successful simulation changed the source repository")
	}
}

func TestComparePreviewRejectsChangedSnapshot(t *testing.T) {
	t.Run("worktree changed after file list", func(t *testing.T) {
		repo := newCompareTestRepo(t)
		writeCompareFile(t, repo.path, "base.txt", "base\n")
		commitCompare(t, repo.path, "base")
		gitCompare(t, repo.path, "checkout", "-b", "feature")
		writeCompareFile(t, repo.path, "base.txt", "dirty one\n")
		service, ws := repo.serviceWorkspace("feature")
		request := CompareRequest{Mode: CompareUncommitted}
		files, err := service.FilesContext(context.Background(), ws, request, WorkspaceMainBranch{})
		if err != nil {
			t.Fatal(err)
		}
		request.ExpectedBaseCommit, request.ExpectedTargetCommit = files.BaseCommit, files.TargetCommit
		writeCompareFile(t, repo.path, "base.txt", "dirty two\n")
		_, err = service.PreviewContext(context.Background(), ws, request, files.Files[0], WorkspaceMainBranch{})
		if err == nil || !strings.Contains(err.Error(), "差异已变化") {
			t.Fatalf("stale worktree preview error = %v", err)
		}
	})

	t.Run("HEAD changed after file list", func(t *testing.T) {
		repo := newCompareTestRepo(t)
		writeCompareFile(t, repo.path, "base.txt", "base\n")
		commitCompare(t, repo.path, "base")
		main := gitCompareOutput(t, repo.path, "rev-parse", "HEAD")
		gitCompare(t, repo.path, "checkout", "-b", "feature")
		writeCompareFile(t, repo.path, "first.txt", "first\n")
		commitCompare(t, repo.path, "first")
		gitCompare(t, repo.path, "update-ref", "refs/remotes/origin/main", main)
		service, ws := repo.serviceWorkspace("feature")
		request := CompareRequest{Mode: CompareMainline}
		branch := WorkspaceMainBranch{DisplayRef: "origin/main", LocalRef: "origin/main"}
		files, err := service.FilesContext(context.Background(), ws, request, branch)
		if err != nil {
			t.Fatal(err)
		}
		request.ExpectedBaseCommit, request.ExpectedTargetCommit = files.BaseCommit, files.TargetCommit
		writeCompareFile(t, repo.path, "second.txt", "second\n")
		commitCompare(t, repo.path, "second")
		_, err = service.PreviewContext(context.Background(), ws, request, files.Files[0], branch)
		if err == nil || !strings.Contains(err.Error(), "差异已变化") {
			t.Fatalf("stale HEAD preview error = %v", err)
		}
	})

	t.Run("empty HEAD commit changed after file list", func(t *testing.T) {
		repo := newCompareTestRepo(t)
		writeCompareFile(t, repo.path, "base.txt", "base\n")
		commitCompare(t, repo.path, "base")
		gitCompare(t, repo.path, "checkout", "-b", "feature")
		writeCompareFile(t, repo.path, "base.txt", "dirty\n")
		service, ws := repo.serviceWorkspace("feature")
		request := CompareRequest{Mode: CompareUncommitted}
		files, err := service.FilesContext(context.Background(), ws, request, WorkspaceMainBranch{})
		if err != nil {
			t.Fatal(err)
		}
		request.ExpectedBaseCommit, request.ExpectedTargetCommit = files.BaseCommit, files.TargetCommit
		gitCompare(t, repo.path, "commit", "--allow-empty", "-m", "advance HEAD")
		_, err = service.PreviewContext(context.Background(), ws, request, files.Files[0], WorkspaceMainBranch{})
		if err == nil || !strings.Contains(err.Error(), "差异已变化") {
			t.Fatalf("stale empty-commit preview error = %v", err)
		}
	})
}

func TestCompareRequestJSONIncludesRangeAndSnapshotFields(t *testing.T) {
	var request CompareRequest
	input := `{"mode":"COMMITS","startCommit":"aaa1111","endCommit":"bbb2222","includeWorkingTree":true,"simulateOnMainline":true,"expectedBaseCommit":"base","expectedTargetCommit":"tree:target"}`
	if err := json.Unmarshal([]byte(input), &request); err != nil {
		t.Fatal(err)
	}
	if request.StartCommit != "aaa1111" || request.EndCommit != "bbb2222" || !request.IncludeWorkingTree || !request.SimulateOnMainline || request.ExpectedBaseCommit != "base" || request.ExpectedTargetCommit != "tree:target" {
		t.Fatalf("decoded CompareRequest = %#v", request)
	}
}

type compareTestRepo struct{ path, locks string }

func newCompareTestRepo(t *testing.T) compareTestRepo {
	t.Helper()
	path := t.TempDir()
	gitCompare(t, path, "init", "-b", "main")
	gitCompare(t, path, "config", "user.name", "Compare Test")
	gitCompare(t, path, "config", "user.email", "compare@example.invalid")
	return compareTestRepo{path: path, locks: t.TempDir()}
}

func (repo compareTestRepo) serviceWorkspace(branch string) (*WorkspaceGitDiffService, domain.ServiceWorkspace) {
	return NewWorkspaceGitDiffService(NewGitClient(nil, "git"), NewOperationLocks(repo.locks)), domain.ServiceWorkspace{
		RepositoryPath: repo.path, WorktreePath: repo.path, Branch: branch,
		Strategy: domain.WorkspaceStandardWorktree, Health: domain.WorkspaceReady,
	}
}

func commitCompare(t *testing.T, repository, message string) {
	t.Helper()
	gitCompare(t, repository, "add", "-A")
	gitCompare(t, repository, "commit", "-m", message)
}

func writeCompareFile(t *testing.T, repository, relative, content string) {
	t.Helper()
	path := filepath.Join(repository, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitCompare(t *testing.T, repository string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, arguments...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}

func gitCompareOutput(t *testing.T, repository string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", repository}, arguments...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(arguments, " "), err)
	}
	return strings.TrimSpace(string(output))
}

func assertComparePaths(t *testing.T, files []CompareChange, want ...string) {
	t.Helper()
	got := make([]string, 0, len(files))
	for _, file := range files {
		got = append(got, file.Path)
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("changed paths = %v, want %v", got, want)
	}
}

func snapshotCompareRepository(t *testing.T, repository string) string {
	t.Helper()
	indexPath := filepath.Join(repository, ".git", "index")
	index, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatal(err)
	}
	refs := gitCompareOutput(t, repository, "for-each-ref", "--format=%(refname):%(objectname)")
	status := gitCompareOutput(t, repository, "status", "--porcelain=v1", "-uall")
	files := make([]string, 0)
	objects := filepath.Join(repository, ".git", "objects")
	if err := filepath.WalkDir(objects, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			rel, err := filepath.Rel(objects, path)
			if err != nil {
				return err
			}
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	objectHash := sha256.Sum256([]byte(strings.Join(files, "\n")))
	indexHash := sha256.Sum256(index)
	worktreeFiles := make([]string, 0)
	if err := filepath.WalkDir(repository, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != repository && entry.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(repository, path)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(content)
		worktreeFiles = append(worktreeFiles, filepath.ToSlash(rel)+":"+hex.EncodeToString(digest[:]))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	sort.Strings(worktreeFiles)
	return strings.Join([]string{
		gitCompareOutput(t, repository, "rev-parse", "HEAD"), refs, status,
		hex.EncodeToString(indexHash[:]), hex.EncodeToString(objectHash[:]), strings.Join(worktreeFiles, "\n"),
	}, "\x00")
}
