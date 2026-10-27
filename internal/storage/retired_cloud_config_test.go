package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"silverwing-go/internal/domain"
)

func TestRetiredCloudSettingsLoadImportAndBackup(t *testing.T) {
	store := NewConfigStore(NewPaths(t.TempDir()))
	if _, err := store.Load(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Paths().Config, "integrations.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err = json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	fields["larkExecutablePath"] = "C:/retired/lark.cmd"
	fields["requirementMaterialsFeishuTarget"] = "retired-invalid-value"
	fields["commandProxyTargets"] = []string{"CODEX", "LARK", "GIT"}
	data, _ = json.Marshal(fields)
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	config, err := store.Load()
	if err != nil || slices.Contains(config.CommandProxyTargets, domain.CommandProxyTarget("LARK")) {
		t.Fatalf("legacy settings: %+v %v", config.CommandProxyTargets, err)
	}
	archive := filepath.Join(t.TempDir(), "legacy.zip")
	if _, err = store.ExportTo(archive); err != nil {
		t.Fatal(err)
	}
	target := NewConfigStore(NewPaths(t.TempDir()))
	if _, err = target.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err = target.ImportFrom(archive); err != nil {
		t.Fatal(err)
	}
	if _, err = target.Load(); err != nil {
		t.Fatal(err)
	}
	backups, err := target.Backups()
	if err != nil || len(backups) == 0 {
		t.Fatalf("%v %v", backups, err)
	}
	if _, err = target.Restore(backups[0].Path); err != nil {
		t.Fatal(err)
	}
}
