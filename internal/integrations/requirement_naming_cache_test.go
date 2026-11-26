package integrations

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

const testRequirementNamingLink = "https://project.feishu.cn/space/bug/detail/123456"

func requirementNamingTestConfig() domain.AppConfig {
	config := domain.DefaultAppConfig()
	config.AIRequirementNamingEnabled = true
	config.MeegleProjects = []domain.MeegleProjectConfig{{ProjectKey: "project-a", SimpleName: "space"}}
	return config
}

func confirmRequirementNamingTestIdentity(service *Service, config domain.AppConfig, identity string) {
	service.identityStateMu.Lock()
	service.identityConfirmed = true
	service.identityState = requirementIdentityAuthenticated
	service.confirmedIdentity = identity
	service.identityLocalContext = service.requirementIdentityLocalContext(config)
	service.identityStateMu.Unlock()
}

func TestCachedRequirementAINamingRequiresConfirmedAccountScope(t *testing.T) {
	service := NewService(storage.NewPaths(t.TempDir()), nil, nil)
	config := requirementNamingTestConfig()
	account := opaqueRequirementIdentity("account-a")
	identity := "project-a:bug:123456"
	want := domain.RequirementAiNamingSuggestion{FolderName: "命名示例", BranchSuffix: "improve_cache"}
	service.writeRequirementAINamingCache(identity, config.AIRequirementNamingModel, want)

	if got, found, err := service.ReadCachedRequirementAINaming(context.Background(), config, testRequirementNamingLink, account); err != nil || found || got != (domain.RequirementAiNamingSuggestion{}) {
		t.Fatalf("unconfirmed identity returned cached naming: %#v %v %v", got, found, err)
	}

	confirmRequirementNamingTestIdentity(service, config, account)
	got, found, err := service.ReadCachedRequirementAINaming(context.Background(), config, testRequirementNamingLink, account)
	if err != nil || !found || got != want {
		t.Fatalf("confirmed account cache lookup failed: %#v %v %v", got, found, err)
	}

	otherAccount := opaqueRequirementIdentity("account-b")
	if got, found, err := service.ReadCachedRequirementAINaming(context.Background(), config, testRequirementNamingLink, otherAccount); err == nil || found || got != (domain.RequirementAiNamingSuggestion{}) {
		t.Fatalf("cache crossed confirmed account scope: %#v %v %v", got, found, err)
	}

	service.identityStateMu.Lock()
	service.identityLocalContext = "stale-profile-context"
	service.identityStateMu.Unlock()
	if got, found, err := service.ReadCachedRequirementAINaming(context.Background(), config, testRequirementNamingLink, account); err != nil || found || got != (domain.RequirementAiNamingSuggestion{}) {
		t.Fatalf("cache was served after the confirmed identity context changed: %#v %v %v", got, found, err)
	}
}

func TestRequirementNamingCacheRetainsV1IdentityOnlyEntries(t *testing.T) {
	service := NewService(storage.NewPaths(t.TempDir()), nil, nil)
	config := requirementNamingTestConfig()
	account := opaqueRequirementIdentity("account-a")
	want := domain.RequirementAiNamingSuggestion{FolderName: "命名示例", BranchSuffix: "improve_cache"}
	confirmRequirementNamingTestIdentity(service, config, account)

	legacy := `{"version":1,"entries":{"project-a:bug:123456":{"folderName":"命名示例","branchSuffix":"improve  cache","model":"gpt-6-luna","rulesVersion":2}}}`
	if err := os.MkdirAll(service.paths.Cache, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(service.paths.Cache, "requirement-ai-naming.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	got, found, err := service.ReadCachedRequirementAINaming(context.Background(), config, testRequirementNamingLink, account)
	if err != nil || !found || got != want {
		t.Fatalf("legacy v1 naming entry was not reused for the confirmed account: %#v %v %v", got, found, err)
	}
}

func TestCachedRequirementAINamingInvalidatesOnLocalProfileChange(t *testing.T) {
	profileHome := t.TempDir()
	t.Setenv("USERPROFILE", profileHome)
	t.Setenv("HOME", profileHome)
	profileDirectory := filepath.Join(profileHome, ".meegle")
	if err := os.MkdirAll(profileDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	profilePath := filepath.Join(profileDirectory, "config.json")
	if err := os.WriteFile(profilePath, []byte(`{"profile":"first"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewService(storage.NewPaths(t.TempDir()), nil, nil)
	config := requirementNamingTestConfig()
	account := opaqueRequirementIdentity("account-a")
	identity := "project-a:bug:123456"
	want := domain.RequirementAiNamingSuggestion{FolderName: "命名示例", BranchSuffix: "improve_cache"}
	confirmRequirementNamingTestIdentity(service, config, account)
	service.writeRequirementAINamingCache(identity, config.AIRequirementNamingModel, want)
	if _, found, err := service.ReadCachedRequirementAINaming(context.Background(), config, testRequirementNamingLink, account); err != nil || !found {
		t.Fatalf("valid profile did not authorize its local cache: found=%v err=%v", found, err)
	}
	if err := os.WriteFile(profilePath, []byte(`{"profile":"a different profile with a changed size"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, found, err := service.ReadCachedRequirementAINaming(context.Background(), config, testRequirementNamingLink, account); err != nil || found {
		t.Fatalf("changed Meegle profile still authorized a local cache: found=%v err=%v", found, err)
	}
}

func TestRequirementNamingCacheReusedAfterReconfirmingAccount(t *testing.T) {
	f := newRequirementReadFixture(t)
	const identity = "project-a:userstory:123"
	want := domain.RequirementAiNamingSuggestion{FolderName: "命名示例", BranchSuffix: "improve_cache"}
	f.service.writeRequirementAINamingCache(identity, f.config.AIRequirementNamingModel, want)
	if err := os.WriteFile(filepath.Join(f.root, "mode"), []byte("fail-workitem"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A fresh backend shares the on-disk cache but has no confirmed identity yet.
	service := NewService(f.service.paths, f.service.config, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, found, err := service.ReadCachedRequirementAINaming(ctx, f.config, f.item.URL, f.token); err != nil || found {
		t.Fatalf("unconfirmed backend read the cache: found=%v err=%v", found, err)
	}
	before := len(f.calls(t))
	result, err := service.requirementAINaming(ctx, f.config, f.payload())
	if err != nil || result != want {
		t.Fatalf("reconfirmed account did not reuse cached naming: %v %v", result, err)
	}
	calls := f.calls(t)[before:]
	prefixes := []string{"config profile ", "auth status ", "user me "}
	if len(calls) != len(prefixes) {
		t.Fatalf("expected a single identity probe, got %q", calls)
	}
	for i, prefix := range prefixes {
		if !strings.HasPrefix(calls[i], prefix) {
			t.Fatalf("unexpected command on naming cache hit: %q", calls[i])
		}
	}
}

func TestRequirementNamingPrewarmPersistsGeneratedNameForForeground(t *testing.T) {
	f := newRequirementReadFixture(t)
	f.item.Title = "武汉通退卡需求"
	f.config.CodexExecutablePath = f.config.MeegleExecutablePath
	if err := f.service.config.Save(f.config); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.writeRequirementDocument(requirementCacheKey("body", f.raw, f.item), f.item, "fixture body", 1234); err != nil {
		t.Fatal(err)
	}
	runLog := filepath.Join(f.root, "naming-calls.jsonl")
	t.Setenv("SILVERWING_NAMING_FIXTURE_CHILD", "1")
	t.Setenv("SILVERWING_NAMING_FIXTURE_CAPTURE", filepath.Join(f.root, "naming-capture.json"))
	t.Setenv("SILVERWING_NAMING_FIXTURE_RUN_LOG", runLog)
	t.Setenv("SILVERWING_NAMING_FIXTURE_RESULT", `{"folderName":"武汉通退卡场景对账","branchSuffix":"Wuhan-MOC  Reconcile UAT"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	payload := f.payload()
	payload["background"] = true
	want := domain.RequirementAiNamingSuggestion{FolderName: "武汉通退卡场景对账", BranchSuffix: "Wuhan-MOC_Reconcile_UAT"}
	result, err := f.service.requirementAINaming(ctx, f.config, payload)
	if err != nil || result != want {
		t.Fatalf("prewarm did not preserve the generated suggestion: %v %v", result, err)
	}
	before := len(f.calls(t))
	got, found, err := f.service.ReadCachedRequirementAINaming(ctx, f.config, f.item.URL, f.token)
	if err != nil || !found || got != want {
		t.Fatalf("prewarmed naming was not read from cache: %#v %v %v", got, found, err)
	}
	result, err = f.service.requirementAINaming(ctx, f.config, f.payload())
	if err != nil || result != want || len(f.calls(t)) != before {
		t.Fatalf("foreground cache hit repeated a Meegle call: %v %v", result, err)
	}
	log, err := os.ReadFile(runLog)
	if err != nil || strings.Count(string(log), "\n") != 1 {
		t.Fatalf("expected one Codex call in prewarm and foreground combined: %v", err)
	}
}

func TestRequirementNamingCacheWinsOverInflightForceRefresh(t *testing.T) {
	account := opaqueRequirementIdentity("account-a")
	key := requirementNamingKey{identity: "project-a:bug:123456", model: "model", context: "scope"}
	request := &requirementNamingRequest{force: true, forbidden: "[]", done: make(chan struct{})}
	coordinator := &requirementNamingCoordinator{requests: map[requirementNamingKey]*requirementNamingRequest{key: request},
		slots: make(chan struct{}, 2), background: make(chan struct{}, 1)}
	want := domain.RequirementAiNamingSuggestion{FolderName: "命名示例", BranchSuffix: "improve-cache"}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	got, err := coordinator.suggest(ctx, key, false, nil, false,
		func() *domain.RequirementAiNamingSuggestion { return &want },
		func(context.Context) (domain.RequirementAiNamingSuggestion, error) {
			t.Fatal("cache hit started a second AI generation")
			return domain.RequirementAiNamingSuggestion{}, nil
		})
	if err != nil || got != want {
		t.Fatalf("in-flight force refresh blocked a valid cache hit for %s: %#v %v", account, got, err)
	}
}
