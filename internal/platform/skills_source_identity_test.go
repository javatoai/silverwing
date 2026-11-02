package platform

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func completionSkillSnapshot(source domain.SkillSource) SkillSourceSnapshot {
	fingerprint := skillSourceFingerprint(source)
	ref := skillSourceRef(source)
	if ref == "" {
		ref = "actual-default"
	}
	return SkillSourceSnapshot{SourceID: source.ID, SourceFingerprint: &fingerprint, ResolvedOrigin: &SkillInstalledOrigin{RepositoryURL: source.RepositoryURL, Ref: ref, Commit: strings.Repeat("a", 40)}, Skills: []ExternalSkill{}, DiscoveredRoots: []string{}}
}

func TestSkillSourceIdentityGuardsOldErrorAndChangedConfiguration(t *testing.T) {
	source := completionSkillSource()
	status := completionSkillSnapshot(source)
	if err := validateSkillSourceSnapshot(source, status, *status.SourceFingerprint); err != nil {
		t.Fatal(err)
	}
	renamed := source
	renamed.Name = "Renamed display only"
	if skillSourceFingerprint(source) != skillSourceFingerprint(renamed) {
		t.Fatal("display name changed identity")
	}
	for _, scenario := range []string{"legacy", "error", "url", "ref", "root", "expected", "missing-version"} {
		t.Run(scenario, func(t *testing.T) {
			configured, snapshot := source, status
			expected := *status.SourceFingerprint
			switch scenario {
			case "legacy":
				snapshot.SourceFingerprint = nil
			case "error":
				snapshot.Error = stringPtr("offline")
			case "url":
				configured.RepositoryURL = "https://example.invalid/other.git"
			case "ref":
				configured.Ref = stringPtr("actual-default")
			case "root":
				configured.SkillRoot = stringPtr("skills")
			case "expected":
				expected = "different-ui-snapshot"
			case "missing-version":
				snapshot.ResolvedOrigin = nil
			}
			if err := validateSkillSourceSnapshot(configured, snapshot, expected); err == nil {
				t.Fatal("unsafe source snapshot accepted", scenario)
			}
		})
	}
	for _, value := range []any{"", nil, 42} {
		if _, err := skillFingerprintExpectation(map[string]any{"sourceFingerprint": value}); err == nil {
			t.Fatal("provided invalid fingerprint ignored", value)
		}
	}
	if expected, err := skillFingerprintExpectation(nil); err != nil || len(expected) != 0 {
		t.Fatal("optional field became required", expected, err)
	}
}

func completionSkillService(t *testing.T, source domain.SkillSource) (*Service, string) {
	t.Helper()
	home := t.TempDir()
	if runtime.GOOS == "windows" {
		t.Setenv("USERPROFILE", home)
	} else {
		t.Setenv("HOME", home)
	}
	paths := storage.NewPaths(t.TempDir())
	store := storage.NewConfigStore(paths)
	cfg := domain.DefaultAppConfig()
	cfg.SkillSources = []domain.SkillSource{source}
	if err := store.Save(cfg); err != nil {
		t.Fatal(err)
	}
	return NewService(paths, store, nil), home
}

func completionSkillRows(t *testing.T, value any) []any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		t.Fatal(err)
	}
	return object["skills"].([]any)
}

func TestSkillSourceIdentityViewsPreserveStaleRowsAndRejectOperations(t *testing.T) {
	source := completionSkillSource()
	service, _ := completionSkillService(t, source)
	status := completionSkillSnapshot(source)
	status.Skills = []ExternalSkill{{Name: "example", Description: "old row", SourcePath: "skills/example"}}
	status.Error = stringPtr("synthetic refresh failed")
	state := emptyExtensionState()
	state.SkillSources[source.ID] = status
	view := service.skillSourceStatusView(status, state, source)
	if view["stale"] != true || view["needsRefresh"] != true || view["error"] != "synthetic refresh failed" || len(completionSkillRows(t, view)) != 1 {
		t.Fatal("failed refresh lost explicit stale/error state", view)
	}
	if _, err := service.installExternalSkill(context.Background(), source, "example", false, state, *status.SourceFingerprint); err == nil {
		t.Fatal("failed refresh installed old cache")
	}
	status.Error = nil
	state.SkillSources[source.ID] = status
	checkout, err := service.skillCheckoutPath(source)
	if err != nil {
		t.Fatal(err)
	}
	writeCompletionSkill(t, checkout, "skills/example", "example")
	if err := service.saveExtensionState(state); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Execute(context.Background(), "skills.preview", map[string]any{"sourceId": source.ID, "name": "example", "sourceFingerprint": *status.SourceFingerprint}); err != nil {
		t.Fatal("fresh current preview was blocked", err)
	}
	for _, action := range []string{"skills.preview", "skills.preview/path", "skills.files/copy-path", "skills.install", "skills.update"} {
		if _, err := service.Execute(context.Background(), action, map[string]any{"sourceId": source.ID, "name": "example", "sourceFingerprint": "stale-ui", "confirmed": true}); err == nil {
			t.Fatal("remote operation ignored expected identity", action)
		}
	}
	changed := source
	changed.SkillRoot = stringPtr("another-root")
	view = service.skillSourceStatusView(status, state, changed)
	if view["stale"] != true || view["needsRefresh"] != true || view["sourceFingerprint"] != *status.SourceFingerprint || view["error"] == nil {
		t.Fatal("configuration mismatch hid old identity or enabled old rows", view)
	}
}

func TestSkillSourceIdentityRefreshFailureCancellationAndRecovery(t *testing.T) {
	for _, scenario := range []string{"failure", "canceled", "success", "changed-config"} {
		t.Run(scenario, func(t *testing.T) {
			source := completionSkillSource()
			service, home := completionSkillService(t, source)
			cfg, err := service.loadConfig()
			if err != nil {
				t.Fatal(err)
			}
			state := emptyExtensionState()
			previous := completionSkillSnapshot(source)
			previous.Skills = []ExternalSkill{{Name: "example", Description: "old", SourcePath: "skills/example"}}
			state.SkillSources[source.ID] = previous
			state.ManagedSkills = []managedSkill{{SourceID: source.ID, Name: "example", InstalledOrigin: previous.ResolvedOrigin}}
			installed := writeCompletionSkill(t, filepath.Join(home, ".agents", "skills"), "example", "example")
			oldBody, err := os.ReadFile(filepath.Join(installed, skillFileName))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			checkout, err := service.skillCheckoutPath(source)
			if err != nil {
				t.Fatal(err)
			}
			origin := *previous.ResolvedOrigin
			origin.Commit = strings.Repeat("b", 40)
			load := func() (string, *SkillInstalledOrigin, error) {
				persisted, err := service.loadExtensionState()
				if err != nil || persisted.SkillSources[source.ID].Error == nil {
					t.Fatal("checkout mutated before invalidating old installable cache", err)
				}
				switch scenario {
				case "failure":
					return "", nil, errors.New("synthetic Git failure")
				case "canceled":
					cancel()
					return "", nil, context.Canceled
				case "changed-config":
					cfg.SkillSources[0].RepositoryURL = "https://example.invalid/changed.git"
					if err := service.config.Save(cfg); err != nil {
						t.Fatal(err)
					}
				}
				cached := writeCompletionSkill(t, checkout, "skills/category/example", "example")
				if err := os.WriteFile(filepath.Join(cached, skillFileName), append(append([]byte{}, oldBody...), []byte("New remote body\n")...), 0o600); err != nil {
					t.Fatal(err)
				}
				return checkout, &origin, nil
			}
			_, refreshErr := service.refreshSkillSourceWithCheckout(ctx, cfg, source, state, load)
			persisted, err := service.loadExtensionState()
			if err != nil {
				t.Fatal(err)
			}
			current := persisted.SkillSources[source.ID]
			if scenario == "success" {
				if refreshErr != nil || validateSkillSourceSnapshot(source, current) != nil || current.Skills[0].SourcePath != "skills/category/example" {
					t.Fatal("successful refresh did not replace stale snapshot", current, refreshErr)
				}
			} else if current.Error == nil || validateSkillSourceSnapshot(source, current) == nil {
				t.Fatal("failed/canceled/stale receipt left old cache installable", scenario, current, refreshErr)
			}
			if *persisted.ManagedSkills[0].InstalledOrigin != *previous.ResolvedOrigin {
				t.Fatal("refresh rewrote installed provenance")
			}
			body, err := os.ReadFile(filepath.Join(installed, skillFileName))
			if err != nil || string(body) != string(oldBody) {
				t.Fatal("source refresh updated an installed skill", err)
			}
		})
	}
}

func TestSkillInstalledOriginFlatInstallUpdateAndUnknownHistory(t *testing.T) {
	source := completionSkillSource()
	service, home := completionSkillService(t, source)
	checkout, err := service.skillCheckoutPath(source)
	if err != nil {
		t.Fatal(err)
	}
	path := writeCompletionSkill(t, checkout, "skills/example", "example")
	if err := os.MkdirAll(filepath.Join(path, "resources"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "resources", "asset.txt"), []byte("complete resource"), 0o600); err != nil {
		t.Fatal(err)
	}
	state := emptyExtensionState()
	status := completionSkillSnapshot(source)
	status.Skills = []ExternalSkill{{Name: "example", Description: "Example", SourcePath: "skills/example"}}
	state.SkillSources[source.ID] = status
	if _, err := service.installSkillFromSnapshot(context.Background(), source, "example", false, state, *status.ResolvedOrigin); err != nil {
		t.Fatal(err)
	}
	if !isRegular(filepath.Join(home, ".agents", "skills", "example", "resources", "asset.txt")) {
		t.Fatal("flat install omitted resources")
	}
	state, err = service.loadExtensionState()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.ManagedSkills) != 1 || *state.ManagedSkills[0].InstalledOrigin != *status.ResolvedOrigin {
		t.Fatal("observed installation origin not persisted", state.ManagedSkills)
	}
	// Moving a source skill into categories does not change source/name ownership.
	nested := filepath.Join(checkout, "skills", "group", "category", "example")
	if err := os.MkdirAll(filepath.Dir(nested), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, nested); err != nil {
		t.Fatal(err)
	}
	status.Skills[0].SourcePath = "skills/group/category/example"
	updatedOrigin := *status.ResolvedOrigin
	updatedOrigin.Ref, updatedOrigin.Commit = "new-actual-default", strings.Repeat("b", 40)
	status.ResolvedOrigin = &updatedOrigin
	state.SkillSources[source.ID] = status
	if _, err := service.installSkillFromSnapshot(context.Background(), source, "example", false, state, updatedOrigin); err != nil {
		t.Fatal("categorized update required takeover", err)
	}
	state, err = service.loadExtensionState()
	if err != nil || len(state.ManagedSkills) != 1 || *state.ManagedSkills[0].InstalledOrigin != updatedOrigin {
		t.Fatal("update lost managed identity/origin", state.ManagedSkills, err)
	}
	catalog, err := service.localSkillsCatalog(state)
	if err != nil {
		t.Fatal(err)
	}
	local := completionSkillRows(t, catalog)[0].(map[string]any)["installedOrigin"].(map[string]any)
	row := completionSkillRows(t, service.skillSourceStatusView(status, state, source))[0].(map[string]any)["installedOrigin"].(map[string]any)
	if local["ref"] != updatedOrigin.Ref || row["commit"] != updatedOrigin.Commit {
		t.Fatal("catalog/source row did not expose recorded origin", local, row)
	}
	changed := source
	changed.RepositoryURL = "https://example.invalid/current-setting-is-not-history.git"
	row = completionSkillRows(t, service.skillSourceStatusView(status, state, changed))[0].(map[string]any)["installedOrigin"].(map[string]any)
	if row["repositoryUrl"] != source.RepositoryURL {
		t.Fatal("current source configuration fabricated installation history", row)
	}
	writeCompletionSkill(t, filepath.Join(home, ".agents", "skills"), "legacy", "legacy")
	writeCompletionSkill(t, filepath.Join(home, ".agents", "skills"), "external", "external")
	state.ManagedSkills = append(state.ManagedSkills, managedSkill{SourceID: source.ID, Name: "legacy"})
	catalog, err = service.localSkillsCatalog(state)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range completionSkillRows(t, catalog) {
		item := value.(map[string]any)
		if item["directoryName"] != "example" {
			if _, guessed := item["installedOrigin"]; guessed {
				t.Fatal("unknown historical/external origin was guessed", item)
			}
		}
	}
}

func TestSkillInstalledOriginRepositoryRootExcludesGitMetadata(t *testing.T) {
	source := completionSkillSource()
	source.SkillRoot = stringPtr(".")
	service, home := completionSkillService(t, source)
	checkout, err := service.skillCheckoutPath(source)
	if err != nil {
		t.Fatal(err)
	}
	writeCompletionSkill(t, checkout, ".", "repository-skill")
	writeCompletionSkill(t, checkout, "examples/nested", "resource-example")
	for relative, content := range map[string]string{
		".git/config":            "synthetic Git metadata",
		".git/objects/synthetic": "synthetic Git object",
		"scripts/helper.txt":     "complete skill resource",
	} {
		path := filepath.Join(checkout, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	skills, _, err := discoverSkillsWithContext(context.Background(), source, checkout)
	if err != nil || len(skills) != 1 || skills[0].SourcePath != "." {
		t.Fatal("repository root did not define one skill boundary", skills, err)
	}
	status := completionSkillSnapshot(source)
	status.Skills = skills
	state := emptyExtensionState()
	state.SkillSources[source.ID] = status
	if _, err := service.installSkillFromSnapshot(context.Background(), source, "repository-skill", false, state, *status.ResolvedOrigin); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(home, ".agents", "skills", "repository-skill")
	for _, relative := range []string{skillFileName, "scripts/helper.txt", "examples/nested/SKILL.md"} {
		if !isRegular(filepath.Join(destination, filepath.FromSlash(relative))) {
			t.Fatal("root skill resource was not copied flat", relative)
		}
	}
	if exists(filepath.Join(destination, ".git")) {
		t.Fatal("checkout Git metadata was installed as a skill resource")
	}
	if !isRegular(filepath.Join(checkout, ".git", "config")) {
		t.Fatal("install altered the source checkout")
	}
}
