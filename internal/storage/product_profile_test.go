package storage

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/productprofile"
)

func TestProductProfilePersistedSchemas(t *testing.T) {
	paths := NewPaths(t.TempDir())
	store := NewConfigStore(paths)
	config, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	layoutPath := filepath.Join(paths.Config, "layout.json")
	layoutJSON, err := os.ReadFile(layoutPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest := domain.DefaultTaskManifest()
	manifest.FolderName = "profile-task"
	manifest.TaskDirectoryName = "profile-task"
	manifest.CreatedAt = "2026-01-01T00:00:00Z"
	manifest.UpdatedAt = manifest.CreatedAt
	taskDir := filepath.Join(paths.Tasks, manifest.TaskDirectoryName)
	manifests := NewManifestStore()
	if err := manifests.Save(taskDir, manifest); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(taskDir, TaskManifestFileName)
	manifestJSON, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	for name, data := range map[string][]byte{"config": configJSON, "layout": layoutJSON, "manifest": manifestJSON} {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		marker, exists := fields["edition"]
		if productprofile.Edition == "" {
			if exists {
				t.Fatalf("developer %s gained an edition field", name)
			}
		} else if string(marker) != `"`+productprofile.Edition+`"` {
			t.Fatalf("%s edition = %s, want %q", name, marker, productprofile.Edition)
		}
	}
	wantMaterials := productprofile.DefaultMaterialsRoot(paths.Home)
	if wantMaterials == "" {
		if config.RequirementMaterialsRoot != nil {
			t.Fatal("developer configuration gained a materials root")
		}
	} else if config.RequirementMaterialsRoot == nil || *config.RequirementMaterialsRoot != wantMaterials {
		t.Fatalf("materials root = %v, want %q", config.RequirementMaterialsRoot, wantMaterials)
	}
	if productprofile.Edition == "" {
		return
	}
	for _, foreign := range []string{"", "qa", "product"} {
		if foreign == productprofile.Edition {
			continue
		}
		for _, missing := range []bool{false, true} {
			mutateEdition := func(data []byte) []byte {
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(data, &fields); err != nil {
					t.Fatal(err)
				}
				fields["edition"], _ = json.Marshal(foreign)
				if missing {
					delete(fields, "edition")
				}
				out, err := json.Marshal(fields)
				if err != nil {
					t.Fatal(err)
				}
				return out
			}
			foreignLayout := mutateEdition(layoutJSON)
			if err := os.WriteFile(layoutPath, foreignLayout, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Load(); err == nil {
				t.Fatalf("foreign/missing configuration edition %q accepted (missing=%v)", foreign, missing)
			}
			if after, err := os.ReadFile(layoutPath); err != nil || !bytes.Equal(after, foreignLayout) {
				t.Fatalf("rejected foreign configuration was rewritten: %v", err)
			}
			foreignManifest := mutateEdition(manifestJSON)
			if err := os.WriteFile(manifestPath, foreignManifest, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := manifests.Load(taskDir); err == nil {
				t.Fatalf("foreign/missing task edition %q accepted (missing=%v)", foreign, missing)
			} else if !missing && !strings.Contains(err.Error(), productprofile.Name) {
				t.Fatalf("task rejection lost current product identity: %v", err)
			}
			if after, err := os.ReadFile(manifestPath); err != nil || !bytes.Equal(after, foreignManifest) {
				t.Fatalf("rejected foreign task was rewritten: %v", err)
			}
		}
	}
}
