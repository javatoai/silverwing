//go:build windows

package storage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
)

func backupDeleteFixture(t *testing.T) (*ConfigStore, []Backup) {
	t.Helper()
	paths := NewPaths(t.TempDir())
	for _, path := range []string{paths.Backups, paths.Config, paths.Tasks} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range map[string]string{"a": "first", "b": "second", "c": "third"} {
		if err := os.WriteFile(filepath.Join(paths.Backups, backupPrefix+name+".zip"), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(paths.Config, "untouched.json"), []byte("configuration fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(paths.Tasks, "untouched.txt"), []byte("task fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewConfigStore(paths)
	backups, err := store.Backups()
	if err != nil || len(backups) != 3 {
		t.Fatalf("listing: %d %v", len(backups), err)
	}
	return store, backups
}

func TestConfigBackupDeleteSelectedOnly(t *testing.T) {
	for _, count := range []int{1, 2} {
		t.Run(string(rune('0'+count)), func(t *testing.T) {
			store, backups := backupDeleteFixture(t)
			ids := []string{}
			for _, backup := range backups[:count] {
				ids = append(ids, backup.ID)
			}
			ids = append(ids, ids[0]) // A duplicate must be settled once.
			result, err := store.DeleteBackupsContext(context.Background(), ids)
			if err != nil || len(result.DeletedIDs) != count || len(result.Failures) != 0 || len(result.Backups) != 3-count {
				t.Fatalf("delete: %+v %v", result, err)
			}
			for _, backup := range backups[:count] {
				if _, err := os.Stat(backup.Path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("selected file was not removed")
				}
			}
			if _, err := os.Stat(backups[count].Path); err != nil {
				t.Fatal("unselected file changed", err)
			}
			for path, expected := range map[string]string{filepath.Join(store.paths.Config, "untouched.json"): "configuration fixture", filepath.Join(store.paths.Tasks, "untouched.txt"): "task fixture"} {
				data, err := os.ReadFile(path)
				if err != nil || string(data) != expected {
					t.Fatal("unrelated data changed", err)
				}
			}
		})
	}
}

func TestConfigBackupDeleteRejectsUnsafeAndChangedEntries(t *testing.T) {
	store, backups := backupDeleteFixture(t)
	outside := filepath.Join(t.TempDir(), backupPrefix+"outside.zip")
	if err := os.WriteFile(outside, []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][]string{nil, {outside}, {filepath.Join("..", filepath.Base(outside))}, {strings.Repeat("a", 32)}} {
		result, err := store.DeleteBackupsContext(context.Background(), ids)
		if err == nil && len(result.Failures) != 1 {
			t.Fatalf("unlisted input accepted: %+v %v", result, err)
		}
		if len(result.DeletedIDs) > 0 {
			t.Fatal("unlisted file was deleted")
		}
	}
	if err := os.WriteFile(backups[1].Path, []byte("changed after listing"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := store.DeleteBackupsContext(context.Background(), []string{backups[0].ID, backups[1].ID})
	if err != nil || len(result.DeletedIDs) != 1 || len(result.Failures) != 1 || result.Failures[0].ID != backups[1].ID {
		t.Fatalf("partial outcomes: %+v %v", result, err)
	}
	for _, path := range []string{outside, backups[1].Path, backups[2].Path} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("protected file changed: %s %v", path, err)
		}
	}
	// Replacing a listed leaf by a directory must never remove that directory.
	if err := os.Remove(backups[2].Path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(backups[2].Path, 0o755); err != nil {
		t.Fatal(err)
	}
	result, err = store.DeleteBackupsContext(context.Background(), []string{backups[2].ID})
	if err != nil || len(result.Failures) != 1 {
		t.Fatalf("directory accepted: %+v %v", result, err)
	}
	info, err := os.Stat(backups[2].Path)
	if err != nil || !info.IsDir() {
		t.Fatal("directory was removed")
	}
}

func TestConfigBackupDeleteCanceledEmptyAndStableIdentity(t *testing.T) {
	store, backups := backupDeleteFixture(t)
	listed, err := store.Backups()
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]string{}
	for _, backup := range backups {
		byPath[backup.Path] = backup.ID
	}
	for _, backup := range listed {
		if byPath[backup.Path] != backup.ID {
			t.Fatal("unchanged file identity changed during refresh")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := store.DeleteBackupsContext(ctx, []string{backups[0].ID})
	if !errors.Is(err, context.Canceled) || len(result.DeletedIDs) != 0 {
		t.Fatal("canceled delete progressed")
	}
	if _, err := os.Stat(backups[0].Path); err != nil {
		t.Fatal("canceled delete changed data")
	}
	lock, err := AcquireLock(filepath.Join(store.paths.Locks, configLockFile))
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.DeleteBackupsContext(context.Background(), []string{backups[0].ID})
	lock()
	if !errors.Is(err, ErrLockBusy) {
		t.Fatal("delete bypassed existing config lock", err)
	}
	empty := NewConfigStore(NewPaths(t.TempDir()))
	if listed, err := empty.Backups(); err != nil || len(listed) != 0 {
		t.Fatalf("empty list: %+v %v", listed, err)
	}
}

func TestConfigBackupDeleteRejectsSameMetadataReplacement(t *testing.T) {
	store, backups := backupDeleteFixture(t)
	selected := backups[0]
	data, err := os.ReadFile(selected.Path)
	if err != nil {
		t.Fatal(err)
	}
	// Keep the old inode alive under another name so even inode-reusing file
	// systems cannot accidentally give the replacement its identity.
	if err := os.Rename(selected.Path, filepath.Join(store.paths.Home, "retired-fixture.zip")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(selected.Path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(selected.Path, selected.ModifiedAt, selected.ModifiedAt); err != nil {
		t.Fatal(err)
	}
	result, err := store.DeleteBackupsContext(context.Background(), []string{selected.ID})
	if err != nil || len(result.DeletedIDs) != 0 || len(result.Failures) != 1 {
		t.Fatalf("replacement accepted: %+v %v", result, err)
	}
	if current, err := os.ReadFile(selected.Path); err != nil || string(current) != string(data) {
		t.Fatal("replacement was removed")
	}
}

func TestConfigBackupDeleteRejectsLinks(t *testing.T) {
	store, backups := backupDeleteFixture(t)
	external := filepath.Join(t.TempDir(), "outside.zip")
	if err := os.WriteFile(external, []byte("external"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(store.paths.Backups, backupPrefix+"link.zip")
	if err := os.Symlink(external, link); err != nil {
		t.Skipf("OS symlink privilege unavailable: %v", err)
	}
	listed, err := store.Backups()
	if err != nil || len(listed) != 3 {
		t.Fatalf("link was listed: %d %v", len(listed), err)
	}
	if err := os.Remove(backups[0].Path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, backups[0].Path); err != nil {
		t.Fatal(err)
	}
	result, err := store.DeleteBackupsContext(context.Background(), []string{backups[0].ID})
	if err != nil || len(result.Failures) != 1 || len(result.DeletedIDs) != 0 {
		t.Fatalf("link replacement accepted: %+v %v", result, err)
	}
	if data, err := os.ReadFile(external); err != nil || string(data) != "external" {
		t.Fatal("external target changed")
	}
	redirected := NewPaths(t.TempDir())
	if err := os.Symlink(store.paths.Backups, redirected.Backups); err != nil {
		t.Fatal(err)
	}
	if _, err := NewConfigStore(redirected).Backups(); err == nil {
		t.Fatal("redirected backups root accepted")
	}
}

func TestConfigBackupDeleteKeepsImportExportRestore(t *testing.T) {
	paths := NewPaths(t.TempDir())
	store := NewConfigStore(paths)
	config := domain.DefaultAppConfig()
	config.TaskRoot = &paths.Tasks
	config.RequirementMaterialsRoot = &paths.Cache
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	exported, err := store.ExportTo(filepath.Join(paths.Home, "export.zip"))
	if err != nil {
		t.Fatal(err)
	}
	changed := config
	changed.Theme = domain.ThemeDark
	if err := store.Save(changed); err != nil {
		t.Fatal(err)
	}
	backups, err := store.Backups()
	if err != nil || len(backups) != 1 {
		t.Fatalf("save backup: %+v %v", backups, err)
	}
	restored, err := store.Restore(backups[0].Path)
	if err != nil || restored.Theme != config.Theme {
		t.Fatal("restore changed", err)
	}
	if err := store.Save(changed); err != nil {
		t.Fatal(err)
	}
	imported, err := store.ImportFrom(exported)
	if err != nil || imported.Theme != config.Theme {
		t.Fatal("import/export changed", err)
	}
	backups, err = store.Backups()
	if err != nil || len(backups) == 0 {
		t.Fatal("recovery backups missing", err)
	}
	before, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	result, err := store.DeleteBackupsContext(context.Background(), []string{backups[0].ID})
	if err != nil || len(result.DeletedIDs) != 1 {
		t.Fatal("delete recovery backup", err)
	}
	after, err := store.Load()
	if err != nil || before.Theme != after.Theme || *after.TaskRoot != paths.Tasks || *after.RequirementMaterialsRoot != paths.Cache {
		t.Fatal("delete changed current config", err)
	}
}
