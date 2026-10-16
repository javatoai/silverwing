//go:build windows

package storage

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestConfigBackupDeleteRejectsWindowsJunction(t *testing.T) {
	store, backups := backupDeleteFixture(t)
	external := t.TempDir()
	marker := filepath.Join(external, "untouched.txt")
	if err := os.WriteFile(marker, []byte("external marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(backups[0].Path); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", backups[0].Path, external).CombinedOutput(); err != nil {
		t.Fatalf("create isolated junction: %s %v", out, err)
	}
	result, err := store.DeleteBackupsContext(context.Background(), []string{backups[0].ID})
	if err != nil || len(result.Failures) != 1 || len(result.DeletedIDs) != 0 {
		t.Fatalf("junction leaf accepted: %+v %v", result, err)
	}
	if info, err := os.Lstat(backups[0].Path); err != nil || !configBackupReparse(info) {
		t.Fatal("junction was removed", err)
	}
	paths := NewPaths(t.TempDir())
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", paths.Backups, external).CombinedOutput(); err != nil {
		t.Fatalf("create isolated root junction: %s %v", out, err)
	}
	if _, err := NewConfigStore(paths).Backups(); err == nil {
		t.Fatal("junction backups root accepted")
	}
	if err := os.Rename(store.paths.Backups, filepath.Join(store.paths.Home, "backups-original")); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", store.paths.Backups, external).CombinedOutput(); err != nil {
		t.Fatalf("replace isolated backups root: %s %v", out, err)
	}
	if result, err := store.DeleteBackupsContext(context.Background(), []string{backups[1].ID}); err == nil || len(result.DeletedIDs) != 0 {
		t.Fatal("cached identity bypassed changed root")
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "external marker" {
		t.Fatal("junction target changed")
	}
}
