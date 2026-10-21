package platform

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
)

func TestDiagnosticCredentialRedaction(t *testing.T) {
	for _, input := range []string{
		"Authorization: Bearer bearer-secret",
		"token=one-secret access_token=two-secret refresh_token=three-secret",
		`{"access_token":"json-secret","refresh_token":"refresh-secret"}`,
		"access_token=first-secret&access_token=second-secret",
		`{"Authorization":"Bearer json-bearer-secret"}`,
	} {
		t.Run(input, func(t *testing.T) {
			got := sanitizeDiagnosticText(input)
			if strings.Contains(got, "secret") {
				t.Fatalf("diagnostic output retained a credential: %q", got)
			}
		})
	}
	if got := sanitizeDiagnosticText(`{"access_token":"json-secret","Authorization":"Bearer bearer-secret"}`); !json.Valid([]byte(got)) {
		t.Fatalf("JSON diagnostic summary was damaged: %q", got)
	}
	const ordinary = "No credentials; token count: 3"
	if got := sanitizeDiagnosticText(ordinary); got != ordinary {
		t.Fatalf("ordinary output changed: %q", got)
	}
}

func TestProxyCredentialRedactionMatchesCommandEnvironment(t *testing.T) {
	endpoint, username, password := "http://proxy.invalid:18080", " user@example ", "pass@word"
	cfg := domain.AppConfig{CommandProxyURL: &endpoint, CommandProxyUsername: &username, CommandProxyPassword: &password, CommandProxyTargets: []domain.CommandProxyTarget{domain.ProxyTargetGit}}
	env, _ := commandEnvironment(cfg, domain.ProxyTargetGit)
	if got := redactProxy(cfg, env["HTTPS_PROXY"]); got != "[代理地址已隐藏]" {
		t.Fatalf("actual command proxy URL was not hidden: %q", got)
	}
}

func TestBoundedPlatformFileReads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "limited.txt")
	if err := os.WriteFile(path, []byte("12345"), 0600); err != nil {
		t.Fatal(err)
	}
	if data, err := readBounded(path, 5); err != nil || string(data) != "12345" {
		t.Fatalf("exact boundary failed: %q %v", data, err)
	}
	if _, err := readBounded(path, 4); err == nil {
		t.Fatal("oversized file accepted")
	}
	if _, err := readBounded(filepath.Dir(path), 5); err == nil {
		t.Fatal("directory accepted")
	}
}
