package domain

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestCommandAndNamingDefaultsRespectExplicitSettings(t *testing.T) {
	assertDefault := func(c AppConfig) {
		t.Helper()
		if c.CommandProxyURL == nil || *c.CommandProxyURL != "http://127.0.0.1:7897" || !reflect.DeepEqual(c.CommandProxyTargets, []CommandProxyTarget{ProxyTargetCodex}) || c.CommandProxyUsername != nil || c.CommandProxyPassword != nil || !c.AIRequirementNamingEnabled || !c.AIRequirementNamingPrewarmEnabled || c.AIRequirementNamingModel != "gpt-6-luna" {
			t.Fatalf("wrong defaults %+v", c)
		}
	}
	assertDefault(DefaultAppConfig())
	var missing AppConfig
	if err := json.Unmarshal([]byte(`{}`), &missing); err != nil {
		t.Fatal(err)
	}
	assertDefault(missing)
	for _, raw := range []string{`{"commandProxyUrl":null,"commandProxyTargets":[],"aiRequirementNamingEnabled":false,"aiRequirementNamingPrewarmEnabled":false,"aiRequirementNamingModel":"gpt-5.6-luna"}`, `{"commandProxyUrl":"","aiRequirementNamingEnabled":false,"aiRequirementNamingPrewarmEnabled":false,"aiRequirementNamingModel":"gpt-5.6-luna"}`, `{"commandProxyUrl":null,"aiRequirementNamingEnabled":false,"aiRequirementNamingPrewarmEnabled":false,"aiRequirementNamingModel":"gpt-5.6-luna"}`} {
		var c AppConfig
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			t.Fatal(err)
		}
		if len(c.CommandProxyTargets) != 0 || c.AIRequirementNamingEnabled || c.AIRequirementNamingPrewarmEnabled || c.AIRequirementNamingModel != "gpt-5.6-luna" {
			t.Fatalf("explicit off/model overwritten %+v", c)
		}
		if err := c.Validate(); err != nil {
			t.Fatal(err)
		}
	}
	var custom AppConfig
	if err := json.Unmarshal([]byte(`{"commandProxyUrl":"socks5://127.0.0.1:1080","commandProxyTargets":["GIT"],"aiRequirementNamingModel":"my-model"}`), &custom); err != nil {
		t.Fatal(err)
	}
	if *custom.CommandProxyURL != "socks5://127.0.0.1:1080" || !reflect.DeepEqual(custom.CommandProxyTargets, []CommandProxyTarget{ProxyTargetGit}) || custom.AIRequirementNamingModel != "my-model" {
		t.Fatal("custom preference overwritten")
	}
	var onlyTargets AppConfig
	if err := json.Unmarshal([]byte(`{"commandProxyTargets":[]}`), &onlyTargets); err != nil {
		t.Fatal(err)
	}
	if len(onlyTargets.CommandProxyTargets) != 0 {
		t.Fatal("explicit empty targets replaced")
	}
	for _, raw := range []string{`{"commandProxyTargets":null}`, `{"commandProxyTargets":["OTHER"]}`, `{"aiRequirementNamingEnabled":"true"}`, `{"aiRequirementNamingPrewarmEnabled":null}`, `{"commandProxyUrl":7897}`, `{"unknown":true}`} {
		var c AppConfig
		if json.Unmarshal([]byte(raw), &c) == nil {
			t.Fatalf("invalid JSON accepted: %s", raw)
		}
	}
}
