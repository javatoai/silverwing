package integrations

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func ownerEstimateFixture(t *testing.T, source string) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(source))
	decoder.UseNumber()
	var node map[string]any
	if err := decoder.Decode(&node); err != nil {
		t.Fatal(err)
	}
	return node
}

func ownerEstimateSprint(t *testing.T) sprintRange {
	t.Helper()
	start, err := time.Parse(time.RFC3339, "2026-10-26T00:00:00+08:00")
	if err != nil {
		t.Fatal(err)
	}
	end, _ := time.Parse(time.RFC3339, "2026-11-06T23:59:59+08:00")
	return sprintRange{Start: start.UnixMilli(), End: end.UnixMilli()}
}

const sharedOwnerEstimateFixture = `{
  "basic":{"node_key":"develop"},
  "assignees":{"owners":[{"user_key":"me"},{"user_key":"other"}]},
  "schedule":{"points":2,"estimate_start_time":1792944000000,"estimate_finish_time":1793980799999},
  "assignee_schedule_list":[
    {"assignee":{"user_key":"me"},"schedule_info":{"points":1,"estimate_start_time":1792944000000,"estimate_finish_time":1793980799999}},
    {"assignee":{"user_key":"other"},"schedule_info":{"points":1,"estimate_start_time":1792944000000,"estimate_finish_time":1793980799999}}
  ],
  "sub_tasks":[{"sub_task_id":"shared","owner":"[{\"username\":\"me\"},{\"username\":\"other\"}]","points":2,
    "estimate_start_date":"2026-10-26T00:00:00+08:00","estimate_end_date":"2026-11-06T23:59:59+08:00","is_finished":false}]
}`

func TestPersonalEstimateFullOwnedPoints(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
		want string
	}{
		{name: "shared child uses all2 instead of personal1", want: "2"},
		{name: "shared node without children uses full total", edit: func(n map[string]any) { n["sub_tasks"] = []any{} }, want: "2"},
		{name: "missing node total retains individual schedule", edit: func(n map[string]any) { n["sub_tasks"] = []any{}; n["schedule"].(map[string]any)["points"] = nil }, want: "1"},
		{name: "shared child needs no personal allocation", edit: func(n map[string]any) { delete(n, "assignee_schedule_list") }, want: "2"},
		{name: "shared node needs no personal allocation", edit: func(n map[string]any) { n["sub_tasks"] = []any{}; delete(n, "assignee_schedule_list") }, want: "2"},
		{name: "other owners are excluded", edit: func(n map[string]any) {
			n["sub_tasks"].([]any)[0].(map[string]any)["owner"] = `[{"username":"other"},{"username":"someone"}]`
		}, want: "0"},
		{name: "outside sprint excluded", edit: func(n map[string]any) {
			child := n["sub_tasks"].([]any)[0].(map[string]any)
			child["estimate_start_date"], child["estimate_end_date"] = "2026-11-07T00:00:00+08:00", "2026-11-08T00:00:00+08:00"
		}, want: "0"},
		{name: "duplicate owners do not multiply points", edit: func(n map[string]any) {
			n["sub_tasks"].([]any)[0].(map[string]any)["owner"] = `[{"username":"me"},{"username":"me"}]`
		}, want: "2"},
		{name: "decimal retained exactly", edit: func(n map[string]any) { n["sub_tasks"].([]any)[0].(map[string]any)["points"] = json.Number("0.125") }, want: "0.125"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := ownerEstimateFixture(t, sharedOwnerEstimateFixture)
			if tc.edit != nil {
				tc.edit(node)
			}
			got, err := personalEstimate([]map[string]any{node}, "me", ownerEstimateSprint(t))
			if err != nil || got.String() != tc.want {
				t.Fatalf("full-owner rule: got=%v error=%v want=%s", got, err, tc.want)
			}
		})
	}
}

func TestPersonalEstimateOwnRecordsSumOnceAndRejectConflicts(t *testing.T) {
	node := ownerEstimateFixture(t, sharedOwnerEstimateFixture)
	child := node["sub_tasks"].([]any)[0].(map[string]any)
	node["sub_tasks"] = []any{child, child}
	got, err := personalEstimate([]map[string]any{node, node}, "me", ownerEstimateSprint(t))
	if err != nil || got.String() != "2" {
		t.Fatalf("duplicate node/record counted twice: %v %v", got, err)
	}
	conflict := make(map[string]any)
	for key, value := range child {
		conflict[key] = value
	}
	conflict["points"] = json.Number("3")
	node["sub_tasks"] = []any{child, conflict}
	if _, err := personalEstimate([]map[string]any{node}, "me", ownerEstimateSprint(t)); err == nil {
		t.Fatal("conflicting duplicate estimate became a known total")
	}
}

func TestPersonalEstimateMixedOwnersAndCompletedZero(t *testing.T) {
	for _, tc := range []struct {
		name   string
		shared bool
		want   string
	}{
		{name: "shared totals never replaced by divided rollup", shared: true, want: "2"},
		{name: "exclusive completed zero keeps existing compensation", want: "3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := ownerEstimateFixture(t, sharedOwnerEstimateFixture)
			node["assignee_schedule_list"].([]any)[0].(map[string]any)["schedule_info"].(map[string]any)["points"] = json.Number("3")
			first := node["sub_tasks"].([]any)[0].(map[string]any)
			if !tc.shared {
				first["owner"] = `[{"username":"me"}]`
			}
			zero, other := make(map[string]any), make(map[string]any)
			for key, value := range first {
				zero[key], other[key] = value, value
			}
			zero["sub_task_id"], zero["points"], zero["is_finished"] = "done", json.Number("0"), true
			other["sub_task_id"], other["points"], other["owner"] = "other", json.Number("8"), `[{"username":"other"}]`
			node["sub_tasks"] = []any{first, zero, other}
			got, err := personalEstimate([]map[string]any{node}, "me", ownerEstimateSprint(t))
			if err != nil || got.String() != tc.want {
				t.Fatalf("got=%v error=%v want=%s", got, err, tc.want)
			}
		})
	}
}

func TestPersonalEstimateBoundariesAndInvalidData(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
		want string
		err  bool
	}{
		{name: "inclusive sprint start", edit: func(c map[string]any) {
			c["estimate_start_date"], c["estimate_end_date"] = "2026-10-25T00:00:00+08:00", "2026-10-26T00:00:00+08:00"
		}, want: "2"},
		{name: "null schedule remains unscheduled", edit: func(c map[string]any) { c["estimate_start_date"] = nil }, want: "0"},
		{name: "zero is known", edit: func(c map[string]any) { c["points"] = json.Number("0") }, want: "0"},
		{name: "negative not a known total", edit: func(c map[string]any) { c["points"] = json.Number("-1") }, err: true},
		{name: "invalid owner not silently ignored", edit: func(c map[string]any) { c["owner"] = "bad json" }, err: true},
		{name: "reversed dates not known", edit: func(c map[string]any) { c["estimate_start_date"] = "2027-01-01T00:00:00+08:00" }, err: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			node := ownerEstimateFixture(t, sharedOwnerEstimateFixture)
			tc.edit(node["sub_tasks"].([]any)[0].(map[string]any))
			got, err := personalEstimate([]map[string]any{node}, "me", ownerEstimateSprint(t))
			if (err != nil) != tc.err || err == nil && got.String() != tc.want {
				t.Fatalf("got=%v error=%v want=%s err=%t", got, err, tc.want, tc.err)
			}
		})
	}
}
