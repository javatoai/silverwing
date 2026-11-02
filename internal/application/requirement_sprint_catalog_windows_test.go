//go:build windows

package application

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestRequirementSprintCatalogDispatchUsesConfirmedIdentity(t *testing.T) {
	t.Setenv("USERPROFILE", t.TempDir())
	paths := storage.NewPaths(t.TempDir())
	cli := filepath.Join(paths.Home, "catalog-fixture.ps1")
	calls := filepath.Join(paths.Home, "catalog-calls.log")
	t.Setenv("SILVERWING_CATALOG_DISPATCH_CALLS", calls)
	const script = `$ErrorActionPreference='Stop'
[IO.File]::AppendAllText($env:SILVERWING_CATALOG_DISPATCH_CALLS, (($args -join ' ') + [Environment]::NewLine))
if ($args[0] -eq 'config') { 'fixture-profile'; exit 0 }
if ($args[0] -eq 'auth') { '{"authenticated":true,"host":"project.feishu.cn"}'; exit 0 }
if ($args[0] -eq 'user') { '{"user_key":"fixture-account"}'; exit 0 }
if ($args[0] -eq 'workitem' -and $args[1] -eq 'query') {
  '{"data":{"1":[{"work_item_id":"900","name":"Current Sprint","work_item_status":"\u8fdb\u884c\u4e2d"}]}}'; exit 0
}
throw 'unexpected catalog command'
`
	if err := os.WriteFile(cli, append([]byte{0xef, 0xbb, 0xbf}, []byte(script)...), 0600); err != nil {
		t.Fatal(err)
	}
	config := domain.DefaultAppConfig()
	config.TaskRoot = &paths.Tasks
	config.MeegleExecutablePath = &cli
	config.MeegleProjects = []domain.MeegleProjectConfig{{ProjectKey: "project-a", SimpleName: "fixture"}}
	if err := storage.NewConfigStore(paths).Save(config); err != nil {
		t.Fatal(err)
	}
	backend, err := New(paths.Home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = backend.Integrations.CloseRequirementDocuments(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	identity, err := backend.RequirementIdentity(ctx)
	if err != nil || identity.State != RequirementIdentityAuthenticated || identity.Identity == "" {
		t.Fatal("fixture identity was not confirmed", identity.State, err)
	}
	result, err := backend.Dispatch(ctx, Action{Kind: "requirements.sprints-refresh", Payload: map[string]any{"expectedIdentity": identity.Identity}})
	if err != nil {
		t.Fatal("catalog action did not cross the application dispatch boundary", err)
	}
	encoded, err := json.Marshal(result.Data)
	var catalog struct {
		Sprints []struct {
			ProjectKey string `json:"projectKey"`
			ID         string `json:"id"`
			Title      string `json:"title"`
		} `json:"sprints"`
		FetchedAt int64 `json:"fetchedAt"`
	}
	if err != nil || json.Unmarshal(encoded, &catalog) != nil || catalog.FetchedAt <= 0 || len(catalog.Sprints) != 1 ||
		catalog.Sprints[0].ProjectKey != "project-a" || catalog.Sprints[0].ID != "900" || catalog.Sprints[0].Title != "Current Sprint" {
		t.Fatal("catalog DTO lost fields at the application boundary", string(encoded), err)
	}
	log, err := os.ReadFile(calls)
	if err != nil || strings.Count(string(log), "workitem query ") != 1 || strings.Contains(string(log), "comment ") {
		t.Fatal("catalog refresh did more than one directory query", string(log), err)
	}
	if _, err := backend.Dispatch(ctx, Action{Kind: "requirements.sprints-refresh", Payload: map[string]any{"expectedIdentity": "raw-user-key"}}); err == nil {
		t.Fatal("catalog dispatch accepted an unconfirmed identity")
	}
}
