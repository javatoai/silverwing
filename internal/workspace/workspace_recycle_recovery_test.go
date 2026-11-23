package workspace

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
)

func TestRestoreRecycledModuleRefreshesTaskAgents(t *testing.T) {
	f, manifest, repository := newSingleMissingRepositoryForceFixture(t)
	ws := manifest.Services[0]
	configureRecycleRecoveryRepository(f, ws, repository)
	f.service.ModuleRemoval.Recycler = f.recycler
	agentsPath, err := f.service.Documents.WriteTaskDocument(f.task, manifest, f.config)
	if err != nil {
		t.Fatal(err)
	}
	agents, err := os.ReadFile(agentsPath)
	if err != nil {
		t.Fatal(err)
	}
	tail := []byte("\r\nUser recovery rule.\r\n")
	if err := os.WriteFile(agentsPath, append(agents, tail...), 0600); err != nil {
		t.Fatal(err)
	}
	agentsInfo, err := os.Stat(agentsPath)
	if err != nil {
		t.Fatal(err)
	}
	admin := gitCompareOutput(t, ws.WorktreePath, "rev-parse", "--absolute-git-dir")
	preview, err := f.service.InspectModuleRemoval(f.config, f.task, ws.WorktreePath)
	if err != nil {
		t.Fatal(err)
	}
	confirmation := WorkspaceModuleRemovalConfirmation{Fingerprint: preview.Fingerprint, AcknowledgeDataLoss: true}
	if result, err := f.service.RemoveModule(t.Context(), f.config, f.task, preview, confirmation); err != nil || !result.DirectoryRecycled || result.CleanupError != "" {
		t.Fatalf("module removal failed: %+v, %v", result, err)
	}
	if err := os.Rename(filepath.Join(f.recycler.bin, filepath.Base(ws.WorktreePath)), ws.WorktreePath); err != nil {
		t.Fatal(err)
	}
	removed, err := os.ReadFile(agentsPath)
	if err != nil || bytes.Contains(removed, []byte(ws.WorktreePath)) || !bytes.HasSuffix(removed, tail) {
		t.Fatalf("removed workspace scope or user text is wrong: %q, %v", removed, err)
	}
	broken := bytes.Replace(removed, []byte("<!-- silverwing:instructions:end -->"), []byte("user-edited marker"), 1)
	if bytes.Equal(removed, broken) {
		t.Fatal("task AGENTS.md is missing its generated instruction marker")
	}
	if err := os.WriteFile(agentsPath, broken, 0600); err != nil {
		t.Fatal(err)
	}
	if count, err := f.service.RestoreRecycledWorkspaces(t.Context(), f.config, f.task); err == nil || count != 0 {
		t.Fatalf("restore accepted a malformed instruction block: %d, %v", count, err)
	}
	if got, err := os.ReadFile(agentsPath); err != nil || !bytes.Equal(got, broken) {
		t.Fatalf("failed restore changed AGENTS.md: %q, %v", got, err)
	}
	if current, err := f.service.Manifests.Load(f.task); err != nil || len(current.Services) != 0 {
		t.Fatalf("failed restore did not roll back the module record: %+v, %v", current.Services, err)
	}
	if _, err := os.Lstat(admin); !os.IsNotExist(err) {
		t.Fatalf("failed restore did not roll back Git registration: %v", err)
	}
	if err := os.WriteFile(agentsPath, removed, 0600); err != nil {
		t.Fatal(err)
	}
	if count, err := f.service.RestoreRecycledWorkspaces(t.Context(), f.config, f.task); err != nil || count != 1 {
		t.Fatalf("restore after fixing AGENTS.md returned %d, %v", count, err)
	}
	if got, err := os.ReadFile(agentsPath); err != nil || !bytes.HasSuffix(got, tail) || !bytes.Contains(got, []byte(ws.WorktreePath)) {
		t.Fatalf("restore did not refresh only the workspace instructions: %q, %v", got, err)
	}
	if info, err := os.Stat(agentsPath); err != nil || !os.SameFile(info, agentsInfo) {
		t.Fatalf("restore replaced AGENTS.md: %v", err)
	}
}

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
