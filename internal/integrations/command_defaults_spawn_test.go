package integrations

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
	"slices"
	"testing"
)

// The already-built test executable acts only as a private fake Codex child.
// No shell, real Codex executable, account, network or model is contacted.
func init() {
	if os.Getenv("SILVERWING_NAMING_FIXTURE_CHILD") != "1" {
		return
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
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy", "NO_PROXY", "no_proxy"} {
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
	if os.WriteFile(output, []byte(`{"folderName":"命名示例","branchSuffix":"naming_fixture"}`), 0600) != nil {
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
			for _, target := range []domain.CommandProxyTarget{domain.ProxyTargetGit, domain.ProxyTargetWorkspaceCommand, domain.ProxyTargetMeegle, domain.ProxyTargetGenbu} {
				env, remove := service.childEnvironment(c, target)
				if len(env) != 0 || !slices.Contains(remove, "HTTP_PROXY") {
					t.Fatalf("unselected target %s inherited proxy", target)
				}
			}
		})
	}
}
