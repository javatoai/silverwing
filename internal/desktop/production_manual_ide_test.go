package desktop

import (
	"errors"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/production"
)

func TestProductionConflictsOnlyOpenIDEOnExplicitClick(t *testing.T) {
	for _, stage := range []string{"feature", "main", "release-sync"} {
		t.Run(stage, func(t *testing.T) {
			s, v, b, backend, window := productionRequestFixture(t)
			b = productionConflictBatch(b, stage)
			mergeAction := map[string]string{"feature": "production.merge", "main": "production.merge-main", "release-sync": "production.merge-release"}[stage]
			for _, kind := range []string{mergeAction, "production.inspect", "production.resume"} {
				s.productionRequestFor(v, application.Action{Kind: kind, Payload: map[string]any{"batchId": b.ID}}, "")
				productionTestCall(t, backend)
				s.page = "tasks"
				answer := productionTestAnswer{result: application.ActionResult{Data: production.Report{Batch: &b}}}
				if kind != "production.inspect" {
					answer.err = errors.New(b.Message)
				}
				backend.replies <- answer
				productionTestUpdate(t, window)
				if v.Busy || len(backend.calls) != 0 || s.productionBatch(v).Intent == nil {
					t.Fatal("conflict launched an IDE or lost its recovery record")
				}
				s.page = "production"
			}
			// Rendering a restored conflict and switching back must also be passive.
			tester := productionIterationTester(s, 960, false)
			if v.Busy || len(backend.calls) != 0 {
				t.Fatal("displaying a restored conflict launched an IDE")
			}
			productionRevealControl(t, tester, "打开 IDE")
			if err := tester.Click("打开 IDE"); err != nil {
				t.Fatal(err)
			}
			if call := productionTestCall(t, backend); call.Kind != "production.open-ide" || call.Payload["batchId"] != b.ID {
				t.Fatalf("manual IDE action lost the current batch: %+v", call)
			}
			backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batch: &b}}}
			productionTestUpdate(t, window)
			if v.Busy || len(backend.calls) != 0 {
				t.Fatal("manual IDE completion opened another IDE")
			}
		})
	}
}
