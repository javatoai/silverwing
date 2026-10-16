package platform

import (
	"os"
	"path/filepath"
	"testing"
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
