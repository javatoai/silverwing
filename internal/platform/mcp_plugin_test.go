package platform

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPluginMCPDisabledServerState(t *testing.T) {
	home := t.TempDir()
	manifestDir := filepath.Join(home, "plugins", "cache", "market", "plugin", ".codex-plugin")
	if err := os.MkdirAll(manifestDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "plugin.json"), []byte(`{"name":"plugin","mcpServers":{"example":{"command":"example"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	response := map[string]any{"config": map[string]any{"plugins": map[string]any{"plugin@market": map[string]any{
		"enabled": true, "mcp_servers": map[string]any{"example": map[string]any{"enabled": false}},
	}}}}
	servers, warnings := discoverPluginMCPs(filepath.Join(home, "config.toml"), response)
	if len(warnings) != 0 || len(servers) != 1 || servers[0].Active || servers[0].Raw["enabled"] != false {
		t.Fatalf("disabled plugin MCP was reported active: %#v %v", servers, warnings)
	}
}

func TestPluginMCPDiscoveryReadsEachManifestOnceAndPreservesSelection(t *testing.T) {
	home := t.TempDir()
	cache := filepath.Join(home, "plugins", "cache")
	stamp := time.Unix(1_700_000_000, 0)
	write := func(source, version, name, command string, offset int) string {
		t.Helper()
		path := filepath.Join(cache, source, version, ".codex-plugin", "plugin.json")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		data := fmt.Sprintf(`{"name":%q,"mcpServers":{"example":{"command":%q}}}`, name, command)
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		modified := stamp.Add(time.Duration(offset) * time.Second)
		if err := os.Chtimes(path, modified, modified); err != nil {
			t.Fatal(err)
		}
		return path
	}
	write("market", "old", "plugin", "old", 0)
	write("market", "new-a", "plugin", "market-new", 2)
	write("market", "new-b", "plugin", "tie-later-path", 2)
	write("other", "new", "plugin", "other-new", 3)
	invalid := write("market", "invalid", "unused", "unused", 4)
	if err := os.WriteFile(invalid, []byte(`{broken`), 0600); err != nil {
		t.Fatal(err)
	}
	oversized := write("market", "oversized", "plugin", "oversized", 5)
	if err := os.WriteFile(oversized, []byte(strings.Repeat(" ", int(maxPluginBytes)+1)), 0600); err != nil {
		t.Fatal(err)
	}
	response := map[string]any{"config": map[string]any{"plugins": map[string]any{
		"plugin@MARKET": map[string]any{"enabled": true},
		"plugin@other":  map[string]any{"enabled": true},
		"plugin":        map[string]any{"enabled": true},
		"unused@market": map[string]any{"enabled": true},
	}}}
	reads := make(map[string]int)
	servers, warnings := discoverPluginMCPsWithReader(filepath.Join(home, "config.toml"), response, func(path string, limit int64) ([]byte, error) {
		reads[path]++
		return readBounded(path, limit)
	})
	if len(warnings) != 0 || len(servers) != 3 || len(reads) != 6 {
		t.Fatalf("unexpected discovery: servers=%+v warnings=%v reads=%v", servers, warnings, reads)
	}
	want := map[string]string{"plugin@MARKET": "market-new", "plugin@other": "other-new", "plugin": "other-new"}
	for _, server := range servers {
		if server.Raw["command"] != want[server.PluginID] {
			t.Fatalf("source/mtime/tie selection changed: %+v", server)
		}
	}
	for path, count := range reads {
		if count != 1 {
			t.Fatalf("manifest read %d times: %s", count, path)
		}
	}
	// A later operation gets a fresh index rather than retaining decoded state.
	write("market", "new-a", "plugin", "updated", 6)
	servers, _ = discoverPluginMCPs(filepath.Join(home, "config.toml"), response)
	for _, server := range servers {
		if server.PluginID == "plugin@MARKET" && server.Raw["command"] != "updated" {
			t.Fatalf("discovery reused a prior operation's manifest: %+v", server)
		}
	}
}

func TestPluginMCPManifestIndexBoundsCandidatesAndDepth(t *testing.T) {
	cache := t.TempDir()
	for i := 0; i < 501; i++ {
		path := filepath.Join(cache, "market", fmt.Sprintf("%03d", i), ".codex-plugin", "plugin.json")
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"name":"plugin-%03d"}`, i)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	reads := 0
	index := indexPluginMCPManifests(cache, func(path string, limit int64) ([]byte, error) {
		reads++
		return readBounded(path, limit)
	})
	if reads != 500 || len(index) != 1000 {
		t.Fatalf("candidate bound changed: reads=%d keys=%d", reads, len(index))
	}
	if _, found := index[pluginMCPManifestKey{name: "plugin-500"}]; found {
		t.Fatal("indexed a candidate after the original 500-manifest boundary")
	}
	deepRoot := t.TempDir()
	deep := filepath.Join(deepRoot, "market", "a", "b", "c", "d", "e", "f", ".codex-plugin", "plugin.json")
	if err := os.MkdirAll(filepath.Dir(deep), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(deep, []byte(`{"name":"too-deep"}`), 0600); err != nil {
		t.Fatal(err)
	}
	index = indexPluginMCPManifests(deepRoot, readBounded)
	if _, found := index[pluginMCPManifestKey{name: "too-deep"}]; found {
		t.Fatal("walk escaped its original depth bound")
	}
}

func TestPluginMCPSourceIndexKeepsEqualFoldSemantics(t *testing.T) {
	for _, pair := range [][2]string{{"MARKET", "market"}, {"Σ", "ς"}, {"K", "K"}} {
		if !strings.EqualFold(pair[0], pair[1]) || pluginMCPSourceKey(pair[0]) != pluginMCPSourceKey(pair[1]) {
			t.Fatalf("source index changed EqualFold matching: %q", pair)
		}
	}
}
