package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
)

// cancelOnErrContext makes cancellation occur on a chosen cancellation check,
// so these tests exercise in-flight work without sleeps or scheduler timing.
type cancelOnErrContext struct {
	context.Context
	cancel   context.CancelFunc
	cancelAt int
	checks   int
}

func (c *cancelOnErrContext) Err() error {
	c.checks++
	if c.checks == c.cancelAt {
		c.cancel()
	}
	return c.Context.Err()
}

func cancellationContext(cancelAt int) *cancelOnErrContext {
	base, cancel := context.WithCancel(context.Background())
	return &cancelOnErrContext{Context: base, cancel: cancel, cancelAt: cancelAt}
}

func TestSnapshotPropagatesCancellationDuringMigrationPendingReadWithoutTasks(t *testing.T) {
	app := qaTestApp(t)
	ctx := cancellationContext(4) // Snapshot entry, config load, migration check, post-Lstat check.
	defer ctx.cancel()

	snapshot, err := app.Snapshot(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Snapshot error = %v, want context cancellation", err)
	}
	if snapshot.Home != "" || len(snapshot.Tasks) != 0 {
		t.Fatalf("canceled Snapshot returned a partial success: %+v", snapshot)
	}
}

func TestTaskRefreshPropagatesCancellationAfterSkillCatalogWithoutTasks(t *testing.T) {
	app := qaTestApp(t)
	ctx := cancellationContext(11) // reaches Snapshot's cancellation check after the Skills catalog read.
	defer ctx.cancel()

	result, err := app.Dispatch(ctx, Action{Kind: "task.refresh"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("task.refresh error = %v, want context cancellation", err)
	}
	if result.Message == "任务已刷新" || result.Data != nil {
		t.Fatalf("canceled task.refresh reported success: %+v", result)
	}
}

func TestSnapshotRetainsDefaultTaskRootInitializationErrorUntilRootIsSet(t *testing.T) {
	home := t.TempDir()
	blockedRoot := filepath.Join(home, "tasks")
	if err := os.WriteFile(blockedRoot, []byte("blocks directory creation"), 0o600); err != nil {
		t.Fatal(err)
	}
	app, err := New(home)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := app.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	const initializationError = "无法创建默认任务根目录"
	if !strings.Contains(snapshot.Error, initializationError) {
		t.Fatalf("Snapshot.Error = %q, want original task-root initialization error", snapshot.Error)
	}

	root := t.TempDir()
	if _, err := app.Config.Update(func(config domain.AppConfig) (domain.AppConfig, error) {
		config.TaskRoot = &root
		return config, nil
	}); err != nil {
		t.Fatal(err)
	}
	snapshot, err = app.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(snapshot.Error, initializationError) {
		t.Fatalf("stale task-root initialization error remained after selecting a valid root: %q", snapshot.Error)
	}
}
