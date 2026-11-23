package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/domain"
)

func TestProfileConfigRebasesManagedPaths(t *testing.T) {
	root := t.TempDir()
	source := NewPaths(filepath.Join(root, "source"))
	source.ProfileID = "portable-profile"
	store := NewConfigStore(source)
	config, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if config.TaskRoot != nil {
		t.Fatalf("fresh profile unexpectedly has a task root: %q", *config.TaskRoot)
	}
	if _, err := os.Stat(source.Tasks); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("loading config created an internal task directory: %v", err)
	}
	assertProfileConfigPath(t, config.RequirementMaterialsRoot, filepath.Join(source.Home, "materials"))
	config.MaterialsGitSync = &domain.MaterialsGitSyncSettings{Enabled: true, RepositoryURL: "https://example.com/materials.git", Branch: "main", IntervalMinutes: 5}
	config.ProjectInstructionsGitURL = "https://example.com/instructions.git"
	external := filepath.Join(root, "external-tasks")
	unclean := root + string(filepath.Separator) + "." + string(filepath.Separator) + "external-tasks"
	config.TaskRoot = &unclean
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	workspace := readConfigObject(t, store, "workspace.json")
	externalJSON, _ := json.Marshal(external)
	if !bytes.Equal(workspace["taskRoot"], externalJSON) {
		t.Fatalf("external task root did not persist as a clean absolute path: %s", workspace["taskRoot"])
	}
	integrations := readConfigObject(t, store, "integrations.json")
	if _, present := integrations["requirementMaterialsRoot"]; present {
		t.Fatal("persisted an internal materials root")
	}
	projectURL, err := json.Marshal(config.ProjectInstructionsGitURL)
	if err != nil || !bytes.Equal(integrations["projectInstructionsGitUrl"], projectURL) {
		t.Fatalf("independent project instructions URL did not persist: %s", integrations["projectInstructionsGitUrl"])
	}
	export := filepath.Join(root, "settings.zip")
	if _, err := store.ExportTo(export); err != nil {
		t.Fatal(err)
	}
	moved := NewPaths(filepath.Join(root, "moved"))
	moved.ProfileID = source.ProfileID
	if err := os.Rename(source.Home, moved.Home); err != nil {
		t.Fatal(err)
	}
	movedStore := NewConfigStore(moved)
	loaded, err := movedStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	previewed, err := movedStore.PreviewImport(export)
	if err != nil {
		t.Fatal(err)
	}
	for _, observed := range []domain.AppConfig{loaded, previewed} {
		assertProfileConfigPath(t, observed.TaskRoot, external)
		assertProfileConfigPath(t, observed.RequirementMaterialsRoot, domain.ManagedTaskMaterialsRoot(moved.Home, config.MaterialsGitSettings()))
		if observed.ProjectInstructionsGitURL != config.ProjectInstructionsGitURL {
			t.Fatal("project instructions URL changed during relocation")
		}
	}
	for _, dir := range []string{external, moved.Tasks} {
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("configuration operations created a task directory at %s: %v", dir, err)
		}
	}
}

func TestProfileConfigIgnoresRetiredWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	paths := NewPaths(filepath.Join(root, "profile"))
	paths.ProfileID = "profile"
	store := NewConfigStore(paths)
	if _, err := store.Load(); err != nil {
		t.Fatal(err)
	}
	const name = "workspace.json"
	workspacePath := filepath.Join(paths.Config, name)
	oldTaskRoot, newTaskRoot := filepath.Join(root, "old-tasks"), filepath.Join(root, "new-tasks")
	fields := readConfigObject(t, store, name)
	fields["taskRoot"], _ = json.Marshal(oldTaskRoot)
	fields["workspaceRoot"] = json.RawMessage(`"relative-legacy-root"`)
	fields["extension"] = json.RawMessage(`{"workspaceRoot":"extension-value"}`)
	legacy, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(workspacePath, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	assertProfileConfigPath(t, config.TaskRoot, oldTaskRoot)
	if raw, err := os.ReadFile(workspacePath); err != nil || !bytes.Equal(raw, legacy) {
		t.Fatalf("loading settings changed the workspace shard: %v", err)
	}
	backup := exportConfigFixture(t, store)
	config.TaskRoot = &newTaskRoot
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	assertRetiredFieldRemoved := func() {
		t.Helper()
		current := readConfigObject(t, store, name)
		if _, present := current["workspaceRoot"]; present {
			t.Fatal("retired workspace root was written")
		}
		if !equalConfigJSON(current["extension"], fields["extension"]) {
			t.Fatalf("workspace extension changed: %s", current["extension"])
		}
	}
	assertRetiredFieldRemoved()
	preview, err := store.PreviewImport(backup)
	if err != nil {
		t.Fatal(err)
	}
	assertProfileConfigPath(t, preview.TaskRoot, oldTaskRoot)
	restored, err := store.ImportFrom(backup)
	if err != nil {
		t.Fatal(err)
	}
	assertProfileConfigPath(t, restored.TaskRoot, oldTaskRoot)
	assertRetiredFieldRemoved()
}

func TestProfileConfigTaskRootUpdateBackupAndImport(t *testing.T) {
	root := t.TempDir()
	paths := NewPaths(filepath.Join(root, "conf"))
	paths.ProfileID = "first-profile"
	store := NewConfigStore(paths)
	preview, err := store.LoadForPreview(context.Background())
	if err != nil || preview.TaskRoot != nil || store.Exists() {
		t.Fatalf("empty profile preview initialized a task root or configuration: %+v, %v", preview.TaskRoot, err)
	}
	if _, err := store.Load(); err != nil {
		t.Fatal(err)
	}
	backupPath := exportConfigFixture(t, store)
	configured := filepath.Join(root, "unavailable-external-tasks")
	updated, err := store.Update(func(config domain.AppConfig) (domain.AppConfig, error) {
		if config.TaskRoot != nil {
			t.Fatalf("fresh profile update supplied a task root: %q", *config.TaskRoot)
		}
		config.TaskRoot = &configured
		return config, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	assertProfileConfigPath(t, updated.TaskRoot, configured)
	export, err := store.ExportTo(filepath.Join(root, "settings.zip"))
	if err != nil {
		t.Fatal(err)
	}
	otherPaths := NewPaths(filepath.Join(root, "other-conf"))
	otherPaths.ProfileID = "second-profile"
	otherStore := NewConfigStore(otherPaths)
	imported, err := otherStore.ImportFrom(export)
	if err != nil {
		t.Fatal(err)
	}
	assertProfileConfigPath(t, imported.TaskRoot, configured)
	backup, err := store.PreviewImport(backupPath)
	if err != nil || backup.TaskRoot != nil {
		t.Fatalf("backup preview supplied a task root: %+v, %v", backup.TaskRoot, err)
	}
	restored, err := store.ImportFrom(backupPath)
	if err != nil || restored.TaskRoot != nil {
		t.Fatalf("restore supplied a task root: %+v, %v", restored.TaskRoot, err)
	}
	for _, dir := range []string{paths.Tasks, otherPaths.Tasks, configured} {
		if _, err := os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("configuration operations created a task directory at %s: %v", dir, err)
		}
	}
	assertConfigBackupDirectoryAbsent(t, store)
}

func TestProfileConfigRejectsInternalRootsInImportedSettings(t *testing.T) {
	paths := NewPaths(t.TempDir())
	paths.ProfileID = "profile"
	store := NewConfigStore(paths)
	if _, err := store.Load(); err != nil {
		t.Fatal(err)
	}
	files, err := store.readShardFiles()
	if err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct{ shard, key, value string }{
		{"workspace.json", "taskRoot", paths.Tasks},
		{"workspace.json", "taskRoot", filepath.Dir(paths.Home)},
		{"workspace.json", "taskRoot", "relative"},
		{"workspace.json", "taskRoot", ""},
		{"integrations.json", "requirementMaterialsRoot", paths.Home},
	} {
		t.Run(fixture.key, func(t *testing.T) {
			copyFiles := make(map[string][]byte, len(files))
			for name, content := range files {
				copyFiles[name] = content
			}
			var object map[string]json.RawMessage
			if err := json.Unmarshal(copyFiles[fixture.shard], &object); err != nil {
				t.Fatal(err)
			}
			object[fixture.key], _ = json.Marshal(fixture.value)
			copyFiles[fixture.shard], err = json.Marshal(object)
			if err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(t.TempDir(), "invalid.zip")
			if err := writeZip(file, copyFiles); err != nil {
				t.Fatal(err)
			}
			if _, err := store.PreviewImport(file); err == nil {
				t.Fatal("invalid path accepted in archive preview")
			}
			if _, err := store.ImportFrom(file); err == nil {
				t.Fatal("invalid path accepted in archive import")
			}
			if current, err := os.ReadFile(filepath.Join(paths.Config, fixture.shard)); err != nil || !bytes.Equal(current, files[fixture.shard]) {
				t.Fatalf("failing import changed shard: %v", err)
			}
		})
	}
}

func assertProfileConfigPath(t *testing.T, actual *string, expected string) {
	t.Helper()
	if actual == nil {
		t.Fatalf("missing configuration path, want %q", expected)
	}
	if *actual != expected {
		t.Fatalf("configuration path = %q, want %q", *actual, expected)
	}
}
