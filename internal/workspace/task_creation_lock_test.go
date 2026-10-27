package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func newTaskCreationLockTestServices(t *testing.T) (*TaskApplicationService, *TaskApplicationService, storage.Paths, domain.AppConfig) {
	t.Helper()
	paths := storage.NewPaths(t.TempDir())
	config, err := storage.NewConfigStore(paths).Load()
	if err != nil {
		t.Fatal(err)
	}
	if config.TaskRoot == nil {
		t.Fatal("default task root was not initialized")
	}
	first := NewTaskApplicationService(nil, nil, paths)
	second := NewTaskApplicationService(nil, nil, paths)
	if first.Locks == second.Locks || first.Locks.Directory != second.Locks.Directory {
		t.Fatal("test requires distinct OperationLocks objects sharing one lock directory")
	}
	return first, second, paths, config
}

func TestCreateTaskRejectsCrossServiceTaskLockWithoutTouchingDirectories(t *testing.T) {
	first, second, _, config := newTaskCreationLockTestServices(t)
	target := filepath.Join(*config.TaskRoot, "busy-create")
	unrelated := filepath.Join(*config.TaskRoot, "keep-unrelated", "sentinel.txt")
	if err := os.MkdirAll(filepath.Dir(unrelated), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unrelated, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}

	err := second.Locks.WithTask(target, func() error {
		_, createErr := first.CreateTask(context.Background(), config, CreateTaskRequest{FolderName: "busy-create"})
		if !errors.Is(createErr, storage.ErrLockBusy) {
			t.Fatalf("CreateTask error = %v, want task lock busy", createErr)
		}
		if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
			t.Fatalf("busy task path was created or changed: %v", statErr)
		}
		if content, readErr := os.ReadFile(unrelated); readErr != nil || string(content) != "keep" {
			t.Fatalf("unrelated directory was changed: content=%q err=%v", content, readErr)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCreateTaskHoldsTaskLockThroughSavingAndCompletionCallbacks(t *testing.T) {
	first, second, _, config := newTaskCreationLockTestServices(t)
	taskPath := filepath.Join(*config.TaskRoot, "progress-lock")
	checkedSaving, checkedCompletion := false, false
	_, err := first.CreateTaskWithProgress(context.Background(), config, CreateTaskRequest{FolderName: "progress-lock"}, func(event TaskCreationProgress) {
		checkLock := func() {
			t.Helper()
			entered := false
			lockErr := second.Locks.WithTask(taskPath, func() error {
				entered = true
				return nil
			})
			if entered || !errors.Is(lockErr, storage.ErrLockBusy) {
				t.Errorf("second service task lock during %s/%s = entered:%t err:%v, want busy", event.Phase, event.Status, entered, lockErr)
			}
		}
		switch {
		case event.Phase == TaskCreationPhaseSavingManifest && event.Status == TaskCreationProgressStarted:
			checkedSaving = true
			checkLock()
		case event.Phase == TaskCreationPhaseCompleted && event.Status == TaskCreationProgressCompleted:
			checkedCompletion = true
			checkLock()
			if deleteErr := second.Delete(context.Background(), config, taskPath, false); !errors.Is(deleteErr, storage.ErrLockBusy) {
				t.Errorf("Delete during completion callback error = %v, want task lock busy", deleteErr)
			}
			if _, statErr := os.Stat(taskPath); statErr != nil {
				t.Errorf("task directory disappeared during completion callback: %v", statErr)
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if !checkedSaving || !checkedCompletion {
		t.Fatalf("lock callbacks observed saving=%t completion=%t", checkedSaving, checkedCompletion)
	}
	if _, err := first.Manifests.Load(taskPath); err != nil {
		t.Fatalf("created task manifest not available after lock release: %v", err)
	}
}
