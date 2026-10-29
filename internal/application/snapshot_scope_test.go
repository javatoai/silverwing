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

func TestScopedSnapshotAvoidsUnrequestedCatalogTasksAndHistory(t *testing.T) {
	a, taskPath := snapshotCancellationFixture(t)
	// A nil catalog makes any accidental unrequested catalog read fail loudly.
	a.Platform = nil
	broken := filepath.Join(filepath.Dir(taskPath), "broken-neighbour")
	if err := os.Mkdir(broken, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, storage.TaskManifestFileName), []byte("{broken"), 0600); err != nil {
		t.Fatal(err)
	}
	history := filepath.Join(taskPath, "tag-operations")
	if err := os.Mkdir(history, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(history, strings.Repeat("a", 32)+".json"), []byte("{broken-history"), 0600); err != nil {
		t.Fatal(err)
	}
	partial, err := a.SnapshotFor(context.Background(), SnapshotRequest{})
	if err != nil || partial.ConfigJSON == "" || partial.Error != "" || len(partial.Tasks) != 0 || len(partial.Skills) != 0 || len(partial.TagHistory) != 0 {
		t.Fatalf("configuration projection read unrelated collections: %+v, %v", partial, err)
	}
	tasks, err := a.SnapshotFor(context.Background(), SnapshotRequest{Tasks: true})
	if err != nil || len(tasks.Tasks) != 1 || !strings.Contains(tasks.Error, "broken-neighbour") || strings.Contains(tasks.Error, "Tag 构建记录") {
		t.Fatalf("task-only projection read history or lost scan diagnostics: %+v, %v", tasks, err)
	}
	if _, err := a.Config.Update(func(cConfig domain.AppConfig) (domain.AppConfig, error) {
		cConfig.Groups[0].Name = "external group change"
		return cConfig, nil
	}); err != nil {
		t.Fatal(err)
	}
	updated, err := a.SnapshotFor(context.Background(), SnapshotRequest{})
	if err != nil || !strings.Contains(updated.ConfigJSON, "external group change") {
		t.Fatalf("scoped read hid external configuration changes: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := a.SnapshotFor(ctx, SnapshotRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("scoped read ignored cancellation: %v", err)
	}
}
