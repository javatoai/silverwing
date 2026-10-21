package desktop

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func TestConfigReadCacheInvalidationAndErrorIsolation(t *testing.T) {
	var cache configSourceReadCache[map[string]string]
	loads := 0
	load := func(source string) (map[string]string, error) {
		loads++
		value := map[string]string{"source": source}
		if source == "invalid" {
			return value, errors.New("invalid source")
		}
		return value, nil
	}
	for _, source := range []string{"first", "invalid", "restored"} {
		for attempt := 0; attempt < 2; attempt++ {
			value, err := cache.read(source, load)
			if source == "invalid" {
				if err == nil || value != nil {
					t.Fatal("an invalid replacement exposed cached or partial values")
				}
			} else if err != nil || value["source"] != source {
				t.Fatal("a changed source retained earlier values or errors")
			}
		}
	}
	if loads != 3 {
		t.Fatalf("unchanged sources, including failures, were decoded %d times", loads)
	}
	s := configurationReadFixture()
	s.configJSON = "null"
	s.saveConfigField("taskRoot", "D:/new")
	if s.lastError == "" {
		t.Fatal("a non-object configuration was accepted for editing")
	}
}

func configurationReadFixture() *app {
	s := taskNavigationFixture()
	s.configJSON = `{"taskRoot":"D:/tasks","enabled":true,"count":7,"nested":{"flag":true},"items":[{"name":"original","nested":[{"value":"saved"}]}],"services":[` +
		strings.TrimSuffix(strings.Repeat(`{"id":"service","modules":[{"id":"module","enabled":true}]},`, 200), ",") + `]}`
	s.snapshot.ConfigJSON = s.configJSON
	return s
}

func TestConfigurationReadsRefreshAfterSourceChanges(t *testing.T) {
	s := configurationReadFixture()
	if s.configString("taskRoot") != "D:/tasks" || !s.configBool("enabled", false) ||
		s.configInt("count", 0) != 7 || !s.configNestedBool("nested", "flag", false) || len(s.configArray("items")) != 1 {
		t.Fatal("initial configuration reads failed")
	}
	s.configJSON = `{"taskRoot":"D:/changed","enabled":false,"count":9,"nested":{"flag":false},"items":[]}`
	if s.configString("taskRoot") != "D:/changed" || s.configBool("enabled", true) ||
		s.configInt("count", 0) != 9 || s.configNestedBool("nested", "flag", true) || len(s.configArray("items")) != 0 {
		t.Fatal("configuration reads retained an earlier source")
	}
	for _, source := range []string{"", "{invalid", "null"} {
		s.configJSON = source
		if s.configString("taskRoot") != "" || !s.configBool("enabled", true) || s.configInt("count", 11) != 11 ||
			!s.configNestedBool("nested", "flag", true) || s.configArray("items") != nil {
			t.Fatalf("invalid or empty source exposed an earlier configuration: %q", source)
		}
	}
	s.applySnapshot(application.Snapshot{ConfigJSON: `{"taskRoot":"D:/restored","count":12}`})
	if s.configString("taskRoot") != "D:/restored" || s.configInt("count", 0) != 12 {
		t.Fatal("snapshot replacement did not refresh configuration reads")
	}
}

func TestConfigurationReadCopiesProtectSavedValues(t *testing.T) {
	s := configurationReadFixture()
	items := s.configArray("items")
	row := items[0].(map[string]any)
	row["name"] = "draft"
	row["nested"].([]any)[0].(map[string]any)["value"] = "draft nested"
	items[0] = nil
	fresh := s.configArray("items")[0].(map[string]any)
	if fresh["name"] != "original" || fresh["nested"].([]any)[0].(map[string]any)["value"] != "saved" {
		t.Fatal("an array draft mutated saved configuration reads")
	}
	draft, err := s.settingsConfigMap()
	if err != nil {
		t.Fatal(err)
	}
	draft["taskRoot"] = "D:/unsaved"
	draft["nested"].(map[string]any)["flag"] = false
	if s.configString("taskRoot") != "D:/tasks" || !s.configNestedBool("nested", "flag", false) {
		t.Fatal("an editable configuration map mutated saved configuration reads")
	}
	otherDraft, _ := s.settingsConfigMap()
	if otherDraft["taskRoot"] != "D:/tasks" || otherDraft["nested"].(map[string]any)["flag"] != true {
		t.Fatal("editable configuration maps share unsaved changes")
	}
}

func TestConfigurationReadsAvoidRepeatedFullDecode(t *testing.T) {
	s := configurationReadFixture()
	read := func() {
		_ = s.configString("taskRoot")
		_ = s.configBool("enabled", false)
		_ = s.configInt("count", 0)
		_ = s.configNestedBool("nested", "flag", false)
		_ = s.configArray("items")
	}
	read()
	if allocations := testing.AllocsPerRun(20, read); allocations > 32 {
		t.Fatalf("stable configuration reads allocate %.0f objects; repeated full JSON decoding must be avoided", allocations)
	}
}

func TestTypedConfigurationReadsKeepSourcesAndDraftsIndependent(t *testing.T) {
	s := taskNavigationFixture()
	config := domain.DefaultAppConfig()
	config.Groups = []domain.GroupConfig{{ID: "backend", Services: []domain.GroupServiceConfig{{
		ID: "service", DisplayName: "original service", Enabled: true, DevelopmentTool: domain.DevelopmentToolIntelliJIdea,
		Bootstrap: domain.BootstrapConfig{CopyRules: []domain.BootstrapCopyRule{}, Commands: []domain.BootstrapCommand{}},
		Modules: []domain.ServiceModuleConfig{{ID: "module", Strategy: domain.WorkspaceStandardWorktree, CustomCommands: []domain.WorkspaceCommandConfig{{
			ID: "command", Enabled: true, Arguments: []string{"saved"},
		}}}},
	}}}}
	encode := func() string {
		encoded, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		var validated domain.AppConfig
		if err := json.Unmarshal(encoded, &validated); err != nil {
			t.Fatalf("invalid configuration fixture: %v", err)
		}
		return string(encoded)
	}
	s.configJSON = encode()
	s.snapshot.ConfigJSON = s.configJSON
	w := application.Workspace{ServiceID: "service", ModuleID: "module", State: "READY"}
	commands := s.workspaceCommandsFor(s.tasks[0], w)
	if len(commands) != 1 || commands[0].Arguments[0] != "saved" {
		t.Fatal("typed configuration did not expose the saved command")
	}
	commands[0].Arguments[0] = "unsaved"
	if s.workspaceCommandsFor(s.tasks[0], w)[0].Arguments[0] != "saved" {
		t.Fatal("command arguments mutated the typed configuration cache")
	}
	group, err := s.taskAppendGroup(s.tasks[0])
	if err != nil {
		t.Fatal(err)
	}
	group.Services[0].DisplayName = "unsaved service"
	if s.taskAddableServices(s.tasks[0])[0].Name != "original service" {
		t.Fatal("task append draft mutated the snapshot configuration cache")
	}
	read := func() {
		_ = s.workspaceCommandsFor(s.tasks[0], w)
		_, _ = s.workspaceMergeConfig()
		_ = s.taskAddableServices(s.tasks[0])
	}
	read()
	if allocations := testing.AllocsPerRun(20, read); allocations > 32 {
		t.Fatalf("stable workspace rendering still decodes full typed configurations: %.0f allocations", allocations)
	}
	config.Groups[0].Services[0].DisplayName = "changed service"
	config.Groups[0].Services[0].Modules[0].CustomCommands[0].Arguments[0] = "changed"
	s.configJSON = encode()
	if s.workspaceCommandsFor(s.tasks[0], w)[0].Arguments[0] != "changed" || s.taskAddableServices(s.tasks[0])[0].Name != "original service" {
		t.Fatal("current and snapshot configuration sources were conflated")
	}
	s.snapshot.ConfigJSON = s.configJSON
	if s.taskAddableServices(s.tasks[0])[0].Name != "changed service" {
		t.Fatal("snapshot configuration change retained an earlier group")
	}
	valid := s.configJSON
	s.configJSON, s.snapshot.ConfigJSON = "invalid", "invalid"
	if len(s.workspaceCommandsFor(s.tasks[0], w)) != 0 || len(s.taskAddableServices(s.tasks[0])) != 0 {
		t.Fatal("invalid typed configuration exposed earlier values")
	}
	if _, ok := s.workspaceMergeConfig(); ok {
		t.Fatal("invalid configuration enabled merge actions")
	}
	s.configJSON, s.snapshot.ConfigJSON = valid, valid
	if len(s.workspaceCommandsFor(s.tasks[0], w)) != 1 || len(s.taskAddableServices(s.tasks[0])) != 1 {
		t.Fatal("typed configuration did not recover after a corrected source")
	}
}

func BenchmarkDesktopConfigurationReads(b *testing.B) {
	s := configurationReadFixture()
	for b.Loop() {
		_ = s.configString("taskRoot")
		_ = s.configBool("enabled", false)
		_ = s.configInt("count", 0)
		_ = s.configNestedBool("nested", "flag", false)
		_ = s.configArray("items")
	}
	b.ReportMetric(float64(len(s.configJSON)), "config-bytes")
	b.ReportAllocs()
}
