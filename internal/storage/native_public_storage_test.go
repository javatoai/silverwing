package storage

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPublicConfigRejectsGenbuProxyTarget(t *testing.T) {
	store := NewConfigStore(NewPaths(t.TempDir()))
	if _, err := store.Load(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Paths().Config, "integrations.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	fields["commandProxyTargets"] = json.RawMessage(`[` + `"GENBU"` + `]`)
	data, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("public config accepted the private GENBU proxy target")
	}
}
