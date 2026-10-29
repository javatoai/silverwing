package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestScopedSnapshotAvoidsUnrequestedCatalogAndTasks(t *testing.T) {
	a, taskPath := scopedSnapshotFixture(t)
	// A nil catalog makes any accidental unrequested catalog read fail loudly.
	a.Platform = nil
	broken := filepath.Join(filepath.Dir(taskPath), "broken-neighbour")
	if err := os.Mkdir(broken, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, storage.TaskManifestFileName), []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	partial, err := a.SnapshotFor(context.Background(), SnapshotRequest{})
	if err != nil || partial.ConfigJSON == "" || partial.Error != "" || len(partial.Tasks) != 0 || len(partial.Skills) != 0 {
		t.Fatalf("configuration projection read unrelated collections: %+v, %v", partial, err)
	}
	tasks, err := a.SnapshotFor(context.Background(), SnapshotRequest{Tasks: true})
	if err != nil || len(tasks.Tasks) != 1 || !strings.Contains(tasks.Error, "broken-neighbour") {
		t.Fatalf("task-only projection lost tasks or scan diagnostics: %+v, %v", tasks, err)
	}
	if _, err := a.Config.Update(func(cConfig domain.AppConfig) (domain.AppConfig, error) {
		cConfig.Groups[0].Name = "external group change"
		return cConfig, nil
	}); err != nil {
		t.Fatal(err)
	}
	updated, err := a.SnapshotFor(context.Background(), SnapshotRequest{})
	if err != nil || !strings.Contains(updated.ConfigJSON, "external group change") || len(updated.Groups) != 1 || updated.Groups[0].Name != "external group change" {
		t.Fatalf("scoped read hid external configuration changes: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.SnapshotFor(ctx, SnapshotRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("scoped read ignored cancellation: %v", err)
	}
}

// The fixture uses the current profile's defaults and creates only local task
// metadata, so the same scoped-read contract applies to QA and Product.
func scopedSnapshotFixture(t *testing.T) (*App, string) {
	t.Helper()
	a, err := New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	config, err := a.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	manifest := domain.DefaultTaskManifest()
	manifest.TaskDirectoryName, manifest.FolderName = "chosen", "Chosen task"
	manifest.CreatedAt, manifest.UpdatedAt = "2026-10-29T00:00:00Z", "2026-10-29T00:00:00Z"
	path := filepath.Join(value(config.TaskRoot), manifest.TaskDirectoryName)
	if err := a.Manifests.Save(path, manifest); err != nil {
		t.Fatal(err)
	}
	return a, path
}
