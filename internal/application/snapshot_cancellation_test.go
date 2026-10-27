package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/platform"
	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
)

type cancelOnErrCallContext struct {
	context.Context
	mu       sync.Mutex
	cancel   context.CancelFunc
	cancelAt int
	calls    int
}

func (c *cancelOnErrCallContext) Err() error {
	c.mu.Lock()
	c.calls++
	if c.calls == c.cancelAt {
		c.cancel()
	}
	c.mu.Unlock()
	return c.Context.Err()
}

func (c *cancelOnErrCallContext) errCalls() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func snapshotCancellationFixture(t *testing.T) (*App, string) {
	t.Helper()
	root := t.TempDir()
	paths := storage.NewPaths(filepath.Join(root, "profile"))
	taskRoot := filepath.Join(root, "tasks")
	if err := os.MkdirAll(taskRoot, 0755); err != nil {
		t.Fatal(err)
	}
	config := domain.DefaultAppConfig()
	config.TaskRoot = &taskRoot
	configStore := storage.NewConfigStore(paths)
	if err := configStore.Save(config); err != nil {
		t.Fatal(err)
	}
	manifests := storage.NewManifestStore()
	taskPath := filepath.Join(taskRoot, "snapshot-task")
	manifest := domain.DefaultTaskManifest()
	manifest.FolderName = "snapshot-task"
	manifest.TaskDirectoryName = "snapshot-task"
	if err := manifests.Save(taskPath, manifest); err != nil {
		t.Fatal(err)
	}
	runner := process.NewRunner()
	return &App{
		Paths: paths, Config: configStore, Manifests: manifests, Runner: runner,
		Platform: platform.NewService(paths, configStore, runner),
	}, taskPath
}

func TestSnapshotReturnsCancellationAfterCatalogScanAndTaskHistory(t *testing.T) {
	for _, stage := range []struct {
		name     string
		cancelAt int
	}{
		{name: "skill-catalog", cancelAt: 8},
		{name: "manifest-scan", cancelAt: 10},
		{name: "task-history", cancelAt: 12},
		{name: "final-snapshot-check", cancelAt: 13},
	} {
		t.Run(stage.name, func(t *testing.T) {
			a, _ := snapshotCancellationFixture(t)
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &cancelOnErrCallContext{Context: base, cancel: cancel, cancelAt: stage.cancelAt}
			_, err := a.Snapshot(ctx)
			if !errors.Is(err, context.Canceled) || ctx.errCalls() != stage.cancelAt {
				t.Fatalf("snapshot did not propagate cancellation at %s: calls=%d err=%v", stage.name, ctx.errCalls(), err)
			}
		})
	}
}

func TestSnapshotRetainsOrdinaryManifestDiagnostics(t *testing.T) {
	a, _ := snapshotCancellationFixture(t)
	brokenPath := filepath.Join(*mustTaskRoot(t, a), "broken-task")
	if err := os.Mkdir(brokenPath, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(brokenPath, storage.TaskManifestFileName), []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := a.Snapshot(context.Background())
	if err != nil || len(snapshot.Tasks) != 1 || !strings.Contains(snapshot.Error, "broken-task") {
		t.Fatalf("ordinary scan failure stopped producing a partial snapshot diagnostic: tasks=%d diagnostic=%q err=%v", len(snapshot.Tasks), snapshot.Error, err)
	}
}

func mustTaskRoot(t *testing.T, a *App) *string {
	t.Helper()
	config, err := a.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if config.TaskRoot == nil {
		t.Fatal("fixture task root is missing")
	}
	return config.TaskRoot
}
