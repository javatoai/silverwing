package storage

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestWorkspaceToolDefaultsPreserveExplicitChoices(t *testing.T) {
	store := NewConfigStore(NewPaths(t.TempDir()))
	config, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(config.DefaultWorkspaceToolIDs, []string{"codex", "cursor"}) {
		t.Fatal(config.DefaultWorkspaceToolIDs)
	}
	for _, ids := range [][]string{{"codex"}, {"cursor"}, {}} {
		config.DefaultWorkspaceToolIDs = ids
		if err := store.Save(config); err != nil {
			t.Fatal(err)
		}
		reopened, err := store.Load()
		if err != nil || !slices.Equal(reopened.DefaultWorkspaceToolIDs, ids) {
			t.Fatalf("reopen replaced choice: %v %v", reopened.DefaultWorkspaceToolIDs, err)
		}
		export := filepath.Join(t.TempDir(), "tools.zip")
		if _, err := store.ExportTo(export); err != nil {
			t.Fatal(err)
		}
		target := NewConfigStore(NewPaths(t.TempDir()))
		if _, err := target.Load(); err != nil {
			t.Fatal(err)
		}
		imported, err := target.ImportFrom(export)
		if err != nil || !slices.Equal(imported.DefaultWorkspaceToolIDs, ids) {
			t.Fatalf("import replaced choice: %v %v", imported.DefaultWorkspaceToolIDs, err)
		}
		backups, err := target.Backups()
		if err != nil || len(backups) == 0 {
			t.Fatalf("backup: %v", err)
		}
		restored, err := target.Restore(backups[0].Path)
		if err != nil || !slices.Equal(restored.DefaultWorkspaceToolIDs, []string{"codex", "cursor"}) {
			t.Fatalf("restore lost old defaults: %v %v", restored.DefaultWorkspaceToolIDs, err)
		}
	}
}
