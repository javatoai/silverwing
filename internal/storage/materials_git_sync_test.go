package storage

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"silverwing-go/internal/domain"
)

func TestMaterialsGitSettingsRoundTripExportAndBackup(t *testing.T) {
	store := NewConfigStore(NewPaths(t.TempDir()))
	config, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := config.MaterialsGitSettings(); got.Enabled || got.Branch != "master" || got.IntervalMinutes != 5 {
		t.Fatal(got)
	}
	s := domain.MaterialsGitSyncSettings{Enabled: true, RepositoryURL: "https://gitlab.example.com/team/docs.git", Branch: "docs/team", IntervalMinutes: 17}
	config.MaterialsGitSync = &s
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Load()
	if err != nil || !reflect.DeepEqual(reopened.MaterialsGitSync, &s) {
		t.Fatalf("reopen: %+v %v", reopened.MaterialsGitSync, err)
	}
	data, err := os.ReadFile(filepath.Join(store.Paths().Config, "integrations.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 {
		t.Fatal("missing integration shard")
	}
	archive := filepath.Join(t.TempDir(), "config.zip")
	if _, err := store.ExportTo(archive); err != nil {
		t.Fatal(err)
	}
	target := NewConfigStore(NewPaths(t.TempDir()))
	if _, err := target.Load(); err != nil {
		t.Fatal(err)
	}
	imported, err := target.ImportFrom(archive)
	if err != nil || !reflect.DeepEqual(imported.MaterialsGitSync, &s) {
		t.Fatalf("import: %+v %v", imported.MaterialsGitSync, err)
	}
	backups, err := target.Backups()
	if err != nil || len(backups) == 0 {
		t.Fatal("backup", err)
	}
	restored, err := target.Restore(backups[0].Path)
	if err != nil || restored.MaterialsGitSettings().Enabled {
		t.Fatalf("restore: %+v %v", restored.MaterialsGitSettings(), err)
	}
	bad := s
	bad.IntervalMinutes = 0
	config.MaterialsGitSync = &bad
	if err := store.Save(config); err == nil {
		t.Fatal("invalid settings saved")
	}
	kept, err := store.Load()
	if err != nil || kept.MaterialsGitSettings() != s {
		t.Fatal("failed save lost previous settings", err)
	}
}
