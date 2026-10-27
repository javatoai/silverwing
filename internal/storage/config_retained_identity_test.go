package storage

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"silverwing-go/internal/domain"
)

func TestConfigSaveAndZipImportEnforceRetainedIdentityUniqueness(t *testing.T) {
	store := NewConfigStore(NewPaths(t.TempDir()))
	before, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	duplicate := before
	duplicate.Groups = append(append([]domain.GroupConfig(nil), before.Groups...), domain.GroupConfig{ID: "secondary", Name: " 默认组 "})
	if err := store.Save(duplicate); err == nil {
		t.Fatal("configuration save accepted duplicate normalized group names")
	}
	unchanged, err := store.Load()
	if err != nil || !reflect.DeepEqual(before, unchanged) {
		t.Fatalf("rejected save changed configuration: %+v err=%v", unchanged, err)
	}

	valid := domain.DefaultAppConfig()
	valid.MeegleProjects = []domain.MeegleProjectConfig{{ProjectKey: "Transit", SimpleName: "transit"}, {ProjectKey: "transit", SimpleName: "transit-lower"}}
	validArchive := configArchiveForTest(t, filepath.Join(t.TempDir(), "valid.zip"), valid)
	if _, err := store.PreviewImport(validArchive); err != nil {
		t.Fatalf("valid distinct archive was rejected during preview: %v", err)
	}
	if _, err := store.ImportFrom(validArchive); err != nil {
		t.Fatalf("valid distinct archive was rejected during import: %v", err)
	}

	conflict := valid
	conflict.Groups = append(append([]domain.GroupConfig(nil), valid.Groups...), domain.GroupConfig{ID: "secondary", Name: " 默认组 "})
	conflictArchive := configArchiveForTest(t, filepath.Join(t.TempDir(), "conflict.zip"), conflict)
	if _, err := store.PreviewImport(conflictArchive); err == nil {
		t.Fatal("archive preview accepted duplicate normalized group names")
	}
	current, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ImportFrom(conflictArchive); err == nil {
		t.Fatal("archive import accepted duplicate normalized group names")
	}
	after, err := store.Load()
	if err != nil || !reflect.DeepEqual(current, after) {
		t.Fatalf("rejected archive import changed configuration: %+v err=%v", after, err)
	}
}

func configArchiveForTest(t *testing.T, path string, config domain.AppConfig) string {
	t.Helper()
	shards := shardsFor(config)
	files := make(map[string][]byte, len(shardNames))
	for _, name := range shardNames {
		data, err := encodeShard(name, shards)
		if err != nil {
			t.Fatal(err)
		}
		var compact json.RawMessage
		if err := json.Unmarshal(data, &compact); err != nil {
			t.Fatal(err)
		}
		files[name] = compact
	}
	if err := writeZip(path, files); err != nil {
		t.Fatal(err)
	}
	return path
}
