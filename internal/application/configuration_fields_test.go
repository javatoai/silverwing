package application

import (
	"context"
	"encoding/json"
	"testing"

	"silverwing-go/internal/domain"
)

func TestPlatformExecutableSettingsRejectFieldUpdates(t *testing.T) {
	config := domain.DefaultAppConfig()
	for _, test := range []struct {
		key, value string
	}{
		{"genbuExecutablePath", `"legacy-genbu"`},
		{"genbuExecutableAutoDetected", `true`},
		{"dragonExecutablePath", `"legacy-dragon"`},
		{"dragonExecutableAutoDetected", `true`},
	} {
		t.Run(test.key, func(t *testing.T) {
			_, err := mergeConfigurationFields(config, ConfigFieldsUpdate{Fields: map[string]json.RawMessage{test.key: json.RawMessage(test.value)}})
			if err == nil {
				t.Fatal("retired executable settings accepted a field update")
			}
		})
	}
}

func TestWholeConfigSaveLeavesLegacyExecutableSettingsInert(t *testing.T) {
	a, err := newApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	legacy := "legacy-dragon"
	current, err := a.Config.Update(func(cfg domain.AppConfig) (domain.AppConfig, error) {
		cfg.DragonExecutablePath = &legacy
		cfg.DragonExecutableAutoDetected = true
		return cfg, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	updated := "new-genbu"
	current.GenbuExecutablePath, current.GenbuExecutableAutoDetected = &updated, true
	current.DragonExecutablePath, current.DragonExecutableAutoDetected = nil, false
	current.TagEnabled = !current.TagEnabled
	data, err := json.Marshal(current)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Dispatch(context.Background(), Action{Kind: "config.save", Text: string(data)}); err != nil {
		t.Fatal(err)
	}
	saved, err := a.Config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if saved.GenbuExecutablePath != nil || saved.GenbuExecutableAutoDetected ||
		saved.DragonExecutablePath == nil || *saved.DragonExecutablePath != legacy || !saved.DragonExecutableAutoDetected ||
		saved.TagEnabled != current.TagEnabled {
		t.Fatal("whole configuration save changed retired settings or lost the active update")
	}
}

func TestOptionalCLISwitchUpdatesOneToolAndRetainsItsPath(t *testing.T) {
	cfg := domain.DefaultAppConfig()
	enabled, larkPath := true, "lark-cli-fixture"
	cfg.OptionalCLIs.Dragon, cfg.LarkExecutablePath = &enabled, &larkPath
	next, err := mergeConfigurationFields(cfg, ConfigFieldsUpdate{ObjectFields: map[string]map[string]json.RawMessage{
		"optionalCLIs": {"lark": json.RawMessage(`false`)},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if next.OptionalCLIEnabled("lark") || next.OptionalCLIPath("lark") != larkPath || !next.OptionalCLIEnabled("dragon") || next.OptionalCLIs.Genbu != nil {
		t.Fatalf("one-tool switch changed another setting: %#v", next.OptionalCLIs)
	}
}
