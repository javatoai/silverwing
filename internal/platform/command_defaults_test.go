package platform

import (
	"silverwing-go/internal/domain"
	"slices"
	"testing"
)

func TestCodexExtensionDefaultProxyEnvironmentIsTargetScoped(t *testing.T) {
	c := domain.DefaultAppConfig()
	env, removed := commandEnvironment(c, domain.ProxyTargetCodex)
	if env["HTTP_PROXY"] != "http://127.0.0.1:7897" || env["HTTPS_PROXY"] != "http://127.0.0.1:7897" || !slices.Contains(removed, "http_proxy") {
		t.Fatal("default Codex RPC proxy env missing")
	}
	for _, target := range []domain.CommandProxyTarget{domain.ProxyTargetGit, domain.ProxyTargetWorkspaceCommand, domain.ProxyTargetMeegle, domain.ProxyTargetLark} {
		env, remove := commandEnvironment(c, target)
		if len(env) != 0 || !slices.Contains(remove, "HTTP_PROXY") {
			t.Fatal("non-Codex target changed")
		}
	}
	c.CommandProxyTargets = []domain.CommandProxyTarget{}
	if env, _ := commandEnvironment(c, domain.ProxyTargetCodex); len(env) != 0 {
		t.Fatal("explicit disabled target overwritten")
	}
}
