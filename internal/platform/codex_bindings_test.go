package platform

import (
	"errors"
	"path/filepath"
	"testing"

	"silverwing-go/internal/storage"
)

func TestCodexBindingMutationUsesSharedLock(t *testing.T) {
	paths := storage.NewPaths(t.TempDir())
	service := NewService(paths, nil, nil)
	first, second := "first-thread", "second-thread"
	if err := service.setCodexBoundThread("first-workspace", &first); err != nil {
		t.Fatal(err)
	}
	release, err := storage.AcquireLock(filepath.Join(paths.Locks, "codex-task-bindings.lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := service.setCodexBoundThread("second-workspace", &second); !errors.Is(err, storage.ErrLockBusy) {
		t.Fatalf("another writer's mutation lock was ignored: %v", err)
	}
	release()
	if err := service.setCodexBoundThread("second-workspace", &second); err != nil {
		t.Fatal(err)
	}
	bindings, err := service.readCodexBindings()
	if err != nil || bindings["first-workspace"] != first || bindings["second-workspace"] != second {
		t.Fatalf("binding update did not preserve other workspaces: %v, %v", bindings, err)
	}
}
