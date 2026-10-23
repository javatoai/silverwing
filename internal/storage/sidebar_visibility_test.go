package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/domain"
)

func TestSidebarVisibilityConfigurationDefaultsAndRoundTrip(t *testing.T) {
	store := NewConfigStore(NewPaths(t.TempDir()))
	config, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	all := domain.SidebarVisibilitySettings{Requirements: true, Tools: true, Production: true, Services: true}
	if config.SidebarVisibility != all {
		t.Fatalf("new configuration hid existing navigation: %+v", config.SidebarVisibility)
	}
	path := filepath.Join(store.Paths().Config, "appearance.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		raw  json.RawMessage
		want domain.SidebarVisibilitySettings
	}{
		{"old config", nil, all},
		{"partial config", json.RawMessage(`{"requirements":false}`), domain.SidebarVisibilitySettings{Tools: true, Production: true, Services: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			delete(fields, "sidebarVisibility")
			if tc.raw != nil {
				fields["sidebarVisibility"] = tc.raw
			}
			encoded, _ := json.Marshal(fields)
			if err := os.WriteFile(path, encoded, 0600); err != nil {
				t.Fatal(err)
			}
			loaded, err := NewConfigStore(store.Paths()).Load()
			if err != nil || loaded.SidebarVisibility != tc.want {
				t.Fatalf("existing config lost defaults: %+v %v", loaded.SidebarVisibility, err)
			}
		})
	}
	config.SidebarVisibility = domain.SidebarVisibilitySettings{Tools: true}
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	loaded, err := NewConfigStore(store.Paths()).Load()
	if err != nil || loaded.SidebarVisibility != config.SidebarVisibility {
		t.Fatalf("restart lost visibility: %+v %v", loaded.SidebarVisibility, err)
	}
	archive := filepath.Join(t.TempDir(), "settings.zip")
	if _, err := store.ExportTo(archive); err != nil {
		t.Fatal(err)
	}
	target := NewConfigStore(NewPaths(t.TempDir()))
	if _, err := target.Load(); err != nil {
		t.Fatal(err)
	}
	imported, err := target.ImportFrom(archive)
	if err != nil || imported.SidebarVisibility != config.SidebarVisibility {
		t.Fatalf("export/import lost visibility: %+v %v", imported.SidebarVisibility, err)
	}
	backups, err := target.Backups()
	if err != nil || len(backups) == 0 {
		t.Fatalf("import backup missing: %v", err)
	}
	restored, err := target.Restore(backups[0].Path)
	if err != nil || restored.SidebarVisibility != all {
		t.Fatalf("backup restore lost original visibility: %+v %v", restored.SidebarVisibility, err)
	}
}
