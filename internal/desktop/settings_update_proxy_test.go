package desktop

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSettingsProxyPreservesLegacyUpdateTargetOnEdit(t *testing.T) {
	config := map[string]any{"commandProxyUrl": "http://127.0.0.1:7897", "commandProxyTargets": []any{"CODEX", "APP_UPDATES", "GIT", "WORKSPACE_COMMAND", "MEEGLE", "GENBU"}}
	_, inputs := settingsConfigInputValues(config)
	for _, removed := range []string{"APP_UPDATES", "GIT", "WORKSPACE_COMMAND", "MEEGLE", "GENBU"} {
		if _, present := inputs["proxy-target:"+removed]; present {
			t.Errorf("removed target %s still supplies settings input", removed)
		}
	}
	draft := settingsProxyDraft{scheme: "http", host: "127.0.0.1", port: "7898", targets: []string{"CODEX"}}
	if err := updateSettingsProxyConfig(config, draft); err != nil {
		t.Fatal(err)
	}
	if targets := mapsString(config["commandProxyTargets"]); !slices.Equal(targets, []string{"CODEX", "APP_UPDATES"}) {
		t.Fatalf("editing the proxy lost existing targets: %v", targets)
	}
	draft.host = ""
	if err := updateSettingsProxyConfig(config, draft); err != nil {
		t.Fatal(err)
	}
	if targets := mapsString(config["commandProxyTargets"]); len(targets) != 0 || config["commandProxyUrl"] != nil {
		t.Fatalf("clearing the proxy left an endpoint or targets: %v", targets)
	}
}

func TestSettingsProxyOnlyRendersSupportedTargets(t *testing.T) {
	s := newApp(nil, Options{CapturePath: "proxy-targets-test"})
	s.snapshot.Home = t.TempDir()
	s.settingsUnit.state.configJSON = `{"commandProxyUrl":"http://127.0.0.1:7897","commandProxyTargets":["CODEX","APP_UPDATES","GIT","WORKSPACE_COMMAND","MEEGLE","GENBU"]}`
	t.Cleanup(s.settingsUnit.stopSettingsAutosave)
	v := ui.NewTester(func(c *ui.Context) { s.settingsProxy(c, setTheme(c, false)) }, 960, 680)
	for _, title := range []string{"应用更新", "Git", "服务快捷命令", "Meegle CLI", "Genbu CLI"} {
		if _, found := v.Find(title); found {
			t.Errorf("removed proxy target %q still renders", title)
		}
	}

	assertTargets := func(want []string) {
		t.Helper()
		entry := s.settingsUnit.state.settingsAutosave.entries["settings:proxy-form"]
		if entry == nil {
			t.Fatal("proxy form did not register an autosave draft")
		}
		var draft settingsProxyAutosaveDraft
		if err := json.Unmarshal([]byte(entry.value), &draft); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(draft.Targets, want) {
			t.Fatalf("proxy draft targets = %v, want %v", draft.Targets, want)
		}
	}
	assertTargets([]string{"CODEX"})
	for step := 0; step < 20 && !v.Focused("Codex CLI"); step++ {
		v.Key(0, ui.KeyTab)
	}
	if !v.Focused("Codex CLI") {
		t.Fatal("Codex proxy switch is missing from the tab order")
	}
	v.Key(0, ui.KeySpace)
	assertTargets(nil)
	if err := v.Click("清除代理"); err != nil {
		t.Fatal(err)
	}
	assertTargets(nil)
}
