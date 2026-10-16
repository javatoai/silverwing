//go:build windows

package integrations

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
)

func TestRequirementCacheV2ExecuteEntriesReuseDiskCacheAndKeepStaleValues(t *testing.T) {
	root := t.TempDir()
	paths := storage.NewPaths(filepath.Join(root, "isolated-home"))
	fixturePath := filepath.Join(root, "fixture.json")
	callsPath := filepath.Join(root, "calls.log")
	fixture := map[string]string{"calls": callsPath, "mode": ""}
	writeRequirementCacheV2Fixture(t, fixturePath, fixture)
	t.Setenv("SILVERWING_REQUIREMENT_CACHE_FIXTURE", fixturePath)
	cliPath := filepath.Join(root, "meegle-fixture.ps1")
	if err := os.WriteFile(cliPath, append([]byte{0xef, 0xbb, 0xbf}, []byte(requirementCacheV2FixtureScript)...), 0o600); err != nil {
		t.Fatalf("write controlled CLI fixture: %v", err)
	}

	config := domain.DefaultAppConfig()
	config.MeegleExecutablePath = &cliPath
	config.MeegleProjects = []domain.MeegleProjectConfig{{ProjectKey: "project-a", SimpleName: "space"}}
	store := storage.NewConfigStore(paths)
	if err := store.Save(config); err != nil {
		t.Fatalf("save isolated test config: %v", err)
	}
	loadedConfig, err := store.Load()
	if err != nil {
		t.Fatalf("load isolated test config: %v", err)
	}
	config = loadedConfig
	service := NewService(paths, store, process.NewRunner())
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	state, identity, err := service.RequirementIdentity(ctx, config)
	if err != nil || state != requirementIdentityAuthenticated || !validOpaqueRequirementIdentity(identity) {
		t.Fatalf("confirm fixture identity: state=%q identity=%q err=%v", state, identity, err)
	}
	confirmed, loggedOut, confirmErr := service.requirementIdentityFor(ctx, config, identity, false, nil)
	verifyErr := service.verifyRequirementIdentitySession(ctx, config, confirmed.raw, confirmed.generation)
	if confirmErr != nil || loggedOut || verifyErr != nil {
		t.Fatalf("confirmed identity session was invalid before entry read: session=%+v loggedOut=%v err=%v verify=%v", confirmed, loggedOut, confirmErr, verifyErr)
	}
	payload := func(id string, force bool) map[string]any {
		return map[string]any{"url": "https://project.feishu.cn/space/userstory/detail/" + id,
			"projectKey": "project-a", "projectName": "space", "type": "userstory", "id": id,
			"expectedIdentity": identity, "force": force}
	}

	metadataValue, err := service.Execute(ctx, "requirements.metadata", payload("101", false))
	metadata, metadataOK := metadataValue.(RequirementMetadataReadResult)
	if err != nil || !metadataOK || metadata.Title == nil || *metadata.Title != "Fixture requirement 101" {
		t.Fatalf("first metadata entry read: value=%#v err=%v calls=%v identity=%q context=%q", metadataValue, err,
			requirementCacheV2FixtureCalls(t, callsPath), identity, service.requirementIdentityContext(config))
	}
	bodyValue, err := service.Execute(ctx, "requirements.read", payload("101", false))
	body, bodyOK := bodyValue.(RequirementReadResult)
	if err != nil || !bodyOK || !strings.Contains(body.Body, "fixture body 101") || body.Source != "REMOTE" {
		t.Fatalf("first body entry read: value=%#v err=%v", bodyValue, err)
	}
	commentsValue, err := service.Execute(ctx, "requirements.comments", payload("101", false))
	comments, commentsOK := commentsValue.(RequirementCommentsReadResult)
	if err != nil || !commentsOK || comments.Source != "REMOTE" || len(comments.Comments) != 0 {
		t.Fatalf("first comments entry read: value=%#v err=%v", commentsValue, err)
	}
	initialCalls := requirementCacheV2FixtureCalls(t, callsPath)
	initialWorkItems := requirementCacheV2CountCalls(initialCalls, "workitem get ")
	initialComments := requirementCacheV2CountCalls(initialCalls, "comment list ")
	if initialWorkItems != 2 || initialComments != 1 {
		t.Fatalf("unexpected first-read remote calls: workitem=%d comments=%d calls=%v", initialWorkItems, initialComments, initialCalls)
	}

	// A warm service replays all three data sources without invoking any CLI.
	for _, kind := range []string{"requirements.metadata", "requirements.read", "requirements.comments"} {
		if _, err := service.Execute(ctx, kind, payload("101", false)); err != nil {
			t.Fatalf("warm %s entry read: %v", kind, err)
		}
	}
	if calls := requirementCacheV2FixtureCalls(t, callsPath); len(calls) != len(initialCalls) {
		t.Fatalf("warm cache entry invoked CLI: before=%v after=%v", initialCalls, calls)
	}

	// A fresh Service models a process restart: it probes identity once, then
	// reads the same persisted metadata/body/comments without refetching them.
	restarted := NewService(paths, storage.NewConfigStore(paths), process.NewRunner())
	for _, kind := range []string{"requirements.metadata", "requirements.read", "requirements.comments"} {
		value, err := restarted.Execute(ctx, kind, payload("101", false))
		if err != nil {
			t.Fatalf("cold-process %s disk-cache read: %v", kind, err)
		}
		switch result := value.(type) {
		case RequirementReadResult:
			if result.Source != "CACHE" || result.Body != body.Body {
				t.Fatalf("cold-process body did not use persisted cache: %+v", result)
			}
		case RequirementCommentsReadResult:
			if result.Source != "CACHE" {
				t.Fatalf("cold-process comments did not use persisted cache: %+v", result)
			}
		case RequirementMetadataReadResult:
			if result.Title == nil || *result.Title != *metadata.Title || result.Stale {
				t.Fatalf("cold-process metadata did not use persisted cache: %+v", result)
			}
		default:
			t.Fatalf("unexpected cold-process %s result type %T", kind, value)
		}
	}
	coldCalls := requirementCacheV2FixtureCalls(t, callsPath)
	if len(coldCalls) != len(initialCalls)+3 || requirementCacheV2CountCalls(coldCalls, "workitem get ") != initialWorkItems ||
		requirementCacheV2CountCalls(coldCalls, "comment list ") != initialComments {
		t.Fatalf("fresh service should run only the three identity checks, calls=%v", coldCalls)
	}

	// Explicit refresh failures return each prior local value as stale. The
	// failed refresh must not replace the on-disk snapshot.
	writeRequirementCacheV2Fixture(t, fixturePath, map[string]string{"calls": callsPath, "mode": "fail-workitem"})
	metadataValue, err = restarted.Execute(ctx, "requirements.metadata", payload("101", true))
	staleMetadata, metadataOK := metadataValue.(RequirementMetadataReadResult)
	if err != nil || !metadataOK || !staleMetadata.Stale || staleMetadata.RefreshError == "" ||
		staleMetadata.Title == nil || *staleMetadata.Title != *metadata.Title {
		t.Fatalf("failed metadata refresh did not preserve stale value: value=%#v err=%v", metadataValue, err)
	}
	bodyValue, err = restarted.Execute(ctx, "requirements.read", payload("101", true))
	staleBody, bodyOK := bodyValue.(RequirementReadResult)
	if err != nil || !bodyOK || !staleBody.Stale || staleBody.RefreshError == "" || staleBody.Body != body.Body {
		t.Fatalf("failed body refresh did not preserve stale value: value=%#v err=%v", bodyValue, err)
	}
	writeRequirementCacheV2Fixture(t, fixturePath, map[string]string{"calls": callsPath, "mode": "fail-comments"})
	commentsValue, err = restarted.Execute(ctx, "requirements.comments", payload("101", true))
	staleComments, commentsOK := commentsValue.(RequirementCommentsReadResult)
	if err != nil || !commentsOK || !staleComments.Stale || staleComments.RefreshError == "" {
		t.Fatalf("failed comments refresh did not preserve stale value: value=%#v err=%v", commentsValue, err)
	}
}

func writeRequirementCacheV2Fixture(t *testing.T, path string, value map[string]string) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode fixture control: %v", err)
	}
	if err := os.WriteFile(path, encoded, 0o600); err != nil {
		t.Fatalf("write fixture control: %v", err)
	}
}

func requirementCacheV2FixtureCalls(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("read fixture command log: %v", err)
	}
	lines := strings.Split(strings.TrimPrefix(string(data), "\ufeff"), "\r\n")
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" {
			result = append(result, line)
		}
	}
	return result
}

func requirementCacheV2CountCalls(calls []string, prefix string) int {
	count := 0
	for _, call := range calls {
		if strings.HasPrefix(call, prefix) {
			count++
		}
	}
	return count
}

const requirementCacheV2FixtureScript = `$ErrorActionPreference = 'Stop'
$fixturePath = $env:SILVERWING_REQUIREMENT_CACHE_FIXTURE
try {
  $fixture = Get-Content -Raw -LiteralPath $fixturePath | ConvertFrom-Json
  $arguments = @($args)
  $call = $arguments -join ' '
  [System.IO.File]::AppendAllText($fixture.calls, ($call + [Environment]::NewLine), (New-Object System.Text.UTF8Encoding($false)))
  $id = ''
  for ($i = 0; $i -lt $arguments.Length - 1; $i++) { if ($arguments[$i] -eq '--work-item-id') { $id = $arguments[$i + 1] } }
  if ($arguments.Length -ge 3 -and $arguments[0] -eq 'config' -and $arguments[1] -eq 'profile') { Write-Output 'fixture-profile'; exit 0 }
  if ($arguments.Length -ge 2 -and $arguments[0] -eq 'auth' -and $arguments[1] -eq 'status') { @{authenticated=$true; host='project.feishu.cn'; tenant_key='fixture-tenant'} | ConvertTo-Json -Compress; exit 0 }
  if ($arguments.Length -ge 2 -and $arguments[0] -eq 'user' -and $arguments[1] -eq 'me') { @{user_key='fixture-account'; tenant_key='fixture-tenant'} | ConvertTo-Json -Compress; exit 0 }
  if ($arguments.Length -ge 2 -and $arguments[0] -eq 'workitem' -and $arguments[1] -eq 'get') {
    if ($fixture.mode -eq 'fail-workitem') { [Console]::Error.WriteLine('fixture workitem failure'); exit 3 }
    @{name=('Fixture requirement ' + $id); description=('fixture body ' + $id); work_item_attribute=@{name=('Fixture requirement ' + $id); work_item_status=@{name='In Progress'}; role_members=@()}} | ConvertTo-Json -Depth 12 -Compress
    exit 0
  }
  if ($arguments.Length -ge 2 -and $arguments[0] -eq 'comment' -and $arguments[1] -eq 'list') {
    if ($fixture.mode -eq 'fail-comments') { [Console]::Error.WriteLine('fixture comments failure'); exit 3 }
    @{comments=@(); pagination=@{page_num=1; total=0; total_pages=1; has_more=$false}} | ConvertTo-Json -Depth 12 -Compress
    exit 0
  }
  [Console]::Error.WriteLine('fixture rejects unknown command')
  exit 2
} catch {
  [Console]::Error.WriteLine($_.Exception.Message)
  exit 2
}
`
