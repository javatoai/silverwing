package storage

import (
	"bytes"
	"context"
	"encoding/json"

	"os"
	"path/filepath"
	"reflect"
	"slices"

	"sync"
	"testing"

	"silverwing-go/internal/domain"
)

const futureConfigValue = `{"large":9007199254740993123456789,"precise":1.234567890123456789e+100,"nested":[null,{"enabled":true,"text":"新版设置"}]}`

func TestConfigCompatibilityAllShardsReadSave(t *testing.T) {
	store := NewConfigStore(NewPaths(t.TempDir()))
	config, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range shardNames {
		editConfigObject(t, store, name, func(fields map[string]json.RawMessage) {
			fields["futureSetting"] = json.RawMessage(futureConfigValue)
		})
	}
	before, err := store.readShardFiles()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadForPreview(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, _ := store.readShardFiles()
	for _, name := range shardNames {
		if !bytes.Equal(before[name], after[name]) {
			t.Fatalf("read rewrote %s", name)
		}
	}
	config.Groups[0].Name = "修改组名"

	config.DefaultWorkspaceToolIDs = []string{}

	config.AIRequirementNamingEnabled = !config.AIRequirementNamingEnabled
	config.Theme = domain.ThemeDark
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	for _, name := range shardNames {
		fields := readConfigObject(t, store, name)
		assertConfigJSON(t, fields["futureSetting"], futureConfigValue)
	}
}

func TestConfigCompatibilityAlternatingVersions(t *testing.T) {
	// This DTO represents an older compatible executable with no knowledge of
	// materialsGitSync or any of the current integration settings beside AI.
	type oldIntegrations struct {
		Schema int  `json:"schema"`
		AI     bool `json:"aiRequirementNamingEnabled"`
	}
	store := NewConfigStore(NewPaths(t.TempDir()))
	config, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	settings := domain.DefaultMaterialsGitSyncSettings()
	config.MaterialsGitSync = &settings
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	editConfigObject(t, store, "integrations.json", func(fields map[string]json.RawMessage) {
		var object map[string]json.RawMessage
		_ = json.Unmarshal(fields["materialsGitSync"], &object)
		object["future"] = json.RawMessage(futureConfigValue)
		fields["materialsGitSync"], _ = json.Marshal(object)
	})
	for round := 0; round < 3; round++ {
		path := filepath.Join(store.Paths().Config, "integrations.json")
		raw, _ := os.ReadFile(path)
		var old oldIntegrations
		if err := decodeConfigShard(raw, &old, oldIntegrations{Schema: configShardSchema}, "integrations.json"); err != nil {
			t.Fatal(err)
		}
		old.AI = !old.AI
		known, _ := json.Marshal(old)
		merged, err := mergeConfigJSON(raw, known, reflect.TypeOf(old), "integrations.json")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, merged, 0o600); err != nil {
			t.Fatal(err)
		}
		config, err = store.Load()
		if err != nil || config.MaterialsGitSync.IntervalMinutes != 5+round {
			t.Fatalf("new version lost its settings: %+v %v", config.MaterialsGitSync, err)
		}
		config.MaterialsGitSync.IntervalMinutes++
		if err := store.Save(config); err != nil {
			t.Fatal(err)
		}
		var nested map[string]json.RawMessage
		_ = json.Unmarshal(readConfigObject(t, store, "integrations.json")["materialsGitSync"], &nested)
		assertConfigJSON(t, nested["future"], futureConfigValue)
	}
}

func TestConfigCompatibilityMissingOptionalDefaults(t *testing.T) {
	store := NewConfigStore(NewPaths(t.TempDir()))
	if _, err := store.Load(); err != nil {
		t.Fatal(err)
	}
	for _, name := range shardNames {
		editConfigObject(t, store, name, func(fields map[string]json.RawMessage) {
			for key := range fields {
				if key != "schema" && key != "edition" {
					delete(fields, key)
				}
			}
		})
	}
	config, err := store.Load()
	if err != nil || !reflect.DeepEqual(config, domain.DefaultAppConfig()) {
		t.Fatalf("optional defaults differ: %+v %v", config, err)
	}
	editConfigObject(t, store, "integrations.json", func(fields map[string]json.RawMessage) {
		fields["materialsGitSync"] = json.RawMessage(`{"repositoryUrl":"","future":null}`)
	})
	editConfigObject(t, store, "appearance.json", func(fields map[string]json.RawMessage) {
		fields["sidebarVisibility"] = json.RawMessage(`{"requirements":false,"future":null}`)
	})
	config, err = store.Load()
	if err != nil || *config.MaterialsGitSync != domain.DefaultMaterialsGitSyncSettings() || config.SidebarVisibility.Requirements || !config.SidebarVisibility.Tools {
		t.Fatalf("nested defaults / explicit false changed: %+v %v", config, err)
	}
	config.MaterialsGitSync = nil
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	assertConfigJSON(t, readConfigObject(t, store, "integrations.json")["materialsGitSync"], `null`)
}

func TestConfigCompatibilityImportUnknownOnlyChangesAndRestore(t *testing.T) {
	store := NewConfigStore(NewPaths(t.TempDir()))
	if _, err := store.Load(); err != nil {
		t.Fatal(err)
	}
	editConfigObject(t, store, "integrations.json", func(fields map[string]json.RawMessage) {
		fields["future"] = json.RawMessage(futureConfigValue)
	})
	files, err := store.readShardFiles()
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(files["integrations.json"], &fields)
	delete(fields, "future")
	fields["newFuture"] = json.RawMessage(`{"precise":90071992547409931234567890,"nil":null}`)
	files["integrations.json"], _ = json.Marshal(fields)
	archive := filepath.Join(t.TempDir(), "future.zip")
	if err := writeZip(archive, files); err != nil {
		t.Fatal(err)
	}
	if _, err := store.PreviewImport(archive); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ImportFrom(archive); err != nil {
		t.Fatal(err)
	}
	fields = readConfigObject(t, store, "integrations.json")
	if _, present := fields["future"]; present {
		t.Fatal("target extensions leaked into the source archive")
	}
	assertConfigJSON(t, fields["newFuture"], `{"precise":90071992547409931234567890,"nil":null}`)
	backups, err := store.Backups()
	if err != nil || len(backups) != 1 {
		t.Fatalf("unknown-only import was treated as a no-op: %+v %v", backups, err)
	}
	if _, err := store.PreviewRestore(backups[0].Path); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Restore(backups[0].Path); err != nil {
		t.Fatal(err)
	}
	fields = readConfigObject(t, store, "integrations.json")
	assertConfigJSON(t, fields["future"], futureConfigValue)
	if _, present := fields["newFuture"]; present {
		t.Fatal("restore retained extensions absent from backup")
	}
	export := filepath.Join(t.TempDir(), "export.zip")
	if _, err := store.ExportTo(export); err != nil {
		t.Fatal(err)
	}
	exported, err := readArchive(export)
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(exported.Files["integrations.json"], &fields)
	assertConfigJSON(t, fields["future"], futureConfigValue)
	empty := NewConfigStore(NewPaths(filepath.Join(t.TempDir(), "new-home")))
	if _, err := empty.ImportFrom(export); err != nil {
		t.Fatal(err)
	}
	assertConfigJSON(t, readConfigObject(t, empty, "integrations.json")["future"], futureConfigValue)
	before, _ := empty.Backups()
	if _, err := empty.ImportFrom(export); err != nil {
		t.Fatal(err)
	}
	after, _ := empty.Backups()
	if len(before) != len(after) {
		t.Fatal("identical archive created an unnecessary backup")
	}
}

func TestConfigCompatibilityInvalidContentStillRejected(t *testing.T) {
	for _, test := range []struct{ name, shard, contents string }{
		{"syntax", "integrations.json", `{"schema":6,`},
		{"trailing", "integrations.json", `{"schema":6} {}`},
		{"schema", "integrations.json", `{"schema":7,"future":true}`},
		{"missing schema", "integrations.json", `{"future":true}`},
		{"schema type", "integrations.json", `{"schema":"6"}`},
		{"known type", "integrations.json", `{"schema":6,"meegleExecutablePath":false,"future":true}`},
		{"known enum", "appearance.json", `{"schema":6,"theme":"FUTURE"}`},
		{"null boolean", "appearance.json", `{"schema":6,"sidebarVisibility":{"tools":null,"future":true}}`},
		{"null list", "layout.json", `{"schema":6,"edition":"product","groups":null}`},
		{"missing row ID", "layout.json", `{"schema":6,"edition":"product","groups":[{"name":"r","future":true}]}`},
		{"nested type", "integrations.json", `{"schema":6,"materialsGitSync":{"branch":false,"future":true}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := NewConfigStore(NewPaths(t.TempDir()))
			config, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(store.Paths().Config, test.shard)
			if err := os.WriteFile(path, []byte(test.contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := store.Load(); err == nil {
				t.Fatal("damaged configuration accepted")
			}
			if err := store.Save(config); err == nil {
				t.Fatal("save overwrote damaged configuration")
			}
			actual, _ := os.ReadFile(path)
			if string(actual) != test.contents {
				t.Fatal("error path rewrote the original file")
			}
		})
	}
	var action struct {
		Name string `json:"name"`
	}
	if err := domain.DecodeStrict([]byte(`{"name":"action","future":true}`), &action); err == nil {
		t.Fatal("global strict decoder was relaxed")
	}
}

func TestConfigCompatibilityConcurrentUpdatesAndLatestExtensions(t *testing.T) {
	store := NewConfigStore(NewPaths(t.TempDir()))
	stale, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	editConfigObject(t, store, "workspace.json", func(fields map[string]json.RawMessage) {
		fields["future"] = json.RawMessage(futureConfigValue)
	})

	stale.AIRequirementNamingModel = "before/"
	if err := store.Save(stale); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	failures := make(chan error, 5)
	for i := 0; i < 5; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			_, err := NewConfigStore(store.Paths()).Update(func(c domain.AppConfig) (domain.AppConfig, error) {
				c.AIRequirementNamingModel += "x"
				return c, nil
			})
			failures <- err
		}(i)
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	config, err := store.Load()
	if err != nil || len(config.AIRequirementNamingModel) != len("before/")+5 {
		t.Fatalf("updates lost: %q %v", config.AIRequirementNamingModel, err)
	}
	assertConfigJSON(t, readConfigObject(t, store, "workspace.json")["future"], futureConfigValue)
	if !slices.Equal(config.DefaultWorkspaceToolIDs, []string{"codex"}) {
		t.Fatal("unrelated settings changed")
	}
}

func editConfigObject(t *testing.T, store *ConfigStore, name string, change func(map[string]json.RawMessage)) {
	t.Helper()
	fields := readConfigObject(t, store, name)
	change(fields)
	data, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.Paths().Config, name), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func readConfigObject(t *testing.T, store *ConfigStore, name string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(store.Paths().Config, name))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	return fields
}

func assertConfigJSON(t *testing.T, actual []byte, expected string) {
	t.Helper()
	// Compact whitespace only; numbers never pass through a floating point value.
	var got, want bytes.Buffer
	if err := json.Compact(&got, actual); err != nil {
		t.Fatalf("invalid retained JSON %q: %v", actual, err)
	}
	if err := json.Compact(&want, []byte(expected)); err != nil {
		t.Fatal(err)
	}
	if got.String() != want.String() {
		t.Fatalf("retained JSON changed: %s, want %s", got.String(), want.String())
	}
}
