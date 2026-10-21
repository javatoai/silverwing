package storage

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"silverwing-go/internal/domain"
	"testing"
)

func TestConfigurationDefaultProxyInitializationAndMissingFields(t *testing.T) {
	paths := NewPaths(t.TempDir())
	store := NewConfigStore(paths)
	c, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	check := func(c domain.AppConfig) {
		t.Helper()
		if c.CommandProxyURL == nil || *c.CommandProxyURL != "http://127.0.0.1:7897" || len(c.CommandProxyTargets) != 1 || c.CommandProxyTargets[0] != domain.ProxyTargetCodex || !c.AIRequirementNamingEnabled || !c.AIRequirementNamingPrewarmEnabled || c.AIRequirementNamingModel != "gpt-6-luna" {
			t.Fatalf("wrong integrated defaults %+v", c)
		}
	}
	check(c)
	path := filepath.Join(paths.Config, "integrations.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"commandProxyUrl", "commandProxyTargets", "aiRequirementNamingEnabled", "aiRequirementNamingPrewarmEnabled", "aiRequirementNamingModel"} {
		delete(fields, key)
	}
	sparse, _ := json.Marshal(fields)
	if err := os.WriteFile(path, sparse, 0600); err != nil {
		t.Fatal(err)
	}
	c, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	check(c)
	after, _ := os.ReadFile(path)
	if !bytes.Equal(after, sparse) {
		t.Fatal("read rewrote missing-field user config")
	}
	fields["commandProxyUrl"] = json.RawMessage(`null`)
	fields["aiRequirementNamingEnabled"] = json.RawMessage(`false`)
	fields["aiRequirementNamingPrewarmEnabled"] = json.RawMessage(`false`)
	fields["aiRequirementNamingModel"] = json.RawMessage(`"gpt-5.6-luna"`)
	off, _ := json.Marshal(fields)
	os.WriteFile(path, off, 0600)
	c, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.CommandProxyURL != nil || len(c.CommandProxyTargets) != 0 || c.AIRequirementNamingEnabled || c.AIRequirementNamingPrewarmEnabled || c.AIRequirementNamingModel != "gpt-5.6-luna" {
		t.Fatal("explicit settings overwritten")
	}
	fields["commandProxyUrl"] = json.RawMessage(`"http://localhost:9080"`)
	fields["commandProxyTargets"] = json.RawMessage(`[]`)
	custom, _ := json.Marshal(fields)
	os.WriteFile(path, custom, 0600)
	c, err = store.Load()
	if err != nil || c.CommandProxyURL == nil || *c.CommandProxyURL != "http://localhost:9080" || len(c.CommandProxyTargets) != 0 {
		t.Fatalf("custom URL/empty targets %v %v", c.CommandProxyURL, err)
	}
	fields["commandProxyTargets"] = json.RawMessage(`false`)
	invalid, _ := json.Marshal(fields)
	os.WriteFile(path, invalid, 0600)
	if _, err := store.Load(); err == nil {
		t.Fatal("invalid target type accepted")
	}
}
