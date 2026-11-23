package integrations

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

var fixtureProxyKeys = []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy", "NO_PROXY", "no_proxy"}

// The already-built test executable acts as a private fake CLI child.
func init() {
	if os.Getenv("SILVERWING_NAMING_FIXTURE_CHILD") != "1" {
		return
	}
	if slices.Contains(os.Args, "--fixture-meegle-proxy-env") {
		env := map[string]string{}
		for _, key := range fixtureProxyKeys {
			if value, ok := os.LookupEnv(key); ok {
				env[key] = value
			}
		}
		if json.NewEncoder(os.Stdout).Encode(env) != nil {
			os.Exit(84)
		}
		os.Exit(0)
	}
	output := ""
	for i, arg := range os.Args {
		if arg == "--output-last-message" && i+1 < len(os.Args) {
			output = os.Args[i+1]
		}
	}
	if output == "" {
		if slices.Contains(os.Args, "workitem") {
			os.WriteFile(os.Getenv("SILVERWING_NAMING_FIXTURE_REMOTE_MARKER"), []byte("unexpected remote read"), 0600)
			os.Exit(73)
		}
		if slices.Contains(os.Args, "mcp") {
			if os.Getenv("SILVERWING_NAMING_FIXTURE_MCP_FAIL") == "1" {
				os.Exit(74)
			}
			io.WriteString(os.Stdout, `[{"name":"node_repl","enabled":true},{"name":"fixture.mcp-name","enabled":true}]`)
			os.Exit(0)
		}
		return
	}
	prompt, _ := io.ReadAll(os.Stdin)
	env := map[string]string{}
	for _, key := range fixtureProxyKeys {
		env[key] = os.Getenv(key)
	}
	data, _ := json.Marshal(struct {
		Args  []string
		Env   map[string]string
		Stdin string
	}{os.Args[1:], env, string(prompt)})
	if os.WriteFile(os.Getenv("SILVERWING_NAMING_FIXTURE_CAPTURE"), data, 0600) != nil {
		os.Exit(81)
	}
	if log := os.Getenv("SILVERWING_NAMING_FIXTURE_RUN_LOG"); log != "" {
		f, e := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if e != nil {
			os.Exit(83)
		}
		f.Write(append(data, '\n'))
		f.Close()
	}
	if os.Getenv("SILVERWING_NAMING_FIXTURE_UNSUPPORTED_REASONING") == "1" && slices.Contains(os.Args, `model_reasoning_effort="low"`) {
		io.WriteString(os.Stderr, "reasoning unsupported")
		os.Exit(75)
	}
	result := os.Getenv("SILVERWING_NAMING_FIXTURE_RESULT")
	if result == "" {
		result = `{"folderName":"命名示例","branchSuffix":"naming_fixture"}`
	}
	if os.WriteFile(output, []byte(result), 0600) != nil {
		os.Exit(82)
	}
	os.Exit(0)
}
func TestNamingSpawnUsesDefaultOrExplicitModelAndCodexProxy(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"default", "custom", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			paths := storage.NewPaths(t.TempDir())
			capture := filepath.Join(paths.Home, "capture.json")
			t.Setenv("SILVERWING_NAMING_FIXTURE_CHILD", "1")
			t.Setenv("SILVERWING_NAMING_FIXTURE_CAPTURE", capture)
			t.Setenv("HTTP_PROXY", "http://inherited.invalid:1")
			t.Setenv("HTTPS_PROXY", "http://inherited.invalid:1")
			c := domain.DefaultAppConfig()
			c.CodexExecutablePath = &exe
			expected := "http://127.0.0.1:7897"
			if mode == "custom" {
				expected = "http://127.0.0.1:9080"
				c.CommandProxyURL = &expected
				c.AIRequirementNamingModel = "gpt-5.6-luna"
			}
			if mode == "disabled" {
				c.CommandProxyURL = nil
				c.CommandProxyTargets = []domain.CommandProxyTarget{}
				expected = ""
			}
			service := NewService(paths, nil, nil)
			result, err := service.suggestRequirementName(context.Background(), c, requirementAIContext{title: "fixture", body: "synthetic"}, nil, c.AIRequirementNamingModel)
			if err != nil {
				t.Fatal(err)
			}
			if result.(domain.RequirementAiNamingSuggestion).FolderName != "命名示例" {
				t.Fatal("fake response not read")
			}
			data, err := os.ReadFile(capture)
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				Args []string
				Env  map[string]string
			}
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatal(err)
			}
			model := slices.Index(got.Args, "--model")
			if model < 0 || model+1 >= len(got.Args) || got.Args[model+1] != c.AIRequirementNamingModel {
				t.Fatalf("model args %q", got.Args)
			}
			for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"} {
				if got.Env[key] != expected {
					t.Fatalf("%s=%q want %q", key, got.Env[key], expected)
				}
			}
			if got.Env["NO_PROXY"] != "" || got.Env["no_proxy"] != "" {
				t.Fatal("unexpected proxy exceptions")
			}
		})
	}
}

func TestMeegleSpawnIgnoresRetiredProxyTarget(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SILVERWING_NAMING_FIXTURE_CHILD", "1")
	for _, key := range fixtureProxyKeys {
		t.Setenv(key, "inherited-proxy-value")
	}
	proxy, noProxy, username, password := "http://proxy.invalid:8080", "fixture.invalid", "fixture-user", "fixture-password"
	config := domain.DefaultAppConfig()
	config.MeegleExecutablePath = &exe
	config.CommandProxyURL, config.CommandProxyNoProxy = &proxy, &noProxy
	config.CommandProxyUsername, config.CommandProxyPassword = &username, &password
	config.CommandProxyTargets = []domain.CommandProxyTarget{"MEEGLE", domain.ProxyTargetCodex, domain.ProxyTargetAppUpdates}
	service := NewService(storage.NewPaths(t.TempDir()), nil, nil)
	result, err := service.runMeegle(context.Background(), config, []string{"--fixture-meegle-proxy-env"}, 10*time.Second, "读取测试环境")
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(result.Stdout), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("Meegle child received proxy variables: %v", got)
	}
}

func TestOnlyCodexProxyAffectsRequirementNamingScope(t *testing.T) {
	service := NewService(storage.NewPaths(t.TempDir()), nil, nil)
	cli := "fixture-meegle"
	config := domain.DefaultAppConfig()
	config.MeegleExecutablePath = &cli
	config.CommandProxyTargets = []domain.CommandProxyTarget{domain.ProxyTargetCodex}

	changed := config
	changed.CommandProxyTargets = []domain.CommandProxyTarget{domain.ProxyTargetCodex, domain.ProxyTargetAppUpdates, "MEEGLE"}
	if service.requirementNamingContext(config, "fixture") != service.requirementNamingContext(changed, "fixture") {
		t.Fatal("unrelated targets changed the Codex naming scope")
	}

	proxy := "http://proxy.invalid:8080"
	changed.CommandProxyURL = &proxy
	if service.requirementNamingContext(config, "fixture") == service.requirementNamingContext(changed, "fixture") {
		t.Fatal("new Codex proxy did not change the naming scope")
	}
	if service.requirementIdentityContext(config) != service.requirementIdentityContext(changed) ||
		requirementReadConfig(config) != requirementReadConfig(changed) ||
		noticeConfigIdentity(config) != noticeConfigIdentity(changed) {
		t.Fatal("SW proxy changed a Meegle or Lark scope")
	}
}
