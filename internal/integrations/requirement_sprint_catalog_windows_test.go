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

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func sprintCatalogWindowsService(t *testing.T, projects []domain.MeegleProjectConfig) (*Service, domain.AppConfig, string, string, string) {
	t.Helper()
	root := t.TempDir()
	fixturePath, callsPath, cliPath := filepath.Join(root, "fixture.json"), filepath.Join(root, "calls.log"), filepath.Join(root, "meegle.ps1")
	writeRequirementCacheV2Fixture(t, fixturePath, map[string]string{"calls": callsPath, "mode": "new", "account": "account-a"})
	t.Setenv("SILVERWING_SPRINT_CATALOG_FIXTURE", fixturePath)
	if err := os.WriteFile(cliPath, append([]byte{0xef, 0xbb, 0xbf}, []byte(requirementSprintCatalogFixtureScript)...), 0600); err != nil {
		t.Fatal(err)
	}
	paths := storage.NewPaths(filepath.Join(root, "home"))
	store := storage.NewConfigStore(paths)
	config := domain.DefaultAppConfig()
	config.MeegleExecutablePath = &cliPath
	config.MeegleProjects = projects
	if err := store.Save(config); err != nil {
		t.Fatal(err)
	}
	config, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	s := NewService(paths, store, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, token, err := s.RequirementIdentity(ctx, config)
	if err != nil || token == "" {
		t.Fatalf("confirm real fixture CLI identity: token=%q err=%v", token, err)
	}
	return s, config, token, fixturePath, callsPath
}

func TestRequirementSprintCatalogLiveListPersistsDespiteLostSelectionOrItemFailures(t *testing.T) {
	for _, mode := range []string{"selected-vanished", "items-failure", "new"} {
		t.Run(mode, func(t *testing.T) {
			s, config, token, fixturePath, callsPath := sprintCatalogWindowsService(t, []domain.MeegleProjectConfig{{ProjectKey: "p", SimpleName: "space"}})
			writeRequirementCacheV2Fixture(t, fixturePath, map[string]string{"calls": callsPath, "mode": mode, "account": "account-a"})
			keys := sprintCatalogTestKeys(t, config, token, nil)
			oldSelection := "p:1"
			oldChoices := []sprintItem{{ProjectKey: "p", ID: "1", Title: "Old", Status: "进行中"}, {ProjectKey: "p", ID: "2", Title: "Current", Status: "进行中"}}
			old := participatedResult{Items: []domain.ParticipatedWorkItem{{ID: "old-item"}}, Sprints: oldChoices, SelectedSprintKey: &oldSelection, FetchedAt: requirementUnixMillis() - time.Hour.Milliseconds()}
			defaultKey, _ := keys.requestedKey()
			if err := s.writeRequirementListCache(defaultKey, old); err != nil {
				t.Fatal(err)
			}
			oldSprintKey, _ := keys.key(&oldSelection)
			if err := s.writeRequirementListCache(oldSprintKey, old); err != nil {
				t.Fatal(err)
			}
			selected := "p:2"
			if mode == "selected-vanished" {
				selected = oldSelection
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			value, err := s.Execute(ctx, "requirements.refresh", map[string]any{"expectedIdentity": token, "sprintKey": selected})
			if err != nil {
				t.Fatal(err)
			}
			got := value.(participatedResult)
			if got.FromCache || got.SprintsFetchedAt <= 0 || len(got.Sprints) != 1 || got.Sprints[0].Key() != "p:2" {
				t.Fatalf("complete live catalog lost its DTO provenance: %+v", got)
			}
			if mode != "new" && len(got.Failures) == 0 {
				t.Fatalf("fixture failure was not preserved: %+v", got)
			}
			restarted := NewService(s.paths, s.config, nil)
			if _, _, err := restarted.RequirementIdentity(ctx, config); err != nil {
				t.Fatal(err)
			}
			before := requirementCacheV2FixtureCalls(t, callsPath)
			for _, payload := range []map[string]any{{"expectedIdentity": token}, {"expectedIdentity": token, "sprintKey": oldSelection}} {
				value, err := restarted.Execute(ctx, "requirements.list", payload)
				if err != nil {
					t.Fatal(err)
				}
				cached := value.(participatedResult)
				if !cached.FromCache || len(cached.Sprints) != 1 || cached.Sprints[0].Key() != "p:2" || cached.SprintsFetchedAt != got.SprintsFetchedAt || len(cached.Items) != 0 || cached.SelectedSprintKey != nil {
					t.Fatalf("restart resurrected removed choices/items after %s: %+v", mode, cached)
				}
			}
			after := requirementCacheV2FixtureCalls(t, callsPath)
			if len(after) != len(before) {
				t.Fatalf("cached list unexpectedly queried the CLI: before=%v after=%v", before, after)
			}
		})
	}
}

func TestRequirementSprintCatalogRefreshCompletePartialFailureAndEmpty(t *testing.T) {
	projects := []domain.MeegleProjectConfig{{ProjectKey: "p", SimpleName: "space"}, {ProjectKey: "q", SimpleName: "second"}}
	s, config, token, fixturePath, callsPath := sprintCatalogWindowsService(t, projects)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	keys := sprintCatalogTestKeys(t, config, token, nil)
	key, _ := keys.sprintCatalogKey()
	var lastFetchedAt int64
	for _, mode := range []string{"new", "partial", "failure", "empty"} {
		writeRequirementCacheV2Fixture(t, fixturePath, map[string]string{"calls": callsPath, "mode": mode, "account": "account-a"})
		before := requirementCacheV2FixtureCalls(t, callsPath)
		value, err := s.Execute(ctx, "requirements.sprints-refresh", map[string]any{"expectedIdentity": token})
		if mode == "partial" || mode == "failure" {
			if err == nil || value != nil {
				t.Fatalf("incomplete catalog reported success: mode=%s value=%+v err=%v", mode, value, err)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			got := value.(requirementSprintCatalogResult)
			want := 2
			if mode == "empty" {
				want = 0
			}
			if got.Sprints == nil || len(got.Sprints) != want || got.FetchedAt <= 0 {
				t.Fatalf("complete catalog response: mode=%s got=%+v", mode, got)
			}
			data, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			var dto map[string]any
			if err := json.Unmarshal(data, &dto); err != nil || len(dto) != 2 || dto["sprints"] == nil || dto["fetchedAt"] == nil {
				t.Fatalf("directory action changed its lower-camel DTO: %s %v", data, err)
			}
			lastFetchedAt = got.FetchedAt
		}
		saved, err := s.readRequirementSprintCatalogCache(key)
		want := 2
		if mode == "empty" {
			want = 0
		}
		if err != nil || saved == nil || len(saved.Sprints) != want || saved.FetchedAt != lastFetchedAt {
			t.Fatalf("catalog mutation after %s: saved=%+v err=%v", mode, saved, err)
		}
		after := requirementCacheV2FixtureCalls(t, callsPath)
		queries := 0
		for _, call := range after[len(before):] {
			if strings.HasPrefix(call, "workitem query ") {
				queries++
				if !strings.Contains(call, "`Sprint`") {
					t.Fatalf("directory-only refresh read work items: %s", call)
				}
			}
			if strings.Contains(call, "meta-fields") || strings.Contains(call, "meta-roles") || strings.HasPrefix(call, "workflow ") || strings.HasPrefix(call, "comment ") {
				t.Fatalf("directory-only refresh performed item enrichment: %s", call)
			}
		}
		if queries != 2 {
			t.Fatalf("configured project catalogs were not each queried once: mode=%s query count=%d", mode, queries)
		}
	}
}

func TestRequirementSprintCatalogLiveIdentitySwitchAndCancellationDoNotPersist(t *testing.T) {
	for _, mode := range []string{"switch-account", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			s, _, token, fixturePath, callsPath := sprintCatalogWindowsService(t, []domain.MeegleProjectConfig{{ProjectKey: "p", SimpleName: "space"}})
			writeRequirementCacheV2Fixture(t, fixturePath, map[string]string{"calls": callsPath, "mode": mode, "account": "account-a"})
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			var err error
			payload := map[string]any{"expectedIdentity": token, "sprintKey": "p:2"}
			if mode == "cancel" {
				_, err = s.ReadRequirementListWithProgress(ctx, "requirements.refresh", payload, func(event RequirementListProgress) {
					if event.Phase == "sprints" && event.Status == "done" {
						cancel()
					}
				})
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("catalog-complete cancellation was ignored: %v", err)
				}
			} else {
				_, err = s.Execute(ctx, "requirements.sprints-refresh", payload)
				var changed *RequirementIdentityChangedError
				if !errors.As(err, &changed) {
					t.Fatalf("account switch during catalog read was accepted: %v", err)
				}
			}
			files, err := filepath.Glob(filepath.Join(s.paths.Cache, "requirement-sprint-catalogs", "*.json"))
			if err != nil || len(files) != 0 {
				t.Fatalf("invalidated live catalog reached disk: %v %v", files, err)
			}
		})
	}
}

const requirementSprintCatalogFixtureScript = `$ErrorActionPreference = 'Stop'
$OutputEncoding = [Console]::OutputEncoding = New-Object System.Text.UTF8Encoding($false)
$fixturePath = $env:SILVERWING_SPRINT_CATALOG_FIXTURE
$fixture = Get-Content -Raw -LiteralPath $fixturePath | ConvertFrom-Json
[IO.File]::AppendAllText($fixture.calls, (($args -join ' ') + [Environment]::NewLine))
if ($args[0] -eq '--version') { 'fixture-1'; exit 0 }
if ($args[0] -eq 'config') { 'fixture-profile'; exit 0 }
if ($args[0] -eq 'auth') { '{"authenticated":true,"host":"project.feishu.cn","tenant_key":"fixture-tenant"}'; exit 0 }
if ($args[0] -eq 'user') { @{user_key=$fixture.account; tenant_key='fixture-tenant'} | ConvertTo-Json -Compress; exit 0 }
if ($args[0] -eq 'workflow') { '{"list":[],"pagination":{"page_num":1,"has_more":false}}'; exit 0 }
if ($args[1] -eq 'meta-fields') { '{"list":[{"field_name":"Duration","field_type":"schedule","field_key":"duration"}],"pagination":{"page_num":1,"has_more":false}}'; exit 0 }
if ($args[1] -eq 'meta-roles') { '{"list":[{"role_id":"dev","role_name":"Dev Owner"}],"pagination":{"page_num":1,"has_more":false}}'; exit 0 }
if ($args[1] -eq 'get') { '{"work_item_fields":[{"key":"duration","value":{"start_time":{"timestamp":1},"end_time":{"timestamp":999999}}}]}'; exit 0 }
if ($args[1] -eq 'query') {
  $query = $args[[Array]::IndexOf($args, '--mql') + 1]
  $project = $args[[Array]::IndexOf($args, '--project-key') + 1]
  if ($query.Contains(('.' + [char]96 + 'Sprint' + [char]96))) {
    if ($fixture.mode -eq 'failure' -or ($fixture.mode -eq 'partial' -and $project -eq 'q')) { [Console]::Error.WriteLine('fixture catalog failure'); exit 3 }
    if ($fixture.mode -eq 'switch-account') { $fixture.account='account-b'; $fixture | ConvertTo-Json -Compress | Set-Content -LiteralPath $fixturePath -Encoding UTF8 }
    if ($fixture.mode -eq 'empty') { '{"data":{"1":[]}}'; exit 0 }
    '{"data":{"1":[{"work_item_id":"2","name":"Current Sprint","work_item_status":"\u8fdb\u884c\u4e2d"}]}}'; exit 0
  }
  if ($fixture.mode -eq 'items-failure') { [Console]::Error.WriteLine('fixture item query failure'); exit 3 }
  '{"data":{"1":[]}}'; exit 0
}
[Console]::Error.WriteLine('unexpected command'); exit 2
`
