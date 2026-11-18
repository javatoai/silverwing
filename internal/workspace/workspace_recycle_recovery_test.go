package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
)

func TestRestoreRecycledTaskPreservesSeparateIndexAndWorkingFile(t *testing.T) {
	ctx := context.Background()
	f, manifest, repository := newSingleMissingRepositoryForceFixture(t)
	ws := manifest.Services[0]
	configureRecycleRecoveryRepository(f, ws, repository)
	target := ws.WorktreePath
	writeCompareFile(t, target, "base.txt", "staged version\n")
	gitCompare(t, target, "add", "--", "base.txt")
	writeCompareFile(t, target, "base.txt", "working copy version\n")
	beforeStatus := gitCompareOutput(t, target, "--no-optional-locks", "status", "--porcelain")
	object := gitCompareOutput(t, target, "rev-parse", ":base.txt")
	admin := gitCompareOutput(t, target, "rev-parse", "--absolute-git-dir")
	pointer, err := os.ReadFile(filepath.Join(target, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeleteWithOptions(ctx, f.config, f.task, TaskDeleteOptions{ForceDiscard: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(admin); !os.IsNotExist(err) {
		t.Fatalf("original Git registration still present: %v", err)
	}
	// Force the test's index-only object to expire in the source repository.
	loose := filepath.Join(repository, ".git", "objects", object[:2], object[2:])
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(loose, old, old); err != nil {
		t.Fatal(err)
	}
	gitCompare(t, repository, "prune", "--expire=now")
	if _, err := os.Stat(loose); !os.IsNotExist(err) {
		t.Fatalf("fixture Git prune did not remove index-only object: %v", err)
	}
	restoreRecycleFixtureTask(t, f)
	count, err := f.service.RestoreRecycledWorkspaces(ctx, f.config, f.task)
	if err != nil || count != 1 {
		t.Fatalf("restore returned %d, %v", count, err)
	}
	if got := gitCompareOutput(t, target, "--no-optional-locks", "status", "--porcelain"); got != beforeStatus || !strings.Contains(got, "MM base.txt") {
		t.Fatalf("wrong Git status after recovery: %q, previous %q", got, beforeStatus)
	}
	if got := gitCompareOutput(t, target, "show", ":base.txt"); got != "staged version" {
		t.Fatalf("wrong Git index content: %q", got)
	}
	if got, err := os.ReadFile(filepath.Join(target, "base.txt")); err != nil || string(got) != "working copy version\n" {
		t.Fatalf("working copy changed: %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(target, ".git")); err != nil || string(got) != string(pointer) {
		t.Fatalf("original .git pointer changed: %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(target, "dirty.txt")); err != nil || string(got) != "dirty\n" {
		t.Fatalf("untracked file changed: %q, %v", got, err)
	}
	if count, err := f.service.RestoreRecycledWorkspaces(ctx, f.config, f.task); err != nil || count != 0 {
		t.Fatalf("repeated restore returned %d, %v", count, err)
	}
}

func TestRestoreRecycledTaskRejectsBranchAndSlotConflicts(t *testing.T) {
	for _, test := range []struct {
		name        string
		otherTarget string
		conflict    string
	}{
		{"branch checked out", "another-worktree", "已在另一个工作区检出"},
		{"slot occupied", "force", "原 Git 登记位置已被占用"},
		{"branch moved", "", "已不存在或移动"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			f, manifest, repository := newSingleMissingRepositoryForceFixture(t)
			ws := manifest.Services[0]
			configureRecycleRecoveryRepository(f, ws, repository)
			admin := gitCompareOutput(t, ws.WorktreePath, "rev-parse", "--absolute-git-dir")
			if err := f.service.DeleteWithOptions(ctx, f.config, f.task, TaskDeleteOptions{ForceDiscard: true}); err != nil {
				t.Fatal(err)
			}
			var other string
			switch test.name {
			case "branch checked out":
				other = filepath.Join(f.base, test.otherTarget)
				gitCompare(t, repository, "worktree", "add", other, ws.Branch)
			case "slot occupied":
				other = filepath.Join(f.base, test.otherTarget)
				gitCompare(t, repository, "worktree", "add", "-b", "other-branch", other, "main")
			case "branch moved":
				writeCompareFile(t, repository, "new.txt", "new commit\n")
				commitCompare(t, repository, "new branch target")
				gitCompare(t, repository, "branch", "--force", ws.Branch, "main")
			}
			before := gitCompareOutput(t, repository, "worktree", "list", "--porcelain")
			restoreRecycleFixtureTask(t, f)
			count, err := f.service.RestoreRecycledWorkspaces(ctx, f.config, f.task)
			if err == nil || count != 0 || !strings.Contains(err.Error(), test.conflict) {
				t.Fatalf("unsafe recovery: %d, %v", count, err)
			}
			if got := gitCompareOutput(t, repository, "worktree", "list", "--porcelain"); got != before {
				t.Fatalf("existing Git registrations changed: %s, previous %s", got, before)
			}
			if test.name == "slot occupied" {
				if got := gitCompareOutput(t, other, "rev-parse", "--absolute-git-dir"); !sameRecyclePath(got, admin) {
					t.Fatalf("the replacement no longer has its original registration: %s", got)
				}
			} else if _, err := os.Lstat(admin); !os.IsNotExist(err) {
				t.Fatalf("conflict unexpectedly created the original registration: %v", err)
			}
		})
	}
}

func TestRestoreRecycledTaskContinuesPastReplacedDirectory(t *testing.T) {
	ctx := context.Background()
	f, manifest, repository := newSingleMissingRepositoryForceFixture(t)
	first := manifest.Services[0]
	configureRecycleRecoveryRepository(f, first, repository)
	second := first
	second.ModuleID, second.ModuleName = "second", "second"
	second.Branch, second.WorktreePath = "feature/second", filepath.Join(f.task, "second")
	gitCompare(t, repository, "worktree", "add", "-b", second.Branch, second.WorktreePath, "main")
	manifest.Services = append(manifest.Services, second)
	if err := f.service.Manifests.Save(f.task, manifest); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeleteWithOptions(ctx, f.config, f.task, TaskDeleteOptions{ForceDiscard: true}); err != nil {
		t.Fatal(err)
	}
	restoreRecycleFixtureTask(t, f)
	if err := os.Rename(first.WorktreePath, filepath.Join(f.base, "original-first-worktree")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(first.WorktreePath, 0o700); err != nil {
		t.Fatal(err)
	}
	writeCompareFile(t, first.WorktreePath, "user.txt", "replacement sentinel\n")
	count, err := f.service.RestoreRecycledWorkspaces(ctx, f.config, f.task)
	if err == nil || !strings.Contains(err.Error(), "原路径已被其他目录使用") || count != 1 {
		t.Fatalf("restore returned %d, %v", count, err)
	}
	if got := gitCompareOutput(t, second.WorktreePath, "branch", "--show-current"); got != second.Branch {
		t.Fatalf("unrelated worktree not restored: %q", got)
	}
	if _, err := os.Lstat(filepath.Join(first.WorktreePath, ".git")); !os.IsNotExist(err) {
		t.Fatalf("replacement directory was registered: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(first.WorktreePath, "user.txt")); err != nil || string(data) != "replacement sentinel\n" {
		t.Fatalf("replacement file changed: %q, %v", data, err)
	}
}

type recycleRestoreChangingManifest struct {
	TaskManifestRepository
	reads     int
	changedAt int
}

func (r *recycleRestoreChangingManifest) Load(path string) (domain.TaskManifest, error) {
	manifest, err := r.TaskManifestRepository.Load(path)
	r.reads++
	if err != nil || r.reads != r.changedAt {
		return manifest, err
	}
	manifest.FolderName = "outside change"
	return manifest, r.TaskManifestRepository.Save(path, manifest)
}

func TestRestoreRecycledTaskPreservesConcurrentlyChangedManifest(t *testing.T) {
	ctx := context.Background()
	f, manifest, repository := newSingleMissingRepositoryForceFixture(t)
	ws := manifest.Services[0]
	configureRecycleRecoveryRepository(f, ws, repository)
	admin := gitCompareOutput(t, ws.WorktreePath, "rev-parse", "--absolute-git-dir")
	if err := f.service.DeleteWithOptions(ctx, f.config, f.task, TaskDeleteOptions{ForceDiscard: true}); err != nil {
		t.Fatal(err)
	}
	restoreRecycleFixtureTask(t, f)
	// An edit after registration publication must be preserved, and the new
	// registration must be rolled back before returning the scope conflict.
	f.service.Manifests = &recycleRestoreChangingManifest{TaskManifestRepository: f.service.Manifests, changedAt: 3}
	count, err := f.service.RestoreRecycledWorkspaces(ctx, f.config, f.task)
	if err == nil || !strings.Contains(err.Error(), "任务清单已变化") || count != 0 {
		t.Fatalf("restore did not reject stale manifest: %d, %v", count, err)
	}
	current, err := f.service.Manifests.Load(f.task)
	if err != nil || current.FolderName != "outside change" {
		t.Fatalf("concurrent manifest edit was overwritten: %+v, %v", current, err)
	}
	if _, err := os.Lstat(admin); !os.IsNotExist(err) {
		t.Fatalf("new Git registration survived rollback: %v", err)
	}
	if count, err := f.service.RestoreRecycledWorkspaces(ctx, f.config, f.task); err != nil || count != 1 {
		t.Fatalf("restore after recheck returned %d, %v", count, err)
	}
}

func configureRecycleRecoveryRepository(f *taskRecycleFixture, ws domain.ServiceWorkspace, repository string) {
	f.config.Repositories = []domain.RepositoryConfig{{
		ID: ws.RepositoryID, Name: ws.ServiceName, RootPath: repository,
		GitCommonDirectory: filepath.Join(repository, ".git"), OriginURL: ws.OriginURL,
	}}
}

func restoreRecycleFixtureTask(t *testing.T, f *taskRecycleFixture) {
	t.Helper()
	if err := os.Rename(filepath.Join(f.recycler.bin, filepath.Base(f.task)), f.task); err != nil {
		t.Fatal(err)
	}
}
