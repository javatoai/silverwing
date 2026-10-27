package storage

import (
	"archive/zip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/domain"
)

func TestNativeConfigRejectsInvalidKnownFields(t *testing.T) {
	for _, test := range []struct {
		name  string
		field string
		value json.RawMessage
	}{
		{name: "invalid path type", field: "codexExecutablePath", value: json.RawMessage(`false`)},
		{name: "invalid proxy target", field: "commandProxyTargets", value: json.RawMessage(`["CODEXX"]`)},
	} {
		t.Run(test.name, func(t *testing.T) {
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
			fields[test.field] = test.value
			data, err = json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Load(); err == nil {
				t.Fatal("invalid native configuration was accepted")
			}
		})
	}
}

func TestNativeConfigSaveImportExportBackupRestoreRoundTrip(t *testing.T) {
	sourcePaths := NewPaths(filepath.Join(t.TempDir(), "source"))
	sourceStore := NewConfigStore(sourcePaths)
	source, err := sourceStore.Load()
	if err != nil {
		t.Fatal(err)
	}

	source.AIRequirementNamingModel = "source/"
	source.Theme = domain.ThemeDark
	if err := sourceStore.Save(source); err != nil {
		t.Fatal(err)
	}
	loaded, err := sourceStore.Load()
	if err != nil || loaded.AIRequirementNamingModel != source.AIRequirementNamingModel || loaded.Theme != source.Theme {
		t.Fatalf("saved config did not round trip: %+v %v", loaded, err)
	}
	exportPath := filepath.Join(sourcePaths.Home, "native-export.zip")
	if _, err := sourceStore.ExportTo(exportPath); err != nil {
		t.Fatal(err)
	}

	targetStore := NewConfigStore(NewPaths(filepath.Join(t.TempDir(), "target")))
	target, err := targetStore.Load()
	if err != nil {
		t.Fatal(err)
	}

	target.AIRequirementNamingModel = "target/"
	target.Theme = domain.ThemeLight
	if err := targetStore.Save(target); err != nil {
		t.Fatal(err)
	}
	imported, err := targetStore.ImportFrom(exportPath)
	if err != nil || imported.AIRequirementNamingModel != "source/" || imported.Theme != domain.ThemeDark {
		t.Fatalf("import did not replace native values: %+v %v", imported, err)
	}
	backups, err := targetStore.Backups()
	if err != nil || len(backups) == 0 {
		t.Fatalf("import backup missing: %+v %v", backups, err)
	}
	restored, err := targetStore.Restore(backups[0].Path)
	if err != nil || restored.AIRequirementNamingModel != "target/" || restored.Theme != domain.ThemeLight {
		t.Fatalf("backup did not restore prior config: %+v %v", restored, err)
	}
}

func TestNativeManifestRoundTripRejectsUnknownField(t *testing.T) {
	dir := t.TempDir()
	store := NewManifestStore()
	manifest := domain.DefaultTaskManifest()
	manifest.FolderName = "folder"
	manifest.TaskDirectoryName = "folder"

	manifest.CreatedAt = "2026-10-21T00:00:00Z"
	manifest.UpdatedAt = manifest.CreatedAt
	if err := store.Save(dir, manifest); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load(dir)
	if err != nil || loaded.FolderName != manifest.FolderName {
		t.Fatalf("manifest did not round trip: %+v %v", loaded, err)
	}

	path := filepath.Join(dir, TaskManifestFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	fields["folderNmae"] = json.RawMessage(`"typo"`)
	data, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(dir); err == nil {
		t.Fatal("unknown manifest field was accepted")
	}
}

func TestNativeConfigRejectsNonRegularShardAndArchiveEntries(t *testing.T) {
	store := NewConfigStore(NewPaths(filepath.Join(t.TempDir(), "config")))
	if _, err := store.Load(); err != nil {
		t.Fatal(err)
	}
	shardPath := filepath.Join(store.Paths().Config, "appearance.json")
	if err := os.Remove(shardPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(shardPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil {
		t.Fatal("directory was accepted as a config shard")
	}
	if err := os.Remove(shardPath); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, shardPath); err == nil {
		if store.Exists() {
			t.Fatal("symlink was reported as a regular config shard")
		}
		if _, err := store.Load(); err == nil {
			t.Fatal("symlink was accepted as a config shard")
		}
	}

	validStore := NewConfigStore(NewPaths(filepath.Join(t.TempDir(), "archive-source")))
	if _, err := validStore.Load(); err != nil {
		t.Fatal(err)
	}
	validArchive := filepath.Join(validStore.Paths().Home, "valid.zip")
	if _, err := validStore.ExportTo(validArchive); err != nil {
		t.Fatal(err)
	}
	nonRegularArchive := filepath.Join(validStore.Paths().Home, "non-regular.zip")
	rewriteArchiveEntryMode(t, validArchive, nonRegularArchive, "appearance.json")
	if _, err := readArchive(nonRegularArchive); err == nil {
		t.Fatal("non-regular ZIP entry was accepted")
	}
}

func rewriteArchiveEntryMode(t *testing.T, source, target, nonRegularName string) {
	t.Helper()
	reader, err := zip.OpenReader(source)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	file, err := os.Create(target)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, entry := range reader.File {
		input, err := entry.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, readErr := io.ReadAll(input)
		closeErr := input.Close()
		if readErr != nil {
			t.Fatal(readErr)
		}
		if closeErr != nil {
			t.Fatal(closeErr)
		}
		header := entry.FileHeader
		if entry.Name == nonRegularName {
			header.SetMode(os.ModeSymlink | 0o777)
		}
		output, err := writer.CreateHeader(&header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := output.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
