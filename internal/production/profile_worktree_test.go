package production

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
)

func TestProfileProductionMove(t *testing.T) {
	f := newProductionFixture(t, true)
	ctx := context.Background()
	f.feature("master", "base.txt", "master change\n", f.base)
	fix := f.feature("hotfix/online", "base.txt", "online change\n", f.base)
	f.run(f.seed, "tag", "2.3.43.1", fix)
	f.run(f.seed, "push", "origin", "2.3.43.1")
	f.g.versions = []string{"2.3.43.1"}
	preview, err := f.s.Inspect(ctx, f.selection)
	if err != nil {
		t.Fatal(err)
	}
	b, err := f.s.MergeProduction(ctx, "", preview)
	if err == nil || b.Phase != "CONFLICT" || b.Intent == nil || b.Intent.Merge == nil {
		t.Fatalf("expected retained conflict: %+v, %v", b, err)
	}
	managedRoot := filepath.Join(f.root, "tasks", "silverwing-production-profile", "production-worktrees")
	wantPath := filepath.Join(managedRoot, b.ID, b.Intent.Merge.ID)
	if b.Intent.Merge.Worktree != wantPath {
		t.Fatalf("worktree path %q, want %q", b.Intent.Merge.Worktree, wantPath)
	}
	if _, err := os.Stat(filepath.Join(f.s.Paths.Home, "production-worktrees")); !os.IsNotExist(err) {
		t.Fatalf("worktree appeared in data directory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(f.s.Store.Directory, b.ID+".json")); err != nil {
		t.Fatal(err)
	}
	f.write(wantPath, "base.txt", "resolution survives relocation\n")
	config, err := f.s.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	switched := config
	newRoot := filepath.Join(f.root, "new tasks")
	switched.TaskRoot = &newRoot
	if err := f.s.Config.Save(switched); err != nil {
		t.Fatal(err)
	}
	fresh, err := f.s.Inspect(ctx, f.selection)
	newManagedRoot := filepath.Join(newRoot, "silverwing-production-profile", "production-worktrees")
	if err != nil || fresh.Target.WorktreeRoot != newManagedRoot {
		t.Fatalf("production preview after task-root switch: %+v, %v", fresh, err)
	}
	if _, _, err := f.s.ConflictWorkspace(ctx, b.ID); err == nil || !strings.Contains(err.Error(), "隔离工作区目录") {
		t.Fatalf("expected production batch target drift after task-root switch: %v", err)
	}
	if err := f.s.Config.Save(config); err != nil {
		t.Fatal(err)
	}
	paths := storage.NewPaths(filepath.Join(f.root, "moved data"))
	paths.ProfileID = f.s.Paths.ProfileID
	if err := os.Rename(f.s.Paths.Home, paths.Home); err != nil {
		t.Fatal(err)
	}
	moved := New(paths, storage.NewConfigStore(paths), f.git, process.NewRunner())
	moved.Genbu = f.g
	conflictPath, _, err := moved.ConflictWorkspace(ctx, b.ID)
	if err != nil || conflictPath != wantPath {
		t.Fatalf("conflict recovery after relocation: %q, %v", conflictPath, err)
	}
	data, err := os.ReadFile(filepath.Join(conflictPath, "base.txt"))
	if err != nil || string(data) != "resolution survives relocation\n" {
		t.Fatalf("resolution after relocation: %q, %v", data, err)
	}
	f.run(conflictPath, "add", "base.txt")
	f.run(conflictPath, "commit", "-m", "resolve production conflict")
	resumed, err := moved.Resume(ctx, b.ID)
	if err != nil || resumed.Phase != "MAIN_READY" || resumed.Intent != nil {
		t.Fatalf("resume after relocation: %+v, %v", resumed, err)
	}
}
