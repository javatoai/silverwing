package desktop

import (
	"slices"
	"strings"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"
)

func TestProductionWritesUpdateVisibleStateWithoutFollowupReads(t *testing.T) {
	for _, kind := range []string{"production.create", "production.merge-main", "production.merge-release", "production.merge", "production.publish"} {
		t.Run(kind, func(t *testing.T) {
			s, v, b, backend, window := productionRequestFixture(t)
			v.Preview.MainInRelease = true
			if kind == "production.merge" {
				// Feature merges must not replace a newer observed main SHA with
				// the older baseline saved in this batch.
				v.Preview.MainSHA = strings.Repeat("e", 40)
			}
			observedMain := v.Preview.MainSHA
			s.productionRequestFor(v, application.Action{Kind: kind, Payload: map[string]any{"batchId": b.ID, "confirmed": true}}, "")
			productionTestCall(t, backend)
			switch kind {
			case "production.merge-main":
				b.MainSHA = strings.Repeat("d", 40)
			case "production.create":
				b.ReleaseSHA = b.MainSHA
			case "production.merge-release", "production.merge":
				b.ReleaseSHA = strings.Repeat("f", 40)
			case "production.publish":
				b.Tags = []production.Tag{{Name: "old-deleted-tag"}, {Name: "2.3.44", Commit: b.ReleaseSHA, Status: domain.GenbuTagProbeStatus{Build: domain.GenbuSuccess}}}
			}
			b.Message = "推送已完成"
			backend.replies <- productionTestAnswer{result: application.ActionResult{Message: b.Message, Data: production.Report{Batch: &b}}}
			productionTestUpdate(t, window)
			if v.Busy || len(backend.calls) != 0 || v.Message != b.Message || v.Preview.ReleaseSHA != b.ReleaseSHA {
				t.Fatal("successful push did not update the visible result immediately")
			}
			switch kind {
			case "production.merge-main":
				if !v.Preview.ProductionInMain || v.Preview.MainInRelease {
					t.Fatal("main merge left stale release inclusion")
				}
			case "production.create", "production.merge-release":
				if !v.Preview.ProductionInMain || !v.Preview.MainInRelease {
					t.Fatal("successful synchronization lost inclusion state")
				}
			case "production.merge":
				if v.Preview.MainSHA != observedMain || !v.Preview.MainInRelease {
					t.Fatal("feature merge replaced unrelated observed state")
				}
			case "production.publish":
				if v.Preview.Tags[0] != "2.3.44" || slices.Contains(v.Preview.Tags, "old-deleted-tag") {
					t.Fatal("Tag receipt restored deleted history or lost newest display order")
				}
			}
		})
	}
}

func TestProductionTagPreparationPreservesKnownDisplayOrder(t *testing.T) {
	s, v, b, backend, window := productionRequestFixture(t)
	v.Preview.Tags = []string{"2.3.43", "1.0.0"}
	s.productionRequestFor(v, application.Action{Kind: "production.prepare-tag", Payload: map[string]any{"batchId": b.ID}}, "")
	productionTestCall(t, backend)
	fresh := *v.Preview
	fresh.Tags = []string{"1.0.0", "2.3.42", "2.3.43"}
	backend.replies <- productionTestAnswer{result: application.ActionResult{Data: production.Report{Batch: &b, Preview: &fresh}}}
	productionTestUpdate(t, window)
	if !slices.Equal(v.Preview.Tags, []string{"2.3.43", "1.0.0", "2.3.42"}) || v.Busy || len(backend.calls) != 0 {
		t.Fatal("lightweight observation changed the known Tag date order or started publishing")
	}
}
