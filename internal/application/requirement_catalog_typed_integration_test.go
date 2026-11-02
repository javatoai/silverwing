package application

import (
	"reflect"
	"testing"
)

func TestRequirementListTypedBoundaryPreservesSprintDirectoryMetadata(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     map[string]any
		present bool
		cache   bool
		stamp   int64
	}{
		{"cached", map[string]any{"sprints": []any{map[string]any{"id": "old"}}, "fromCache": true, "sprintsFetchedAt": int64(100)}, true, true, 100},
		{"authoritative-empty", map[string]any{"sprints": nil, "fromCache": false, "sprintsFetchedAt": int64(300)}, true, false, 300},
		{"incomplete", map[string]any{"sprints": nil, "fromCache": false, "sprintsFetchedAt": int64(0)}, true, false, 0},
		{"markerless", map[string]any{"items": nil}, false, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := requirementListResult(tc.raw)
			if err != nil {
				t.Fatal(err)
			}
			got := result.Data
			if (got.FromCache != nil) != tc.present || (got.SprintsFetchedAt != nil) != tc.present {
				t.Fatalf("lost provenance presence: %+v", got)
			}
			if tc.present && (*got.FromCache != tc.cache || *got.SprintsFetchedAt != tc.stamp) {
				t.Fatalf("lost provenance values: %+v", got)
			}
			roundTrip, err := ResultData[RequirementList](result.ToActionResult())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, roundTrip) {
				t.Fatalf("typed dispatch changed metadata: %+v != %+v", got, roundTrip)
			}
		})
	}
}
