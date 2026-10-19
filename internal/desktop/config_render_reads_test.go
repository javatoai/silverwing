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
	target := "https://example.feishu.cn/wiki/Target1"
	config.RequirementMaterialsFeishuTarget = &target
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
	s.configJSON, s.snapshot.ConfigJSON = string(encoded), string(encoded)
	s.tasks[0].requirementURL = "https://project.feishu.cn/space-0/userstory/detail/123"
	return s
}

func TestRenderConfigurationReadsAvoidRepeatedDecodeAndCopies(t *testing.T) {
	for _, page := range []string{"services", "materials", "requirements", "navigation"} {
		t.Run(page, func(t *testing.T) {
			s := renderConfigurationFixture(t)
			read := func() {
				switch page {
				case "services":
					_ = s.serviceEnabled("backend", "service-99")
				case "materials":
					_, _ = s.materialsFeishuSyncTarget()
					_ = s.materialsFeishuCommentAvailable(s.tasks[0])
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
	if !s.serviceEnabled("backend", "service-0") || s.serviceEnabled("backend", "service-1") || !s.materialsFeishuCommentAvailable(s.tasks[0]) {
		t.Fatal("initial read projections are incorrect")
	}
	if target, err := s.materialsFeishuSyncTarget(); err != nil || !strings.HasSuffix(target, "/Target1") {
		t.Fatalf("initial sync target: %q, %v", target, err)
	}
	s.configJSON = strings.ReplaceAll(s.configJSON, "space-0", "changed-space")
	s.configJSON = strings.ReplaceAll(s.configJSON, "/Target1", "/Target2")
	s.configJSON = strings.Replace(s.configJSON, `"enabled":true`, `"enabled":false`, 1)
	if s.serviceEnabled("backend", "service-0") || s.materialsFeishuCommentAvailable(s.tasks[0]) || s.currentRequirementListIdentity().projects == initial.projects {
		t.Fatal("render projections retained an earlier configuration")
	}
	if target, err := s.materialsFeishuSyncTarget(); err != nil || !strings.HasSuffix(target, "/Target2") {
		t.Fatalf("changed sync target: %q, %v", target, err)
	}
	// A mutable account status and application home must remain live even when
	// no configuration bytes change. Async request scopes depend on these.
	s.settingsCLIStatus = map[string]map[string]any{"meegle": {"authenticated": true, "userKey": "first"}}
	identity := s.currentRequirementListIdentity()
	s.settingsCLIStatus["meegle"]["userKey"] = "second"
	s.snapshot.Home = "changed-home"
	changed := s.currentRequirementListIdentity()
	if changed.account == identity.account || changed.home != "changed-home" || changed.projects != identity.projects {
		t.Fatal("configuration caching retained live application or account state")
	}
	valid := s.configJSON
	for _, source := range []string{"", "invalid", "null"} {
		s.configJSON = source
		if s.showsTagNavigation() || s.materialsFeishuCommentAvailable(s.tasks[0]) || s.currentRequirementListIdentity().projects != "[]" {
			t.Fatalf("invalid configuration exposed earlier values: %q", source)
		}
		if target, err := s.materialsFeishuSyncTarget(); err == nil || target != "" {
			t.Fatalf("invalid configuration enabled synchronization: %q", source)
		}
	}
	s.configJSON = valid
	if target, err := s.materialsFeishuSyncTarget(); err != nil || !strings.HasSuffix(target, "/Target2") || !s.showsTagNavigation() {
		t.Fatal("render configuration reads did not recover after a corrected source")
	}
}

func BenchmarkDesktopPageConfigurationReads(b *testing.B) {
	for _, page := range []string{"services", "materials", "requirements", "navigation"} {
		b.Run(page, func(b *testing.B) {
			s := renderConfigurationFixture(b)
			b.ReportAllocs()
			b.ReportMetric(float64(len(s.configJSON)), "config-bytes")
			for b.Loop() {
				switch page {
				case "services":
					_ = s.serviceEnabled("backend", "service-99")
				case "materials":
					_, _ = s.materialsFeishuSyncTarget()
					_ = s.materialsFeishuCommentAvailable(s.tasks[0])
				case "requirements":
					_ = s.currentRequirementListIdentity()
				case "navigation":
					_ = s.showsTagNavigation()
				}
			}
		})
	}
}
