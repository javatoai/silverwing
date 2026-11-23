package desktop

import (
	"encoding/json"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSettingsUpdateProxyUsesSharedTargetsAndDefaultsOff(t *testing.T) {
	readInputs := func(config map[string]any) map[string]any {
		t.Helper()
		// Applied settings arrive through JSON, after the existing save action.
		encoded, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		var applied map[string]any
		if err := json.Unmarshal(encoded, &applied); err != nil {
			t.Fatal(err)
		}
		_, inputs := settingsConfigInputValues(applied)
		return inputs
	}
	config := map[string]any{"commandProxyUrl": "http://127.0.0.1:7897", "commandProxyTargets": []any{"CODEX", "GIT", "WORKSPACE_COMMAND", "MEEGLE", "GENBU"}}
	_, inputs := settingsConfigInputValues(config)
	if inputs["proxy-target:APP_UPDATES"] != false {
		t.Fatal("update proxy default enabled")
	}
	for _, removed := range []string{"GIT", "WORKSPACE_COMMAND", "MEEGLE", "GENBU"} {
		if _, present := inputs["proxy-target:"+removed]; present {
			t.Errorf("removed target %s still supplies settings input", removed)
		}
	}
	draft := settingsProxyDraft{scheme: "http", host: "127.0.0.1", port: "7897", targets: []string{"CODEX", "APP_UPDATES"}}
	if err := updateSettingsProxyConfig(config, draft); err != nil {
		t.Fatal(err)
	}
	inputs = readInputs(config)
	if inputs["proxy-target:APP_UPDATES"] != true || inputs["proxy-target:CODEX"] != true {
		t.Fatal("enabled update proxy or existing Codex target was lost")
	}
	draft.targets = []string{"CODEX"}
	if err := updateSettingsProxyConfig(config, draft); err != nil {
		t.Fatal(err)
	}
	inputs = readInputs(config)
	if inputs["proxy-target:APP_UPDATES"] != false || inputs["proxy-target:CODEX"] != true {
		t.Fatal("disabling update proxy changed another target")
	}
}

func TestSettingsProxyOnlyRendersSupportedTargets(t *testing.T) {
	s := newApp(nil, Options{CapturePath: "proxy-targets-test"})
	s.snapshot.Home = t.TempDir()
	s.settingsUnit.state.configJSON = `{"commandProxyUrl":"http://127.0.0.1:7897","commandProxyTargets":["CODEX","GIT","WORKSPACE_COMMAND","MEEGLE","GENBU"]}`
	t.Cleanup(s.settingsUnit.stopSettingsAutosave)
	v := ui.NewTester(func(c *ui.Context) { s.settingsProxy(c, setTheme(c, false)) }, 960, 680)
	for _, title := range []string{"Git", "服务快捷命令", "Meegle CLI", "Genbu CLI"} {
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
	for _, title := range []string{"Codex CLI", "应用更新"} {
		for step := 0; step < 20 && !v.Focused(title); step++ {
			v.Key(0, ui.KeyTab)
		}
		if !v.Focused(title) {
			t.Fatalf("proxy switch %q is missing from the tab order", title)
		}
		v.Key(0, ui.KeySpace)
	}
	assertTargets([]string{"APP_UPDATES"})
	if err := v.Click("清除代理"); err != nil {
		t.Fatal(err)
	}
	assertTargets(nil)
}
