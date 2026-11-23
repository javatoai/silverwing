package storage

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"

	"silverwing-go/internal/domain"
)

func TestRetiredCloudSettingsLoadImportAndBackup(t *testing.T) {
	for _, tc := range []struct {
		name    string
		legacy  string
		targets []domain.CommandProxyTarget
	}{
		{
			name:    "active selections",
			legacy:  `["GIT","APP_UPDATES","WORKSPACE_COMMAND","MEEGLE","CODEX","GENBU","LARK","APP_UPDATES"]`,
			targets: []domain.CommandProxyTarget{domain.ProxyTargetAppUpdates, domain.ProxyTargetCodex},
		},
		{
			name:    "retired selections only",
			legacy:  `["GIT","WORKSPACE_COMMAND","MEEGLE","GENBU","LARK"]`,
			targets: []domain.CommandProxyTarget{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertTargets := func(config domain.AppConfig, err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
				if !slices.Equal(config.CommandProxyTargets, tc.targets) {
					t.Fatalf("proxy targets = %v, want %v", config.CommandProxyTargets, tc.targets)
				}
			}
			assertWritten := func(store *ConfigStore) {
				t.Helper()
				fields := readConfigObject(t, store, "integrations.json")
				if _, present := fields["requirementMaterialsFeishuTarget"]; present {
					t.Fatal("retired Feishu settings were written")
				}
				want, _ := json.Marshal(tc.targets)
				assertConfigJSON(t, fields["commandProxyTargets"], string(want))
			}
			store := NewConfigStore(NewPaths(t.TempDir()))
			if _, err := store.Load(); err != nil {
				t.Fatal(err)
			}
			editConfigObject(t, store, "integrations.json", func(fields map[string]json.RawMessage) {
				fields["larkExecutablePath"] = json.RawMessage(`"C:/retired/lark.cmd"`)
				fields["requirementMaterialsFeishuTarget"] = json.RawMessage(`"retired-invalid-value"`)
				fields["commandProxyTargets"] = json.RawMessage(tc.legacy)
			})
			config, err := store.Load()
			assertTargets(config, err)
			fields := readConfigObject(t, store, "integrations.json")
			assertConfigJSON(t, fields["commandProxyTargets"], tc.legacy)
			archive := filepath.Join(t.TempDir(), "legacy.zip")
			if _, err = store.ExportTo(archive); err != nil {
				t.Fatal(err)
			}
			target := NewConfigStore(NewPaths(t.TempDir()))
			assertTargets(target.ImportFrom(archive))
			assertWritten(target)

			// Restore the original legacy settings after saving a different selection.
			config.CommandProxyTargets = []domain.CommandProxyTarget{domain.ProxyTargetAppUpdates}
			if err = store.Save(config); err != nil {
				t.Fatal(err)
			}
			assertTargets(store.ImportFrom(archive))
			assertWritten(store)
		})
	}
}
