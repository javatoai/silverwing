package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/domain"
)

func TestOptionalCLIConfigPreservesLegacyPathsAndExplicitSettings(t *testing.T) {
	paths := NewPaths(t.TempDir())
	store := NewConfigStore(paths)
	cfg, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range []string{"lark", "genbu", "dragon"} {
		if cfg.OptionalCLIEnabled(tool) || cfg.OptionalCLIPath(tool) != "" {
			t.Fatalf("fresh config enables %s", tool)
		}
	}

	shardPath := filepath.Join(paths.Config, "integrations.json")
	data, err := os.ReadFile(shardPath)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	legacyFields := map[string]json.RawMessage{
		"genbuExecutablePath":          json.RawMessage(`"genbu-fixture"`),
		"genbuExecutableAutoDetected":  json.RawMessage(`true`),
		"dragonExecutablePath":         json.RawMessage(`"dragon-fixture"`),
		"dragonExecutableAutoDetected": json.RawMessage(`true`),
	}
	for key, raw := range legacyFields {
		if _, ok := fields[key]; ok {
			t.Fatalf("fresh configuration persisted retired key %s", key)
		}
		fields[key] = raw
	}
	// Recreate the integrations shard written before the optional switches.
	fields["larkExecutablePath"] = json.RawMessage(`"  lark-fixture  "`)
	delete(fields, "optionalCLIs")
	data, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shardPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	legacy, err := NewConfigStore(paths).Load()
	if err != nil {
		t.Fatal(err)
	}
	if legacy.OptionalCLIs != (domain.OptionalCLISettings{}) || !legacy.OptionalCLIEnabled("lark") || legacy.OptionalCLIPath("lark") != "lark-fixture" || legacy.OptionalCLIEnabled("genbu") || legacy.OptionalCLIEnabled("dragon") {
		t.Fatalf("legacy config lost the path-based defaults: %#v", legacy.OptionalCLIs)
	}
	archive := filepath.Join(t.TempDir(), "legacy-config.zip")
	if _, err := store.ExportTo(archive); err != nil {
		t.Fatal(err)
	}
	imported, err := NewConfigStore(NewPaths(t.TempDir())).ImportFrom(archive)
	if err != nil {
		t.Fatal(err)
	}
	if imported.GenbuExecutablePath == nil || *imported.GenbuExecutablePath != "genbu-fixture" || !imported.GenbuExecutableAutoDetected ||
		imported.DragonExecutablePath == nil || *imported.DragonExecutablePath != "dragon-fixture" || !imported.DragonExecutableAutoDetected ||
		imported.OptionalCLIPath("genbu") != "" || imported.OptionalCLIPath("dragon") != "" {
		t.Fatal("legacy archive lost stored values or activated a retired executable")
	}

	disabled, enabled := false, true
	legacy.OptionalCLIs.Lark, legacy.OptionalCLIs.Dragon = &disabled, &enabled
	if err := store.Save(legacy); err != nil {
		t.Fatal(err)
	}
	saved, err := NewConfigStore(paths).Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.OptionalCLIEnabled("lark") || saved.OptionalCLIPath("lark") != "lark-fixture" || saved.OptionalCLIs.Lark == nil || saved.OptionalCLIs.Genbu != nil || !saved.OptionalCLIEnabled("dragon") || saved.OptionalCLIPath("dragon") != "" || saved.OptionalCLIEnabled("codex") {
		t.Fatalf("roundtrip altered optional CLI settings: %#v", saved.OptionalCLIs)
	}
}
