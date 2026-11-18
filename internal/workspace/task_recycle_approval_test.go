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

type afterMaterialsRecycle struct {
	*fixtureDirectoryRecycler
	material string
	after    func() error
}

func (r afterMaterialsRecycle) Recycle(ctx context.Context, path string) error {
	if err := r.fixtureDirectoryRecycler.Recycle(ctx, path); err != nil {
		return err
	}
	if path == r.material {
		return r.after()
	}
	return nil
}

func TestTaskRecycleRejectsWorkspaceReplacementAfterMaterialsMove(t *testing.T) {
	f, manifest, repository := newSingleMissingRepositoryForceFixture(t)
	target := manifest.Services[0].WorktreePath
	parked := filepath.Join(f.base, "parked")
	f.service.Lifecycle.Recycler = afterMaterialsRecycle{
		fixtureDirectoryRecycler: f.recycler,
		material:                 f.material,
		after: func() error {
			if !isWithin(target, f.base) || !isWithin(parked, f.base) {
				t.Fatal("test replacement path outside fixture")
			}
			if err := os.Rename(target, parked); err != nil {
				return err
			}
			return os.Mkdir(target, 0o700)
		},
	}
	preview, err := f.service.InspectDeletion(t.Context(), f.config, f.task)
	if err != nil || !preview.Force.CanForce || !preview.Materials.CanDelete {
		t.Fatalf("preflight failed: %+v %v", preview, err)
	}
	err = f.service.DeleteWithOptions(t.Context(), f.config, f.task, TaskDeleteOptions{
		ForceGuard: preview.Force.Guard, DeleteMaterials: true, MaterialsGuard: preview.Materials.Guard,
	})
	if err == nil || !strings.Contains(err.Error(), "任务尚未完成回收") || len(f.recycler.calls) != 1 {
		t.Fatalf("workspace replacement was not stopped: %v %v", err, f.recycler.calls)
	}
	records, err := f.service.Git.Worktrees(repository)
	if err != nil {
		t.Fatal(err)
	}
	for _, record := range records {
		if filepath.Clean(record.Path) == filepath.Clean(target) {
			return
		}
	}
	t.Fatal("worktree registration was removed despite the changed target")
}

func TestTaskRecycleNormalApprovalRejectsNewWorkspaceRisk(t *testing.T) {
	f, manifest, repo := newSingleMissingRepositoryForceFixture(t)
	ws := manifest.Services[0]
	f.config.Repositories = []domain.RepositoryConfig{{
		ID: ws.RepositoryID, Name: ws.ServiceName, RootPath: repo,
		GitCommonDirectory: filepath.Join(repo, ".git"), OriginURL: ws.OriginURL,
	}}
	preview, err := f.service.InspectDeletion(t.Context(), f.config, f.task)
	if err != nil || preview.Guard == "" || len(preview.Risks) != 1 {
		t.Fatalf("preflight failed: %+v %v", preview, err)
	}
	// Same status and pathname, but new bytes since the user confirmed.
	writeCompareFile(t, ws.WorktreePath, "dirty.txt", "new untracked content\n")
	err = f.service.DeleteWithOptions(t.Context(), f.config, f.task, TaskDeleteOptions{ForceDiscard: true, Guard: preview.Guard})
	if err == nil || !strings.Contains(err.Error(), "已变化") || len(f.recycler.calls) != 0 {
		t.Fatalf("stale approval passed: %v %v", err, f.recycler.calls)
	}
}

func TestTaskRecycleRestoresWorktreeIndexAndWorktreeFiles(t *testing.T) {
	f, manifest, repo := newSingleMissingRepositoryForceFixture(t)
	target := manifest.Services[0].WorktreePath
	writeCompareFile(t, target, "base.txt", "staged-only\n")
	gitCompare(t, target, "add", "base.txt")
	stagedObject := gitCompareOutput(t, target, "rev-parse", ":base.txt")
	writeCompareFile(t, target, "base.txt", "unstaged\n")
	preview, err := f.service.InspectDeletion(t.Context(), f.config, f.task)
	if err != nil || !preview.Force.CanForce {
		t.Fatalf("preflight failed: %+v %v", preview.Force, err)
	}
	if err := f.service.DeleteWithOptions(t.Context(), f.config, f.task, TaskDeleteOptions{ForceGuard: preview.Force.Guard}); err != nil {
		t.Fatal(err)
	}
	gitCompare(t, repo, "prune", "--expire=now")
	check, err := f.service.Git.RunContext(t.Context(), repo, 10*time.Second, false, true, "cat-file", "-e", stagedObject)
	if err != nil || check.ExitCode == 0 {
		t.Fatalf("test expected the original index-only object to be pruned: %+v %v", check, err)
	}
	// The fixture recycler requires both paths to be under its isolated root.
	trashed := filepath.Join(f.recycler.bin, filepath.Base(f.task))
	if !isWithin(trashed, f.base) || !isWithin(f.task, f.base) {
		t.Fatal("test restore path outside fixture")
	}
	if err := os.Rename(trashed, f.task); err != nil {
		t.Fatal(err)
	}
	recovered, err := f.service.RestoreRecycledWorkspaces(t.Context(), f.config, f.task)
	if err != nil || recovered != 1 {
		t.Fatalf("recover worktree: %d %v", recovered, err)
	}
	if staged := gitCompareOutput(t, target, "show", ":base.txt"); staged != "staged-only" {
		t.Fatalf("wrong staged content: %q", staged)
	}
	if data, err := os.ReadFile(filepath.Join(target, "base.txt")); err != nil || string(data) != "unstaged\n" {
		t.Fatalf("wrong unstaged content: %q %v", data, err)
	}
	if status := gitCompareOutput(t, target, "status", "--porcelain"); !strings.Contains(status, "MM base.txt") {
		t.Fatalf("staged and unstaged state was lost: %q", status)
	}
}
