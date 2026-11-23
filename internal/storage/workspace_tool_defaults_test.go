package storage

import (
	"encoding/json"
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
	if !slices.Equal(config.DefaultWorkspaceToolIDs, []string{"codex"}) {
		t.Fatal(config.DefaultWorkspaceToolIDs)
	}
	for _, tc := range []struct {
		name string
		raw  string
		want []string
	}{
		{"missing", "", []string{"codex"}},
		{"codex", `["codex"]`, []string{"codex"}},
		{"cursor-only", `["cursor"]`, []string{}},
		{"legacy-and-other", `["custom","cursor","codex"]`, []string{"custom", "codex"}},
		{"none", `[]`, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			editConfigObject(t, store, "tools.json", func(fields map[string]json.RawMessage) {
				if tc.raw == "" {
					delete(fields, "defaultWorkspaceToolIds")
				} else {
					fields["defaultWorkspaceToolIds"] = json.RawMessage(tc.raw)
				}
			})
			reopened, err := store.Load()
			if err != nil || reopened.DefaultWorkspaceToolIDs == nil || !slices.Equal(reopened.DefaultWorkspaceToolIDs, tc.want) {
				t.Fatalf("load tool selection: %v %v", reopened.DefaultWorkspaceToolIDs, err)
			}
			export := filepath.Join(t.TempDir(), "tools.zip")
			if _, err := store.ExportTo(export); err != nil {
				t.Fatal(err)
			}
			target := NewConfigStore(NewPaths(t.TempDir()))
			imported, err := target.ImportFrom(export)
			if err != nil || imported.DefaultWorkspaceToolIDs == nil || !slices.Equal(imported.DefaultWorkspaceToolIDs, tc.want) {
				t.Fatalf("import tool selection: %v %v", imported.DefaultWorkspaceToolIDs, err)
			}
			imported.DefaultWorkspaceToolIDs = []string{"different", " Cursor "}
			if err := target.Save(imported); err != nil {
				t.Fatal(err)
			}
			assertConfigJSON(t, readConfigObject(t, target, "tools.json")["defaultWorkspaceToolIds"], `["different"]`)
			restored, err := target.ImportFrom(export)
			if err != nil || restored.DefaultWorkspaceToolIDs == nil || !slices.Equal(restored.DefaultWorkspaceToolIDs, tc.want) {
				t.Fatalf("restore tool selection: %v %v", restored.DefaultWorkspaceToolIDs, err)
			}
		})
	}
}
