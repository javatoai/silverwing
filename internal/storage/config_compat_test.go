package storage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
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
	config.DefaultBranchPrefix = "feature/"
	config.TagHistoryMaxGroups++
	config.DefaultWorkspaceToolIDs = []string{}
	config.BlockedGitWriteBranches = []string{"main"}
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
				if key != "schema" {
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
		fields["sidebarVisibility"] = json.RawMessage(`{"production":false,"future":null}`)
	})
	config, err = store.Load()
	if err != nil || *config.MaterialsGitSync != domain.DefaultMaterialsGitSyncSettings() || config.SidebarVisibility.Production || !config.SidebarVisibility.Services {
		t.Fatalf("nested defaults / explicit false changed: %+v %v", config, err)
	}
	config.MaterialsGitSync = nil
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	assertConfigJSON(t, readConfigObject(t, store, "integrations.json")["materialsGitSync"], `null`)
}

func TestConfigCompatibilityNestedRowIdentityAndDeletion(t *testing.T) {
	store := NewConfigStore(NewPaths(t.TempDir()))
	config, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	config.MeegleProjects = []domain.MeegleProjectConfig{{ProjectKey: "first", SimpleName: "one"}, {ProjectKey: "second", SimpleName: "two"}}
	config.Repositories = []domain.RepositoryConfig{{ID: "repo", Name: "repo", RootPath: "C:/repo", GitCommonDirectory: "C:/repo/.git"}}
	config.Groups[0].Services = []domain.GroupServiceConfig{{ID: "service", RepositoryID: "repo", DisplayName: "service", Enabled: true, GenbuServiceName: "service", MasterBranch: "origin/master", TestTagBaselineRef: "origin/release/test", DevelopmentTool: domain.DevelopmentToolIntelliJIdea,
		Modules: []domain.ServiceModuleConfig{{ID: "one", Name: "one", Strategy: domain.WorkspaceStandardWorktree, TagMode: domain.TagBuildMergeToTargetBranch}, {ID: "two", Name: "two", Strategy: domain.WorkspaceStandardWorktree, TagMode: domain.TagBuildMergeToTargetBranch}}}}
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	editConfigObject(t, store, "integrations.json", func(fields map[string]json.RawMessage) {
		fields["meegleProjects"] = json.RawMessage(`[{"project_key":"first","simple_name":"one","future":{"owner":"first"}},{"project_key":"second","simple_name":"two","future":{"owner":"second"}}]`)
	})
	editConfigObject(t, store, "services.json", func(fields map[string]json.RawMessage) {
		// Add extensions at the group, service and module levels.
		var groups []map[string]json.RawMessage
		_ = json.Unmarshal(fields["groups"], &groups)
		groups[0]["future"] = json.RawMessage(`"group"`)
		var services []map[string]json.RawMessage
		_ = json.Unmarshal(groups[0]["services"], &services)
		services[0]["future"] = json.RawMessage(`"service"`)
		var modules []map[string]json.RawMessage
		_ = json.Unmarshal(services[0]["modules"], &modules)
		modules[0]["future"] = json.RawMessage(`"one"`)
		modules[1]["future"] = json.RawMessage(`"two"`)
		services[0]["modules"], _ = json.Marshal(modules)
		groups[0]["services"], _ = json.Marshal(services)
		fields["groups"], _ = json.Marshal(groups)
	})
	config.MeegleProjects[0], config.MeegleProjects[1] = config.MeegleProjects[1], config.MeegleProjects[0]
	config.MeegleProjects[0].SimpleName = "renamed"
	service := &config.Groups[0].Services[0]
	service.Modules[0], service.Modules[1] = service.Modules[1], service.Modules[0]
	service.Modules[0].Name = "renamed"
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	var projects []map[string]json.RawMessage
	_ = json.Unmarshal(readConfigObject(t, store, "integrations.json")["meegleProjects"], &projects)
	assertConfigJSON(t, projects[0]["future"], `{"owner":"second"}`)
	assertConfigJSON(t, projects[1]["future"], `{"owner":"first"}`)
	var groups []struct {
		Future   string `json:"future"`
		Services []struct {
			Future  string                        `json:"future"`
			Modules []struct{ ID, Future string } `json:"modules"`
		} `json:"services"`
	}
	_ = json.Unmarshal(readConfigObject(t, store, "services.json")["groups"], &groups)
	if groups[0].Future != "group" || groups[0].Services[0].Future != "service" || groups[0].Services[0].Modules[0].ID != "two" || groups[0].Services[0].Modules[0].Future != "two" {
		t.Fatalf("nested identities crossed rows: %+v", groups)
	}
	config.MeegleProjects = config.MeegleProjects[:1]
	config.Groups[0].Services = nil
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(readConfigObject(t, store, "integrations.json")["meegleProjects"], &projects)
	if len(projects) != 1 || string(projects[0]["project_key"]) != `"second"` {
		t.Fatal("deleted row survived", projects)
	}
	assertConfigJSON(t, projects[0]["future"], `{"owner":"second"}`)
	if raw := readConfigObject(t, store, "services.json")["groups"]; bytes.Contains(raw, []byte(`"service"`)) || bytes.Contains(raw, []byte(`"modules"`)) {
		t.Fatalf("removed service retained extensions: %s", raw)
	}
}

func TestConfigCompatibilityNoIDRowsAndAmbiguity(t *testing.T) {
	type container struct {
		Commands []domain.BootstrapCommand `json:"commands"`
	}
	item := `{"name":"one","executable":"tool","future":9007199254740993}`
	other := `{"name":"two","executable":"tool","future":null}`
	typ := reflect.TypeOf(container{})
	before := []byte(`{"commands":[` + item + `,` + other + `]}`)
	after := []byte(`{"commands":[{"name":"two","executable":"tool"},{"name":"one","executable":"tool"}]}`)
	merged, err := mergeConfigJSON(before, after, typ, "test")
	if err != nil {
		t.Fatal(err)
	}
	var rows struct{ Commands []map[string]json.RawMessage }
	_ = json.Unmarshal(merged, &rows)
	assertConfigJSON(t, rows.Commands[0]["future"], `null`)
	assertConfigJSON(t, rows.Commands[1]["future"], `9007199254740993`)
	for _, test := range []struct{ before, after string }{
		{`{"commands":[` + item + `,` + item + `]}`, `{"commands":[{"name":"one","executable":"tool"}]}`},
		{string(before), `{"commands":[{"name":"changed","executable":"tool"}]}`},
	} {
		if _, err := mergeConfigJSON([]byte(test.before), []byte(test.after), typ, "test"); err == nil || !strings.Contains(err.Error(), "无法唯一匹配") {
			t.Fatalf("ambiguous mapping accepted: %v", err)
		}
	}
	if _, err := mergeConfigJSON(before, []byte(`{"commands":[]}`), typ, "test"); err != nil {
		t.Fatal("explicit delete-all blocked", err)
	}
}

func TestConfigCompatibilityAmbiguousSaveDoesNotTouchFiles(t *testing.T) {
	store := NewConfigStore(NewPaths(t.TempDir()))
	config, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	config.Repositories = []domain.RepositoryConfig{{ID: "repo", Name: "repo", RootPath: "C:/repo", GitCommonDirectory: "C:/repo/.git"}}
	config.Groups[0].Services = []domain.GroupServiceConfig{{ID: "service", RepositoryID: "repo", DisplayName: "service", MasterBranch: "origin/master", TestTagBaselineRef: "origin/release/test", DevelopmentTool: domain.DevelopmentToolIntelliJIdea,
		Modules:   []domain.ServiceModuleConfig{{ID: "module", Name: "module", Strategy: domain.WorkspaceStandardWorktree, TagMode: domain.TagBuildMergeToTargetBranch}},
		Bootstrap: domain.BootstrapConfig{Commands: []domain.BootstrapCommand{{Name: "command", Executable: "tool", Arguments: []string{}, WorkingDirectory: ".", TimeoutSeconds: 600, Enabled: true}}}}}
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Paths().Config, "services.json")
	raw, _ := os.ReadFile(path)
	// The unique command name cannot match any other setting in this fixture.
	raw = bytes.Replace(raw, []byte(`"name": "command"`), []byte(`"future": 9007199254740993, "name": "command"`), 1)
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	// Other changes to this service can still be saved without an ID on an
	// otherwise unchanged bootstrap row, even when its optional fields default.
	config.Groups[0].Services[0].Enabled = true
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	before, _ := store.readShardFiles()
	backupsBefore, _ := store.Backups()
	config.Groups[0].Services[0].Bootstrap.Commands[0].Name = "edited-without-id"
	config.Theme = domain.ThemeDark
	if err := store.Save(config); err == nil || !strings.Contains(err.Error(), "无法唯一匹配") {
		t.Fatal("ambiguous save was accepted", err)
	}
	after, _ := store.readShardFiles()
	for name, raw := range before {
		if !bytes.Equal(raw, after[name]) {
			t.Fatal("failed save changed shard", name)
		}
	}
	backupsAfter, _ := store.Backups()
	if len(backupsBefore) != len(backupsAfter) {
		t.Fatal("ambiguous save began a backup/write transaction")
	}
	config.Groups[0].Services[0].Bootstrap.Commands = nil
	if err := store.Save(config); err != nil {
		t.Fatal("explicit command removal blocked", err)
	}
	after, _ = store.readShardFiles()
	if bytes.Contains(after["services.json"], []byte(`"future"`)) {
		t.Fatal("deleted command retained its unknown fields")
	}
}

func TestConfigCompatibilityIdentifiedRowTypes(t *testing.T) {
	for _, row := range []struct {
		typ reflect.Type
		id  string
	}{
		{reflect.TypeOf(groupLayoutShard{}), "id"},
		{reflect.TypeOf(groupServicesShard{}), "groupId"},
		{reflect.TypeOf(domain.RepositoryConfig{}), "id"},
		{reflect.TypeOf(domain.GroupServiceConfig{}), "id"},
		{reflect.TypeOf(domain.ServiceModuleConfig{}), "id"},
		{reflect.TypeOf(domain.WorkspaceCommandConfig{}), "id"},
		{reflect.TypeOf(domain.SkillSource{}), "id"},
		{reflect.TypeOf(domain.CodexPluginMarketplaceSource{}), "id"},
		{reflect.TypeOf(domain.DevelopmentToolConfig{}), "type"},
		{reflect.TypeOf(domain.MeegleProjectConfig{}), "project_key"},
	} {
		t.Run(row.typ.Name(), func(t *testing.T) {
			before := []byte(fmt.Sprintf(`[{%q:"a","future":"a"},{%q:"b","future":"b"}]`, row.id, row.id))
			after := []byte(fmt.Sprintf(`[{%q:"b"},{%q:"c"},{%q:"a"}]`, row.id, row.id, row.id))
			merged, err := mergeConfigJSON(before, after, reflect.SliceOf(row.typ), "rows")
			if err != nil {
				t.Fatal(err)
			}
			var result []map[string]json.RawMessage
			_ = json.Unmarshal(merged, &result)
			if string(result[0]["future"]) != `"b"` || result[1]["future"] != nil || string(result[2]["future"]) != `"a"` {
				t.Fatalf("unknown fields crossed identities: %s", merged)
			}
			ambiguous := []byte(fmt.Sprintf(`[{%q:"a"},{%q:"a"}]`, row.id, row.id))
			if _, err := mergeConfigJSON(before, ambiguous, reflect.SliceOf(row.typ), "rows"); err == nil {
				t.Fatal("ambiguous ID accepted")
			}
		})
	}
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
		{"null list", "workspace.json", `{"schema":6,"repositories":null}`},
		{"missing row ID", "workspace.json", `{"schema":6,"repositories":[{"name":"r","rootPath":"C:/r","gitCommonDirectory":"C:/r/.git","future":true}]}`},
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
	stale.DefaultBranchPrefix = "before/"
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
				c.DefaultBranchPrefix += fmt.Sprint(i)
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
	if err != nil || len(config.DefaultBranchPrefix) != len("before/")+5 {
		t.Fatalf("updates lost: %q %v", config.DefaultBranchPrefix, err)
	}
	assertConfigJSON(t, readConfigObject(t, store, "workspace.json")["future"], futureConfigValue)
	if !slices.Equal(config.DefaultWorkspaceToolIDs, []string{"codex", "cursor"}) {
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
