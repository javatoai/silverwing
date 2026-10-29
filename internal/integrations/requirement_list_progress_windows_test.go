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

func TestRequirementListProgressRealQueryStages(t *testing.T) {
	for _, mode := range []string{"normal", "empty", "partial", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			cli := filepath.Join(root, "meegle.ps1")
			calls := filepath.Join(root, "calls.log")
			t.Setenv("SILVERWING_LIST_PROGRESS_CALLS", calls)
			t.Setenv("SILVERWING_LIST_PROGRESS_MODE", mode)
			if err := os.WriteFile(cli, append([]byte{0xef, 0xbb, 0xbf}, []byte(listProgressFixtureScript)...), 0600); err != nil {
				t.Fatal(err)
			}
			paths := storage.NewPaths(filepath.Join(root, "home"))
			store := storage.NewConfigStore(paths)
			config := domain.DefaultAppConfig()
			config.MeegleExecutablePath = &cli
			config.MeegleProjects = []domain.MeegleProjectConfig{{ProjectKey: "p", SimpleName: "fixture"}}
			if err := store.Save(config); err != nil {
				t.Fatal(err)
			}
			s := NewService(paths, store, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			events := []RequirementListProgress{}
			result, err := s.ReadRequirementListWithProgress(ctx, "requirements.refresh", map[string]any{"sprintKey": "p:900"}, func(event RequirementListProgress) {
				events = append(events, event)
				if mode == "cancel" && event.WorkItemType == "bug" && event.Status == "running" {
					cancel()
				}
			})
			if mode == "cancel" {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation was ignored: %v", err)
				}
			} else {
				if err != nil {
					t.Fatal(err)
				}
				list, ok := result.(participatedResult)
				if !ok {
					t.Fatalf("progress changed the result type: %T", result)
				}
				wantItems, wantQueries, wantFailures := 1, 4, 0
				if mode == "empty" {
					wantItems = 0
				}
				if mode == "partial" {
					wantQueries, wantFailures = 3, 1
				}
				if len(list.Items) != wantItems || list.CompletedQueries != wantQueries || len(list.Failures) != wantFailures {
					t.Fatalf("unexpected final result: %+v", list)
				}
				if len(events) == 0 || events[len(events)-1].Phase != "estimates" || events[len(events)-1].Completed != wantItems {
					t.Fatalf("estimates did not report real completed work: %+v", events)
				}
			}
			types := map[string]string{}
			for i, event := range events {
				if event.Sequence != uint64(i+1) || event.Completed < 0 || event.Total > 0 && event.Completed > event.Total {
					t.Fatalf("invalid progress sequence or count: %+v", event)
				}
				if event.WorkItemType != "" && event.Status != "running" {
					types[event.WorkItemType] = event.Status
				}
				if event.Phase == "apply" {
					t.Fatal("integration claimed UI application was finished")
				}
			}
			if mode != "cancel" && len(types) != 4 || mode == "partial" && types["bug"] != "failed" {
				t.Fatalf("missing per-type results: %+v", types)
			}
			log, err := os.ReadFile(calls)
			if err != nil {
				t.Fatal(err)
			}
			// A callback must not add a second list read or load body/comments.
			wantQueries := 5 // one Sprint query and four work-item types
			if mode == "cancel" {
				wantQueries = 3
			}
			if strings.Count(string(log), "workitem query ") != wantQueries || strings.Contains(string(log), "comment ") || strings.Contains(string(log), "build ") {
				t.Fatalf("progress performed additional remote work: %s", log)
			}
		})
	}
}

const listProgressFixtureScript = `$ErrorActionPreference = 'Stop'
for ($logAttempt = 0; ; $logAttempt++) {
  try { [IO.File]::AppendAllText($env:SILVERWING_LIST_PROGRESS_CALLS, (($args -join ' ') + [Environment]::NewLine)); break }
  catch [IO.IOException] { if ($logAttempt -ge 100) { throw }; Start-Sleep -Milliseconds 10 }
}
$mode = $env:SILVERWING_LIST_PROGRESS_MODE
if ($args[0] -eq '--version') { 'fixture-1'; exit 0 }
if ($args[0] -eq 'auth') { '{"authenticated":true}'; exit 0 }
if ($args[0] -eq 'user') { '{"user_key":"fixture-user"}'; exit 0 }
if ($args[0] -eq 'workflow') { '{"list":[],"pagination":{"page_num":1,"has_more":false}}'; exit 0 }
if ($args[1] -eq 'meta-fields') {
  '{"list":[{"field_name":"Duration","field_type":"schedule","field_key":"duration"}],"pagination":{"page_num":1,"has_more":false}}'; exit 0
}
if ($args[1] -eq 'get') {
  '{"work_item_fields":[{"key":"duration","value":{"start_time":{"timestamp":1},"end_time":{"timestamp":999999}}}]}'; exit 0
}
if ($args[1] -eq 'meta-roles') {
  '{"list":[{"role_id":"dev","role_name":"Dev Owner"}],"pagination":{"page_num":1,"has_more":false}}'; exit 0
}
if ($args[1] -eq 'query') {
  $query = $args[[Array]::IndexOf($args, '--mql') + 1]
  if ($query.Contains(('.' + [char]96 + 'Sprint' + [char]96))) {
    '{"data":{"1":[{"work_item_id":"900","name":"Fixture Sprint","work_item_status":"\u8fdb\u884c\u4e2d"}]}}'; exit 0
  }
  if ($mode -eq 'partial' -and $query.Contains(([char]96 + 'Bug' + [char]96))) { [Console]::Error.WriteLine('fixture bug query failure'); exit 3 }
  if ($mode -ne 'empty' -and $query.Contains('User Story')) {
    '{"data":{"1":[{"work_item_id":"101","name":"Fixture requirement"}]}}'; exit 0
  }
  '{"data":{},"list":null}'; exit 0
}
[Console]::Error.WriteLine('unexpected command'); exit 2
`
