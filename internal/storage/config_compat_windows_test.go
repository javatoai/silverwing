//go:build windows

package storage

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"silverwing-go/internal/domain"
)

func TestConfigCompatibilityWindowsFailedSaveRollsBackUnknownFields(t *testing.T) {
	for _, operation := range []string{"save", "import"} {
		t.Run(operation, func(t *testing.T) {
			store := NewConfigStore(NewPaths(t.TempDir()))
			config, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			for _, name := range shardNames {
				editConfigObject(t, store, name, func(fields map[string]json.RawMessage) {
					fields["future"] = json.RawMessage(futureConfigValue)
				})
			}
			before, err := store.readShardFiles()
			if err != nil {
				t.Fatal(err)
			}
			config.DefaultBranchPrefix = "changed/"
			config.Theme = domain.ThemeDark
			archive := filepath.Join(t.TempDir(), "replacement.zip")
			if operation == "import" {
				files := map[string][]byte{}
				for _, name := range shardNames {
					files[name], err = encodeShard(name, shardsFor(config))
					if err != nil {
						t.Fatal(err)
					}
				}
				if err := writeZip(archive, files); err != nil {
					t.Fatal(err)
				}
			}
			// Appearance is replaced after workspace; refuse its replacement to
			// exercise a real multi-shard rollback with raw extension values.
			handle := holdAtomicTarget(t, filepath.Join(store.Paths().Config, "appearance.json"))
			if operation == "save" {
				err = store.Save(config)
			} else {
				_, err = store.ImportFrom(archive)
			}
			if closeErr := syscall.CloseHandle(handle); closeErr != nil {
				t.Fatal(closeErr)
			}
			if err == nil {
				t.Fatal("occupied target did not fail")
			}
			after, readErr := store.readShardFiles()
			if readErr != nil {
				t.Fatal(readErr)
			}
			for name, raw := range before {
				if !bytes.Equal(raw, after[name]) {
					t.Fatalf("rollback lost original bytes in %s", name)
				}
			}
			entries, _ := os.ReadDir(store.Paths().Config)
			for _, entry := range entries {
				if isShardStagingFile(entry.Name()) {
					t.Fatal("failed transaction left staging file", entry.Name())
				}
			}
			backups, err := store.Backups()
			if err != nil || len(backups) != 1 {
				t.Fatalf("recovery backup missing: %+v %v", backups, err)
			}
			backup, err := readArchive(backups[0].Path)
			if err != nil {
				t.Fatal(err)
			}
			for name, raw := range before {
				if !bytes.Equal(raw, backup.Files[name]) {
					t.Fatal("backup lost raw extension bytes", name)
				}
			}
		})
	}
}
