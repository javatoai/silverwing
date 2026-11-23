package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/storage"
)

func TestMaterialsGitUploadsRequirementsFromTotalRoot(t *testing.T) {
	f := newMaterialsGitFixture(t, false)
	seedMaterialsLayoutRemote(t, f)
	materialsGitTestFile(t, f.root, "requirements/2026/7132215081-需求/研发/note.md", "原文\r\n")
	materialsGitTestFile(t, f.root, "requirements/2025/7000000000-历史/产品/design.md", "历史资料")
	materialsGitTestFile(t, f.root, "other-team.md", "whole repository")
	materialsGitTestFile(t, f.root, "2026/operations.md", "ordinary year directory")
	materialsGitTestFile(t, f.root, ".gitignore", "/temp/\n")
	materialsGitTestFile(t, f.root, "temp/ignored.md", "ignored by user")
	f.sync()
	tracked := materialsGitTestRun(t, f.remote, "-c", "core.quotepath=false", "ls-tree", "-r", "--name-only", "master")
	for _, path := range []string{"requirements/2026/7132215081-需求/研发/note.md", "requirements/2025/7000000000-历史/产品/design.md", "other-team.md", "2026/operations.md"} {
		if !strings.Contains(tracked, path) {
			t.Fatalf("missing %s: %s", path, tracked)
		}
	}
	if strings.Contains(tracked, "temp/ignored.md") {
		t.Fatal("user ignore lost")
	}
}

func TestMaterialsGitRejectsInvalidLayoutBeforeRunningGit(t *testing.T) {
	for _, test := range []struct {
		name, relative, message string
	}{
		{"nested Git directory", "requirements/.git", "独立的 Git 元数据"},
		{"nested Git file", "requirements/.git", "独立的 Git 元数据"},
		{"legacy annual materials", "2026/123-需求", "年度需求资料"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "requirements")
			offending := filepath.Join(root, filepath.FromSlash(test.relative))
			if test.name == "nested Git file" {
				materialsGitTestFile(t, root, test.relative, "gitdir: ../.git/worktrees/requirements\n")
			} else if err := os.MkdirAll(offending, 0o700); err != nil {
				t.Fatal(err)
			}
			paths := storage.NewPaths(t.TempDir())
			scope := MaterialsGitScope{Root: root, Home: paths.Home, Branch: "master", URL: "https://example.invalid/docs"}
			service := &MaterialsGitSyncService{Git: NewGitClient(nil, filepath.Join(paths.Home, "unused-git")), Paths: paths}
			status, err := service.Sync(context.Background(), scope, true, func(phase string) {
				t.Errorf("invalid layout reached Git phase %q", phase)
			})
			if err == nil || !strings.Contains(err.Error(), test.message) || !strings.Contains(err.Error(), offending) || !status.Paused || status.Changed {
				t.Fatalf("invalid layout was not blocked: %+v %v", status, err)
			}
			for _, path := range []string{filepath.Join(root, ".git"), service.statePath(scope)} {
				if _, err := os.Lstat(path); !os.IsNotExist(err) {
					t.Fatalf("invalid layout created Git metadata or sync state at %s: %v", path, err)
				}
			}
			if _, err := os.Lstat(offending); err != nil {
				t.Fatalf("invalid layout changed user data: %v", err)
			}
		})
	}
}

func TestMaterialsGitRejectsLegacyLayoutBeforeApplyingFetchedTree(t *testing.T) {
	for _, name := range []string{"first checkout", "existing branch", "branch switch"} {
		t.Run(name, func(t *testing.T) {
			f := newMaterialsGitFixture(t, true)
			var previousHead string
			if name != "first checkout" {
				f.sync()
				previousHead = materialsGitTestRun(t, f.root, "rev-parse", "HEAD")
			}
			if name == "branch switch" {
				f.scope.Branch = "docs/legacy"
				materialsGitTestRun(t, f.seed, "checkout", "-b", f.scope.Branch)
			}

			const legacyDir = "2025/456-旧需求"
			materialsGitTestFile(t, f.seed, legacyDir+"/研发/legacy.md", "legacy layout\n")
			materialsGitTestRun(t, f.seed, "add", ".")
			materialsGitTestRun(t, f.seed, "commit", "-m", "legacy tree")
			materialsGitTestRun(t, f.seed, "push", "origin", f.scope.Branch)
			remoteHead := materialsGitTestRun(t, f.remote, "rev-parse", f.scope.Branch)
			status, err := f.service.Sync(context.Background(), f.scope, true, nil)
			offending := filepath.Join(f.root, filepath.FromSlash(legacyDir))
			if err == nil || !strings.Contains(err.Error(), offending) || !status.Paused {
				t.Fatalf("fetched legacy layout did not pause: %+v %v", status, err)
			}
			if _, err := os.Lstat(offending); !os.IsNotExist(err) {
				t.Fatalf("sync applied legacy tree: %v", err)
			}
			if previousHead != "" && materialsGitTestRun(t, f.root, "rev-parse", "HEAD") != previousHead {
				t.Fatal("sync changed the checked-out commit")
			}
			if materialsGitTestRun(t, f.root, "branch", "--show-current") != "master" || materialsGitTestRun(t, f.remote, "rev-parse", f.scope.Branch) != remoteHead {
				t.Fatal("sync changed the local branch or remote commit")
			}
		})
	}
}

func TestMaterialsGitUsesExactSelectedRepositoryAndLock(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	parent := f.root
	materialsGitTestRun(t, parent, "init", "--initial-branch=master")
	f.scope.Root = filepath.Join(parent, "requirements")
	materialsGitTestRun(t, parent, "clone", "--branch", "master", f.remote, f.scope.Root)
	materialsGitTestFile(t, parent, "parent-only.md", "outside sync scope\n")
	const relative = "requirements/2026/123-需求/研发/selected.md"
	materialsGitTestFile(t, f.scope.Root, relative, "selected checkout\n")

	parentRelease, err := storage.AcquireMaterialsRepositoryLock(parent)
	if err != nil {
		t.Fatal(err)
	}
	defer parentRelease()
	release, err := storage.AcquireMaterialsRepositoryLock(f.scope.Root)
	if err != nil {
		t.Fatal(err)
	}
	_, lockedErr := f.service.Sync(context.Background(), f.scope, false, nil)
	release()
	if !errors.Is(lockedErr, storage.ErrLockBusy) {
		t.Fatalf("sync must share the selected root lock: %v", lockedErr)
	}

	status, err := f.service.Sync(context.Background(), f.scope, false, nil)
	if err != nil || status.Paused || status.LastSuccess == "" {
		t.Fatalf("selected repository did not sync: %+v %v", status, err)
	}
	if got := materialsGitTestRun(t, f.remote, "show", "master:"+relative); got != "selected checkout" {
		t.Fatalf("unexpected synced document: %q", got)
	}
	remoteFiles := materialsGitTestRun(t, f.remote, "ls-tree", "-r", "--name-only", "master")
	if strings.Contains(remoteFiles, "parent-only.md") || strings.Contains(remoteFiles, "requirements/requirements/") {
		t.Fatalf("sync used the parent repository: %s", remoteFiles)
	}
	if got := materialsGitTestRun(t, parent, "status", "--porcelain"); got != "?? parent-only.md\n?? requirements/" {
		t.Fatalf("parent repository changed: %q", got)
	}
	if got := materialsGitTestRun(t, f.scope.Root, "remote", "get-url", "origin"); got != f.remote {
		t.Fatalf("origin changed: %q", got)
	}
}

func TestMaterialsGitBoundRootDoesNotReinitializeMissingGit(t *testing.T) {
	f := newMaterialsGitFixture(t, true)
	if err := f.service.save(f.scope, materialsGitBinding{URL: f.scope.URL, Branch: f.scope.Branch}); err != nil {
		t.Fatal(err)
	}
	materialsGitTestFile(t, f.scope.Root, "requirements/2026/123-需求/研发/local.md", "existing files without metadata\n")
	status, err := f.service.Sync(context.Background(), f.scope, true, nil)
	if err == nil || !strings.Contains(err.Error(), "Git 元数据") || !status.Paused {
		t.Fatalf("missing original metadata was accepted: %+v %v", status, err)
	}
	if _, err := os.Stat(filepath.Join(f.scope.Root, ".git")); !os.IsNotExist(err) {
		t.Fatalf("original checkout was reinitialized: %v", err)
	}
}

func seedMaterialsLayoutRemote(t *testing.T, f *materialsGitFixture) {
	t.Helper()
	materialsGitTestFile(t, f.seed, "README.md", "isolated repository\n")
	materialsGitTestRun(t, f.seed, "add", "README.md")
	materialsGitTestRun(t, f.seed, "commit", "-m", "seed")
	materialsGitTestRun(t, f.seed, "push", "origin", "master")
}
