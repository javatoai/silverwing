package integrations

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func assertNamingIsolation(t *testing.T, args []string) {
	t.Helper()
	for _, setting := range []string{"features.plugins=false", "features.apps=false", "features.hooks=false", "features.shell_snapshot=false", "analytics.enabled=false", `otel.exporter="none"`, `otel.trace_exporter="none"`, `otel.metrics_exporter="none"`, `mcp_servers={"fixture.mcp-name"={enabled=false},"node_repl"={enabled=false}}`} {
		if !slices.Contains(args, setting) {
			t.Fatalf("isolation setting missing: %s in %q", setting, args)
		}
	}
}

func TestNamingIsolationSurvivesReasoningFallback(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	paths := storage.NewPaths(t.TempDir())
	capture := filepath.Join(paths.Home, "capture.json")
	log := filepath.Join(paths.Home, "calls.jsonl")
	t.Setenv("SILVERWING_NAMING_FIXTURE_CHILD", "1")
	t.Setenv("SILVERWING_NAMING_FIXTURE_CAPTURE", capture)
	t.Setenv("SILVERWING_NAMING_FIXTURE_RUN_LOG", log)
	t.Setenv("SILVERWING_NAMING_FIXTURE_UNSUPPORTED_REASONING", "1")
	c := domain.DefaultAppConfig()
	c.CodexExecutablePath = &exe
	service := NewService(paths, nil, nil)
	if _, err := service.suggestRequirementName(context.Background(), c, requirementAIContext{title: "合成标题", body: "合成正文"}, nil, c.AIRequirementNamingModel); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("unexpected naming calls: %d", len(lines))
	}
	for i, line := range lines {
		var call struct{ Args []string }
		if err := json.Unmarshal([]byte(line), &call); err != nil {
			t.Fatal(err)
		}
		assertNamingIsolation(t, call.Args)
		if slices.Contains(call.Args, `model_reasoning_effort="low"`) != (i == 0) {
			t.Fatal("reasoning fallback changed incorrectly")
		}
	}
}

func TestNamingDoesNotRunWithoutIsolation(t *testing.T) {
	exe, _ := os.Executable()
	paths := storage.NewPaths(t.TempDir())
	capture := filepath.Join(paths.Home, "capture.json")
	t.Setenv("SILVERWING_NAMING_FIXTURE_CHILD", "1")
	t.Setenv("SILVERWING_NAMING_FIXTURE_CAPTURE", capture)
	t.Setenv("SILVERWING_NAMING_FIXTURE_MCP_FAIL", "1")
	c := domain.DefaultAppConfig()
	c.CodexExecutablePath = &exe
	_, err := NewService(paths, nil, nil).suggestRequirementName(context.Background(), c, requirementAIContext{title: "合成标题"}, nil, c.AIRequirementNamingModel)
	if err == nil || !strings.Contains(err.Error(), "隔离准备失败") {
		t.Fatal(err)
	}
	if _, err := os.Stat(capture); !os.IsNotExist(err) {
		t.Fatal("AI executed without isolation")
	}
}

func TestForcedNamingReusesRequirementBodyCache(t *testing.T) {
	exe, _ := os.Executable()
	paths := storage.NewPaths(t.TempDir())
	capture, marker := filepath.Join(paths.Home, "capture.json"), filepath.Join(paths.Home, "remote.txt")
	t.Setenv("SILVERWING_NAMING_FIXTURE_CHILD", "1")
	t.Setenv("SILVERWING_NAMING_FIXTURE_CAPTURE", capture)
	t.Setenv("SILVERWING_NAMING_FIXTURE_REMOTE_MARKER", marker)
	c := domain.DefaultAppConfig()
	c.CodexExecutablePath, c.MeegleExecutablePath = &exe, &exe
	c.MeegleProjects = []domain.MeegleProjectConfig{{ProjectKey: "project-a", SimpleName: "space"}}
	service := NewService(paths, nil, nil)
	_, _, err := service.requirementIdentityFor(context.Background(), c, "", false, func(context.Context, domain.AppConfig) (string, bool, error) { return "account-a", false, nil })
	if err != nil {
		t.Fatal(err)
	}
	item := domain.ParticipatedWorkItem{ProjectKey: "project-a", Type: "bug", ID: "123456", Title: "最新缓存标题", URL: "https://project.feishu.cn/space/bug/detail/123456"}
	if _, err := service.writeRequirementDocument(requirementCacheKey("body", "account-a", item), item, "cache-only-body", 1234); err != nil {
		t.Fatal(err)
	}
	item.Title = "旧列表标题"
	if _, err := service.generateRequirementNaming(context.Background(), c, item, "project-a:bug:123456", c.AIRequirementNamingModel, nil, true, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("forced naming re-fetched requirement")
	}
	raw, err := os.ReadFile(capture)
	if err != nil {
		t.Fatal(err)
	}
	var call struct {
		Args  []string
		Stdin string
	}
	if err := json.Unmarshal(raw, &call); err != nil {
		t.Fatal(err)
	}
	assertNamingIsolation(t, call.Args)
	if !strings.Contains(call.Stdin, "cache-only-body") || !strings.Contains(call.Stdin, "最新缓存标题") || strings.Contains(call.Stdin, "旧列表标题") {
		t.Fatal("naming did not use the latest cached title/body")
	}
}
