package platform

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/executable"
	"silverwing-go/internal/storage"
)

func TestCodexExtensionDefaultProxyEnvironmentIsTargetScoped(t *testing.T) {
	c := domain.DefaultAppConfig()
	env, removed := commandEnvironment(c, cliCommandProxyTarget(executable.ToolCodex))
	if env["HTTP_PROXY"] != "http://127.0.0.1:7897" || env["HTTPS_PROXY"] != "http://127.0.0.1:7897" || !slices.Contains(removed, "http_proxy") {
		t.Fatal("default Codex RPC proxy env missing")
	}
	for _, tool := range []executable.Tool{executable.ToolGit, executable.ToolMeegle} {
		env, remove := commandEnvironment(c, cliCommandProxyTarget(tool))
		if len(env) != 0 || !slices.Contains(remove, "HTTP_PROXY") {
			t.Fatalf("%s received proxy settings or retained inherited proxy variables", tool)
		}
	}
	c.CommandProxyTargets = []domain.CommandProxyTarget{}
	if env, _ := commandEnvironment(c, domain.ProxyTargetCodex); len(env) != 0 {
		t.Fatal("explicit disabled target overwritten")
	}
}

func TestRunToolProxyEnvironment(t *testing.T) {
	t.Setenv("SW_CLI_PROXY_TEST_CHILD", "1")
	t.Setenv("HTTPS_PROXY", "http://inherited.invalid:8080")
	t.Setenv("all_proxy", "http://inherited.invalid:8080")
	command, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(storage.NewPaths(t.TempDir()), nil, nil)
	cfg := domain.DefaultAppConfig()
	proxy := "http://silverwing.invalid:18080"
	cfg.CommandProxyURL = &proxy
	cfg.CommandProxyTargets = []domain.CommandProxyTarget{domain.ProxyTargetCodex, domain.ProxyTargetAppUpdates}
	for _, test := range []struct {
		tool  executable.Tool
		proxy string
	}{
		{executable.ToolGit, ""},
		{executable.ToolMeegle, ""},
		{executable.ToolCodex, proxy},
	} {
		t.Run(string(test.tool), func(t *testing.T) {
			result, err := s.runTool(context.Background(), cfg, test.tool, command, []string{"-test.run=^TestCLIProxyEnvironmentChild$"}, "", 10*time.Second)
			if err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf("HTTPS_PROXY=%q all_proxy=%q\n", test.proxy, test.proxy)
			if !strings.Contains(result.Stdout, want) {
				t.Fatalf("expected %q in child stdout, got %q", want, result.Stdout)
			}
		})
	}
}

func TestCLIProxyEnvironmentChild(t *testing.T) {
	if os.Getenv("SW_CLI_PROXY_TEST_CHILD") != "1" {
		t.Skip("child process fixture")
	}
	fmt.Printf("HTTPS_PROXY=%q all_proxy=%q\n", os.Getenv("HTTPS_PROXY"), os.Getenv("all_proxy"))
}
