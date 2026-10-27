//go:build windows

package integrations

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func TestRequirementListProgressEstimatesPreserveDegradedData(t *testing.T) {
	for _, tc := range []struct {
		mode                          string
		items, estimates, workflows   int
		completedQueries, queryErrors int
	}{
		{mode: "workflow-failure", items: 1, workflows: 1, completedQueries: 4},
		{mode: "mixed", items: 2, estimates: 1, workflows: 2, completedQueries: 4},
		{mode: "all-skipped", items: 2, completedQueries: 4},
		{mode: "user-unavailable", items: 1, completedQueries: 4},
		{mode: "invalid-estimate", items: 1, workflows: 1, completedQueries: 4},
		{mode: "incomplete-type", items: 1, completedQueries: 3, queryErrors: 1},
		{mode: "empty", completedQueries: 4},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			root := t.TempDir()
			cli, calls := filepath.Join(root, "meegle.ps1"), filepath.Join(root, "calls.log")
			t.Setenv("SILVERWING_LIST_PROGRESS_CALLS", calls)
			t.Setenv("SILVERWING_LIST_PROGRESS_MODE", tc.mode)
			script := requirementEstimateProgressScript(t, tc.mode)
			if err := os.WriteFile(cli, append([]byte{0xef, 0xbb, 0xbf}, []byte(script)...), 0600); err != nil {
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
			var estimates []RequirementListProgress
			payload := map[string]any{"sprintKey": "p:900"}
			value, err := s.ReadRequirementListWithProgress(ctx, "requirements.refresh", payload, func(event RequirementListProgress) {
				if event.Phase == "estimates" {
					estimates = append(estimates, event)
				}
			})
			if err != nil {
				t.Fatalf("optional estimate failure became fatal: %v", err)
			}
			result, ok := value.(participatedResult)
			if !ok || len(result.Items) != tc.items || result.CompletedQueries != tc.completedQueries || len(result.Failures) != tc.queryErrors {
				t.Fatalf("progress changed readable list/query results: %+v", value)
			}
			actualEstimates := 0
			for _, item := range result.Items {
				if item.MySprintEstimateDays != nil {
					actualEstimates++
				} else if !strings.Contains(strings.Join(item.MetadataWarnings, " "), "个人估分") {
					t.Fatalf("missing estimate lost its per-item degraded warning: %+v", item)
				}
			}
			if actualEstimates != tc.estimates || len(estimates) == 0 {
				t.Fatalf("unexpected estimates/result events: result=%+v events=%+v", result, estimates)
			}
			last := estimates[len(estimates)-1]
			if last.Completed != actualEstimates || last.Total != tc.items {
				t.Fatalf("attempted/skipped work was counted as successful: %+v", estimates)
			}
			if tc.items == 0 {
				if last.Status != "done" || last.Completed != 0 || last.Total != 0 {
					t.Fatalf("empty list did not complete the zero-work phase: %+v", last)
				}
			} else if last.Status != "failed" || !strings.Contains(last.Message, "未完成") || !strings.Contains(last.Message, "：") {
				t.Fatalf("incomplete estimates claimed success or lost the explanation: %+v", last)
			}
			for _, event := range estimates {
				if event.Completed > actualEstimates || event.Completed < 0 {
					t.Fatalf("intermediate completion count included a failed read: %+v", estimates)
				}
			}
			if tc.mode == "mixed" && (len(estimates) != 3 || estimates[1].Completed != 0) {
				t.Fatalf("a failed first read advanced the next item's completed count: %+v", estimates)
			}
			log, err := os.ReadFile(calls)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Count(string(log), "workitem query ") != 5 ||
				strings.Count(string(log), "workflow get-node ") != tc.workflows ||
				len(strings.Split(strings.TrimSpace(string(log)), "\n")) != 14+tc.workflows ||
				strings.Contains(string(log), "comment ") || strings.Contains(string(log), "build ") {
				t.Fatalf("progress performed additional CLI reads/writes or failed to skip workflows: %s", log)
			}
			if tc.mode == "workflow-failure" {
				// The observer must preserve the ordinary nonfatal Execute result
				// and perform exactly the same commands as that existing path.
				if err := os.WriteFile(calls, nil, 0600); err != nil {
					t.Fatal(err)
				}
				plain, err := s.Execute(ctx, "requirements.refresh", payload)
				if err != nil {
					t.Fatalf("ordinary Execute made optional estimates fatal: %v", err)
				}
				plainList, ok := plain.(participatedResult)
				if !ok || len(plainList.Items) != 1 || plainList.Items[0].MySprintEstimateDays != nil ||
					len(plainList.Items[0].MetadataWarnings) == 0 || plainList.CompletedQueries != 4 || len(plainList.Failures) != 0 {
					t.Fatalf("ordinary list contract changed: %+v", plain)
				}
				plainLog, err := os.ReadFile(calls)
				if err != nil {
					t.Fatal(err)
				}
				if string(plainLog) != string(log) {
					t.Fatalf("progress changed command count/order:\nwith progress:\n%s\nordinary:\n%s", log, plainLog)
				}
			}
		})
	}
}

func requirementEstimateProgressScript(t *testing.T, mode string) string {
	t.Helper()
	script := listProgressFixtureScript
	replace := func(old, replacement string) {
		t.Helper()
		if strings.Count(script, old) != 1 {
			t.Fatalf("estimate fixture replacement is not unique: %s", old)
		}
		script = strings.Replace(script, old, replacement, 1)
	}
	workflow := `if ($args[0] -eq 'workflow') { '{"list":[],"pagination":{"page_num":1,"has_more":false}}'; exit 0 }`
	oneItem := `[{"work_item_id":"101","name":"Fixture requirement"}]`
	twoItems := `[{"work_item_id":"101","name":"First requirement"},{"work_item_id":"102","name":"Second requirement"}]`
	switch mode {
	case "workflow-failure":
		replace(workflow, `if ($args[0] -eq 'workflow') { [Console]::Error.WriteLine('fixture estimate unavailable'); exit 3 }`)
	case "mixed":
		replace(oneItem, twoItems)
		replace(workflow, `if ($args[0] -eq 'workflow') {
  $id = $args[[Array]::IndexOf($args, '--work-item-id') + 1]
  if ($id -eq '101') { [Console]::Error.WriteLine('fixture estimate unavailable'); exit 3 }
  '{"list":[],"pagination":{"page_num":1,"has_more":false}}'; exit 0
}`)
	case "all-skipped":
		replace(oneItem, twoItems)
		replace(`'{"work_item_fields":[{"key":"duration","value":{"start_time":{"timestamp":1},"end_time":{"timestamp":999999}}}]}'`,
			`'{"work_item_fields":[]}'`)
	case "user-unavailable":
		replace(`if ($args[0] -eq 'user') { '{"user_key":"fixture-user"}'; exit 0 }`,
			`if ($args[0] -eq 'user') { [Console]::Error.WriteLine('fixture current user unavailable'); exit 3 }`)
	case "invalid-estimate":
		replace(workflow, `if ($args[0] -eq 'workflow') { '{"list":[{"basic":{"node_key":"node"},"sub_tasks":[]}],"pagination":{"page_num":1,"has_more":false}}'; exit 0 }`)
	case "incomplete-type":
		replace(oneItem, `[{"work_item_id":"101"}]`)
	case "empty":
	default:
		t.Fatalf("unknown estimate fixture mode %q", mode)
	}
	return script
}
