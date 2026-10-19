//go:build windows

package integrations

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestRequirementRefreshRejectsAccountSwitchedDuringRemoteRead(t *testing.T) {
	root := t.TempDir()
	paths := storage.NewPaths(filepath.Join(root, "home"))
	fixturePath, cliPath := filepath.Join(root, "fixture.json"), filepath.Join(root, "meegle.ps1")
	writeRequirementCacheV2Fixture(t, fixturePath, map[string]string{"calls": filepath.Join(root, "calls.log"), "account": "fixture-account", "mode": "switch-account"})
	t.Setenv("SILVERWING_REQUIREMENT_CACHE_FIXTURE", fixturePath)
	script := strings.Replace(requirementCacheV2FixtureScript, "user_key='fixture-account'", "user_key=$fixture.account", 1)
	script = strings.Replace(script, "    if ($fixture.mode -eq 'fail-workitem')", `    if ($fixture.mode -eq 'switch-account') { $fixture.account='different-account'; $fixture | ConvertTo-Json -Compress | Set-Content -LiteralPath $fixturePath -Encoding UTF8 }
    if ($fixture.mode -eq 'fail-workitem')`, 1)
	if err := os.WriteFile(cliPath, append([]byte{0xef, 0xbb, 0xbf}, []byte(script)...), 0600); err != nil {
		t.Fatal(err)
	}
	config := domain.DefaultAppConfig()
	config.MeegleExecutablePath = &cliPath
	config.MeegleProjects = []domain.MeegleProjectConfig{{ProjectKey: "project-a", SimpleName: "space"}}
	store := storage.NewConfigStore(paths)
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	config, _ = store.Load()
	s := NewService(paths, store, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, token, err := s.RequirementIdentity(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Execute(ctx, "requirements.metadata", map[string]any{"url": "https://project.feishu.cn/space/userstory/detail/101", "force": true, "fullRefresh": true, "expectedIdentity": token})
	var changed *RequirementIdentityChangedError
	if !errors.As(err, &changed) {
		t.Fatalf("changed CLI account accepted: %v", err)
	}
	files, err := filepath.Glob(filepath.Join(paths.Cache, "requirement-metadata", "*.json"))
	if err != nil || len(files) != 0 {
		t.Fatalf("account-switched result reached cache: %v %v", files, err)
	}
}

func TestRequirementRefreshExecutePersistsProjectionEstimateAndOverlaysCachedList(t *testing.T) {
	root := t.TempDir()
	paths := storage.NewPaths(filepath.Join(root, "home"))
	fixturePath, callsPath, cliPath := filepath.Join(root, "fixture.json"), filepath.Join(root, "calls.log"), filepath.Join(root, "meegle.ps1")
	writeRequirementCacheV2Fixture(t, fixturePath, map[string]string{"calls": callsPath, "mode": ""})
	t.Setenv("SILVERWING_REQUIREMENT_CACHE_FIXTURE", fixturePath)
	script := strings.Replace(requirementCacheV2FixtureScript, "  if ($arguments.Length -ge 2 -and $arguments[0] -eq 'workitem' -and $arguments[1] -eq 'get') {", requirementRefreshFixtureBranches+"\n  if ($arguments.Length -ge 2 -and $arguments[0] -eq 'workitem' -and $arguments[1] -eq 'get') {", 1)
	script = strings.Replace(script, "role_members=@()", "role_members=@(@{name='Dev Owner'; members=@(@{name='Updated dev'})},@{name='QC Owner';members=@(@{name='Updated test'})},@{name='产品经理';members=@(@{name='Updated PM'})})", 1)
	script = "$OutputEncoding = [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)\n" + script
	if err := os.WriteFile(cliPath, append([]byte{0xef, 0xbb, 0xbf}, []byte(script)...), 0600); err != nil {
		t.Fatal(err)
	}
	config := domain.DefaultAppConfig()
	config.MeegleExecutablePath = &cliPath
	config.MeegleProjects = []domain.MeegleProjectConfig{{ProjectKey: "project-a", SimpleName: "space"}}
	store := storage.NewConfigStore(paths)
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	config, _ = store.Load()
	s := NewService(paths, store, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	_, token, err := s.RequirementIdentity(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	payload := map[string]any{"url": "https://project.feishu.cn/space/userstory/detail/101", "expectedIdentity": token, "force": true, "fullRefresh": true, "sprintKey": "project-a:900"}
	value, err := s.Execute(ctx, "requirements.metadata", payload)
	if err != nil {
		t.Fatal(err)
	}
	metadata := value.(RequirementMetadataReadResult)
	if metadata.CacheError != "" || metadata.EstimateError != "" || metadata.WorkItem == nil || len(metadata.WorkItem.Developers) != 1 || valuePointer(metadata.SprintEstimates["project-a:900"].Days) != "0" {
		t.Fatalf("incomplete single refresh: %+v", metadata)
	}
	for _, kind := range []string{"requirements.read", "requirements.comments"} {
		if _, err := s.Execute(ctx, kind, payload); err != nil {
			t.Fatal(err)
		}
	}
	item := domain.ParticipatedWorkItem{ProjectKey: "project-a", ProjectName: "space", Type: "userstory", ID: "101", URL: payload["url"].(string), Title: "old", MySprintEstimateDays: refreshString("99")}
	keys, err := participatedListCacheKeysFor(config, token, map[string]any{"sprintKey": "project-a:900"})
	if err != nil {
		t.Fatal(err)
	}
	listKey, _ := keys.requestedKey()
	if err := s.writeRequirementListCache(listKey, participatedResult{Items: []domain.ParticipatedWorkItem{item}, FetchedAt: metadata.FetchedAt - 1, SelectedSprintKey: refreshString("project-a:900")}); err != nil {
		t.Fatal(err)
	}
	before := requirementCacheV2FixtureCalls(t, callsPath)
	writeRequirementCacheV2Fixture(t, fixturePath, map[string]string{"calls": callsPath, "mode": "fail-workitem"})
	restarted := NewService(paths, store, nil)
	payload["force"] = false
	for _, kind := range []string{"requirements.metadata", "requirements.read", "requirements.comments"} {
		if _, err := restarted.Execute(ctx, kind, payload); err != nil {
			t.Fatalf("restart cache %s: %v", kind, err)
		}
	}
	listValue, err := restarted.Execute(ctx, "requirements.list", map[string]any{"expectedIdentity": token, "sprintKey": "project-a:900"})
	if err != nil {
		t.Fatal(err)
	}
	got := listValue.(participatedResult).Items[0]
	if got.Title != "Fixture requirement 101" || valuePointer(got.Status) != "In Progress" || len(got.Developers) != 1 || len(got.QCOwners) != 1 || len(got.ProductManagers) != 1 || valuePointer(got.MySprintEstimateDays) != "0" {
		t.Fatalf("cached list ignored newer single refresh: %+v", got)
	}
	after := requirementCacheV2FixtureCalls(t, callsPath)
	if requirementCacheV2CountCalls(after, "workitem get ") != requirementCacheV2CountCalls(before, "workitem get ") || requirementCacheV2CountCalls(after, "comment list ") != requirementCacheV2CountCalls(before, "comment list ") || requirementCacheV2CountCalls(after, "workflow get-node ") != requirementCacheV2CountCalls(before, "workflow get-node ") {
		t.Fatal("page reentry unexpectedly fetched remote content")
	}
	// Disk failure must be visible, even when the remote metadata succeeded.
	blocked := storage.NewPaths(filepath.Join(root, "blocked-home"))
	blockedStore := storage.NewConfigStore(blocked)
	if err := blockedStore.Save(config); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(blocked.Cache, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(blocked.Cache, "requirement-metadata"), []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	writeRequirementCacheV2Fixture(t, fixturePath, map[string]string{"calls": callsPath, "mode": ""})
	badService := NewService(blocked, blockedStore, nil)
	payload["force"] = true
	value, err = badService.Execute(ctx, "requirements.metadata", payload)
	if err != nil {
		t.Fatal(err)
	}
	if value.(RequirementMetadataReadResult).CacheError == "" {
		t.Fatal("cache failure was reported as success")
	}
}

const requirementRefreshFixtureBranches = `  if ($arguments.Length -ge 2 -and $arguments[0] -eq 'workitem' -and $arguments[1] -eq 'meta-fields') {
    @{list=@(@{field_name='Duration';field_type='schedule';field_key='duration'});pagination=@{page_num=1;has_more=$false}} | ConvertTo-Json -Depth 12 -Compress; exit 0
  }
  if ($arguments.Length -ge 2 -and $arguments[0] -eq 'workflow' -and $arguments[1] -eq 'get-node') {
    @{list=@();pagination=@{page_num=1;has_more=$false}} | ConvertTo-Json -Depth 12 -Compress; exit 0
  }
  if ($arguments.Length -ge 2 -and $arguments[0] -eq 'workitem' -and $arguments[1] -eq 'get' -and $id -eq '900') {
    @{work_item_fields=@(@{key='duration';value=@{start_time=@{timestamp=100};end_time=@{timestamp=200}}})} | ConvertTo-Json -Depth 12 -Compress; exit 0
  }`
