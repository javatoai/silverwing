//go:build windows

package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestAstraIntegrationsCLIResponseBoundaries(t *testing.T) {
	root := t.TempDir()
	responsePath := filepath.Join(root, "response.json")
	t.Setenv("SILVERWING_ASTRA_CLI_RESPONSE", responsePath)
	cliPath := filepath.Join(root, "fixture.ps1")
	const script = `$ErrorActionPreference = 'Stop'
if ($args[0] -eq '--version') { Write-Output 'fixture 1.0.0'; exit 0 }
$fixture = Get-Content -Raw -LiteralPath $env:SILVERWING_ASTRA_CLI_RESPONSE | ConvertFrom-Json
if ($fixture.stdout) { [Console]::Out.WriteLine($fixture.stdout) }
if ($fixture.stderr) { [Console]::Error.WriteLine($fixture.stderr) }
exit $fixture.exitCode
`
	if err := os.WriteFile(cliPath, append([]byte{0xef, 0xbb, 0xbf}, []byte(script)...), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewService(storage.NewPaths(filepath.Join(root, "home")), nil, nil)
	config := domain.DefaultAppConfig()
	config.MeegleExecutablePath = &cliPath
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	setResponse := func(stdout, stderr string, exitCode int) {
		t.Helper()
		data, err := json.Marshal(map[string]any{"stdout": stdout, "stderr": stderr, "exitCode": exitCode})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(responsePath, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	setResponse(`{"message":"temporarily unavailable"}`, "", 0)
	if _, err := service.meegleProjects(ctx, config); err == nil {
		t.Fatal("missing project list was reported as a real empty list")
	}
	if _, err := service.meegleStatus(ctx, config); err == nil {
		t.Fatal("missing authentication status was reported as logged out")
	}
	setResponse(`{"projects":[]}`, "", 0)
	if _, err := service.meegleProjects(ctx, config); err != nil {
		t.Fatalf("real empty project list rejected: %v", err)
	}
	setResponse(`{"authenticated":false}`, "", 0)
	status, err := service.meegleStatus(ctx, config)
	if err != nil || object(status)["authenticated"] != false {
		t.Fatalf("explicit logout rejected: %v %v", status, err)
	}
	secret := strings.Repeat("SECRET-PREFIX-", 60)
	config.CommandProxyPassword = &secret
	setResponse("", strings.Repeat("x", 480)+secret, 3)
	_, err = service.runMeegle(ctx, config, []string{"failure"}, 10*time.Second, "fixture failure")
	if err == nil || strings.Contains(err.Error(), "SECRET-PREFIX-") || !utf8.ValidString(err.Error()) {
		t.Fatal("CLI failure was not safely redacted before truncation")
	}
}

func TestAstraIntegrationsProjectCatalogRejectsChangedAccount(t *testing.T) {
	root := t.TempDir()
	fixturePath := filepath.Join(root, "fixture.json")
	t.Setenv("SILVERWING_ASTRA_PROJECT_FIXTURE", fixturePath)
	cliPath := filepath.Join(root, "fixture.ps1")
	const script = `$ErrorActionPreference = 'Stop'
if ($args[0] -eq '--version') { Write-Output 'fixture 1.0.0'; exit 0 }
$fixture = Get-Content -Raw -LiteralPath $env:SILVERWING_ASTRA_PROJECT_FIXTURE | ConvertFrom-Json
$command = $args[0..1] -join ' '
Add-Content -LiteralPath ($env:SILVERWING_ASTRA_PROJECT_FIXTURE + '.calls') -Value $command
switch ($command) {
  'config profile' { Write-Output 'fixture-profile' }
  'auth status' { Write-Output '{"authenticated":true,"host":"fixture.invalid","tenant_key":"fixture-tenant"}' }
  'user me' { @{ user_key = $fixture.account } | ConvertTo-Json -Compress }
  'project search' {
    if ($fixture.switchAccount) {
      $fixture.account = $fixture.switchAccount
      $fixture.switchAccount = ''
      $fixture | ConvertTo-Json -Compress | Set-Content -LiteralPath $env:SILVERWING_ASTRA_PROJECT_FIXTURE -Encoding UTF8
    }
    Write-Output '{"projects":[{"name":"Fixture","project_key":"fixture-project","simple_name":"fixture-space"}]}'
  }
  default { [Console]::Error.WriteLine('unexpected fixture command'); exit 2 }
}
`
	if err := os.WriteFile(cliPath, append([]byte{0xef, 0xbb, 0xbf}, []byte(script)...), 0o600); err != nil {
		t.Fatal(err)
	}
	writeFixture := func(account, switchAccount string) {
		t.Helper()
		data, err := json.Marshal(map[string]string{"account": account, "switchAccount": switchAccount})
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fixturePath, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	service := NewService(storage.NewPaths(filepath.Join(root, "home")), nil, nil)
	config := domain.DefaultAppConfig()
	config.MeegleExecutablePath = &cliPath
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if _, err := service.meegleProjectsForIdentity(ctx, config, nil); err == nil {
		t.Fatal("unscoped project catalog read accepted")
	}
	if _, err := os.Stat(fixturePath + ".calls"); !os.IsNotExist(err) {
		t.Fatal("unscoped project read invoked CLI")
	}
	writeFixture("account-a", "")
	state, identity, err := service.RequirementIdentity(ctx, config)
	if err != nil || state != requirementIdentityAuthenticated || identity == "" {
		t.Fatalf("fixture identity not authenticated: state=%q err=%v", state, err)
	}
	payload := map[string]any{"expectedIdentity": identity}
	if _, err := service.meegleProjectsForIdentity(ctx, config, payload); err != nil {
		t.Fatalf("same-account catalog rejected: %v", err)
	}
	writeFixture("account-a", "account-b")
	value, err := service.meegleProjectsForIdentity(ctx, config, payload)
	var changed *RequirementIdentityChangedError
	if value != nil || !errors.As(err, &changed) {
		t.Fatalf("catalog returned after account changed: value=%v err=%v", value, err)
	}
	if changed.Identity == identity || !validOpaqueRequirementIdentity(changed.Identity) {
		t.Fatalf("changed account did not return a distinct opaque identity: %+v", changed)
	}
}
