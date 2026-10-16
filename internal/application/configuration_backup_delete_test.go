//go:build windows

package application

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/storage"
)

func TestConfigurationBackupDeleteRequiresConfirmedListedIDs(t *testing.T) {
	paths := storage.NewPaths(t.TempDir())
	if err := os.MkdirAll(paths.Backups, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(paths.Backups, "silverwing-config-fixture.zip")
	if err := os.WriteFile(file, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := &App{Paths: paths, Config: storage.NewConfigStore(paths)}
	ctx := context.Background()
	listed, err := a.Dispatch(ctx, Action{Kind: "config.backups"})
	if err != nil {
		t.Fatal(err)
	}
	backup := listed.Data.([]storage.Backup)[0]
	if _, err := a.Dispatch(ctx, Action{Kind: "config.delete-backups", Path: file, Payload: map[string]any{"ids": []string{backup.ID}}}); err == nil {
		t.Fatal("unconfirmed delete was accepted")
	}
	if _, err := a.Dispatch(ctx, Action{Kind: "config.delete-backups", Path: file, Payload: map[string]any{"confirmed": true, "ids": []string{file}}}); err == nil {
		t.Fatal("absolute delete path was accepted")
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("invalid actions changed the fixture")
	}
	result, err := a.Dispatch(ctx, Action{Kind: "config.delete-backups", Payload: map[string]any{"confirmed": true, "ids": []string{backup.ID}}})
	if err != nil || len(result.Data.(storage.BackupDeleteResult).DeletedIDs) != 1 {
		t.Fatalf("confirmed typed delete: %+v %v", result, err)
	}
}
