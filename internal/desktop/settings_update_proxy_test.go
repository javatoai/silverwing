package desktop

import (
	"encoding/json"
	"testing"
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
	config := map[string]any{"commandProxyUrl": "http://127.0.0.1:7897", "commandProxyTargets": []any{"GIT"}}
	_, inputs := settingsConfigInputValues(config)
	if inputs["proxy-target:APP_UPDATES"] != false {
		t.Fatal("update proxy default enabled")
	}
	draft := settingsProxyDraft{scheme: "http", host: "127.0.0.1", port: "7897", targets: []string{"GIT", "APP_UPDATES"}}
	if err := updateSettingsProxyConfig(config, draft); err != nil {
		t.Fatal(err)
	}
	inputs = readInputs(config)
	if inputs["proxy-target:APP_UPDATES"] != true || inputs["proxy-target:GIT"] != true {
		t.Fatal("enabled update proxy or existing Git target was lost")
	}
	draft.targets = []string{"GIT"}
	if err := updateSettingsProxyConfig(config, draft); err != nil {
		t.Fatal(err)
	}
	inputs = readInputs(config)
	if inputs["proxy-target:APP_UPDATES"] != false || inputs["proxy-target:GIT"] != true {
		t.Fatal("disabling update proxy changed another target")
	}
}
