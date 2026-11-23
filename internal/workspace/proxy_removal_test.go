package workspace

import (
	"context"
	"os"
	"slices"
	"testing"

	"silverwing-go/internal/domain"
)

func TestConfiguredGitIgnoresRetiredProxyTarget(t *testing.T) {
	config := domain.DefaultAppConfig()
	config.CommandProxyTargets = append(config.CommandProxyTargets, "GIT")
	git := NewGitClientWithConfig(nil, config)
	if len(git.Environment) != 0 || !slices.Contains(git.RemoveEnvironment, "HTTP_PROXY") || !slices.Contains(git.RemoveEnvironment, "https_proxy") {
		t.Fatalf("Git inherited or received a Silverwing proxy: environment keys=%d, removals=%v", len(git.Environment), git.RemoveEnvironment)
	}
}

func TestWorkspaceCommandIgnoresRetiredProxyTarget(t *testing.T) {
	t.Setenv("SILVERWING_TEST_SHORTCUT_ENVIRONMENT", "1")
	t.Setenv("HTTP_PROXY", "http://inherited.invalid:1234")
	t.Setenv("HTTPS_PROXY", "http://inherited.invalid:1234")
	config := domain.DefaultAppConfig()
	config.CommandProxyTargets = append(config.CommandProxyTargets, "WORKSPACE_COMMAND")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewWorkspaceCommandService(nil).Execute(context.Background(), config, t.TempDir(), domain.WorkspaceCommandConfig{
		ID: "proxy-test", Name: "Proxy test", Executable: executable, WorkingDirectory: ".", Enabled: true, TimeoutSeconds: 10,
		Arguments: []string{"-test.run=^TestWorkspaceCommandProxyHelper$"},
	}, nil)
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("shortcut child environment failed: exit=%d, stdout=%q, stderr=%q, error=%v", result.ExitCode, result.Stdout, result.Stderr, err)
	}
}

func TestWorkspaceCommandProxyHelper(t *testing.T) {
	if os.Getenv("SILVERWING_TEST_SHORTCUT_ENVIRONMENT") != "1" {
		return
	}
	for _, name := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		if _, exists := os.LookupEnv(name); exists {
			t.Errorf("shortcut inherited or received %s", name)
		}
	}
}
