package desktop

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
)

func renderConfigurationFixture(tb testing.TB) *app {
	tb.Helper()
	s := taskNavigationFixture()
	config := domain.DefaultAppConfig()
	config.Groups[0].ID = "backend"
	for i := 0; i < 100; i++ {
		config.Groups[0].Services = append(config.Groups[0].Services, domain.GroupServiceConfig{
			ID: fmt.Sprintf("service-%d", i), Enabled: i%2 == 0,
			DevelopmentTool: domain.DevelopmentToolIntelliJIdea,
			Bootstrap:       domain.BootstrapConfig{CopyRules: []domain.BootstrapCopyRule{}, Commands: []domain.BootstrapCommand{}},
			Modules:         []domain.ServiceModuleConfig{},
		})
		config.MeegleProjects = append(config.MeegleProjects, domain.MeegleProjectConfig{
			ProjectKey: fmt.Sprintf("key-%d", i), SimpleName: fmt.Sprintf("space-%d", i),
		})
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		tb.Fatal(err)
	}
	var checked domain.AppConfig
	if err := json.Unmarshal(encoded, &checked); err != nil {
		tb.Fatal(err)
	}
	s.settingsUnit.state.configJSON, s.snapshot.ConfigJSON = string(encoded), string(encoded)
	s.taskUnit.state.tasks[0].requirementURL = "https://project.feishu.cn/space-0/userstory/detail/123"
	return s
}

func TestRenderConfigurationReadsAvoidRepeatedDecodeAndCopies(t *testing.T) {
	for _, page := range []string{"services", "materials-git", "requirements", "navigation"} {
		t.Run(page, func(t *testing.T) {
			s := renderConfigurationFixture(t)
			read := func() {
				switch page {
				case "services":
					_ = s.serviceEnabled("backend", "service-99")
				case "materials-git":
					_, _ = s.materialsGitConfig()
				case "requirements":
					_ = s.currentRequirementListIdentity()
				case "navigation":
					_ = s.showsTagNavigation()
				}
			}
			read()
			if allocations := testing.AllocsPerRun(20, read); allocations > 64 {
				t.Fatalf("%s rendering allocated %.0f objects for an unchanged configuration", page, allocations)
			}
		})
	}
}

func TestRenderConfigurationReadsTrackSourceAndLiveIdentity(t *testing.T) {
	s := renderConfigurationFixture(t)
	initial := s.currentRequirementListIdentity()
	if !s.serviceEnabled("backend", "service-0") || s.serviceEnabled("backend", "service-1") {
		t.Fatal("initial read projections are incorrect")
	}
	s.settingsUnit.state.configJSON = strings.ReplaceAll(s.settingsUnit.state.configJSON, "space-0", "changed-space")
	s.settingsUnit.state.configJSON = strings.ReplaceAll(s.settingsUnit.state.configJSON, "/Target1", "/Target2")
	s.settingsUnit.state.configJSON = strings.Replace(s.settingsUnit.state.configJSON, `"enabled":true`, `"enabled":false`, 1)
	if s.serviceEnabled("backend", "service-0") || s.currentRequirementListIdentity().projects == initial.projects {
		t.Fatal("render projections retained an earlier configuration")
	}
	// A mutable account status and application home must remain live even when
	// no configuration bytes change. Async request scopes depend on these.
	s.settingsUnit.state.settingsCLIStatus = map[string]map[string]any{"meegle": {"authenticated": true, "userKey": "first"}}
	identity := s.currentRequirementListIdentity()
	s.settingsUnit.state.settingsCLIStatus["meegle"]["userKey"] = "second"
	s.snapshot.Home = "changed-home"
	changed := s.currentRequirementListIdentity()
	if changed.account == identity.account || changed.home != "changed-home" || changed.projects != identity.projects {
		t.Fatal("configuration caching retained live application or account state")
	}
	valid := s.settingsUnit.state.configJSON
	for _, source := range []string{"", "invalid", "null"} {
		s.settingsUnit.state.configJSON = source
		if s.showsTagNavigation() || s.currentRequirementListIdentity().projects != "[]" {
			t.Fatalf("invalid configuration exposed earlier values: %q", source)
		}
	}
	s.settingsUnit.state.configJSON = valid
	if !s.showsTagNavigation() {
		t.Fatal("configuration did not recover")
	}
}

func BenchmarkDesktopPageConfigurationReads(b *testing.B) {
	for _, page := range []string{"services", "requirements", "navigation"} {
		b.Run(page, func(b *testing.B) {
			s := renderConfigurationFixture(b)
			b.ReportAllocs()
			b.ReportMetric(float64(len(s.settingsUnit.state.configJSON)), "config-bytes")
			for b.Loop() {
				switch page {
				case "services":
					_ = s.serviceEnabled("backend", "service-99")
				case "requirements":
					_ = s.currentRequirementListIdentity()
				case "navigation":
					_ = s.showsTagNavigation()
				}
			}
		})
	}
}
