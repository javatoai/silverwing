package desktop

import (
	"reflect"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func TestTagHistoryRefreshResponse(t *testing.T) {
	newRecord := func(id, taskID string) application.TagRecord {
		return application.TagRecord{ID: id, TaskID: taskID, GenbuStatus: domain.GenbuTagProbeStatus{
			Build: domain.GenbuInitial, UAT: domain.GenbuInitial, Production: domain.GenbuInitial,
		}}
	}
	makeState := func() (*app, application.Action) {
		s := &app{configJSON: `{"taskRoot":"D:\\fixture\\tasks"}`, inputs: map[string]*string{},
			tasks: []task{{id: "a", path: `D:\fixture\tasks\a`, createdAt: "created-a"}}}
		s.snapshot.Home = `D:\fixture\home`
		s.snapshot.TagHistory = []application.TagRecord{{ID: "old-a", TaskID: "a"}, {ID: "kept-b", TaskID: "b"}}
		state := s.ensureTagHistoryView()
		return s, application.Action{Kind: "tag.history.refresh", TaskID: "a", Payload: tagHistoryScopePayload(state)}
	}
	for _, asMap := range []bool{false, true} {
		s, action := makeState()
		var data any = application.TagHistoryRefreshResult{Records: []application.TagRecord{newRecord("new-a", "a")}, StatusError: "remote probe failed"}
		if asMap {
			data = map[string]any{"records": []application.TagRecord{newRecord("new-a", "a")}, "statusError": "remote probe failed"}
		}
		s.applyDeliveryActionResult(action, application.ActionResult{Data: data})
		want := []application.TagRecord{{ID: "kept-b", TaskID: "b"}, newRecord("new-a", "a")}
		if !reflect.DeepEqual(s.snapshot.TagHistory, want) || *s.input(tagGenbuTaskErrorKey("a"), "") != "remote probe failed" {
			t.Fatalf("map=%t: local history or task isolation lost: %#v", asMap, s.snapshot.TagHistory)
		}
	}
	t.Run("invalid-response-keeps-history", func(t *testing.T) {
		s, action := makeState()
		before := append([]application.TagRecord(nil), s.snapshot.TagHistory...)
		s.applyDeliveryActionResult(action, application.ActionResult{Data: map[string]any{"statusError": "missing records"}})
		if !reflect.DeepEqual(before, s.snapshot.TagHistory) || s.tagHistory.Error == "" {
			t.Fatal("a missing list cleared local history or hid the decoding error")
		}
	})
	t.Run("cross-task-response-keeps-history", func(t *testing.T) {
		s, action := makeState()
		before := append([]application.TagRecord(nil), s.snapshot.TagHistory...)
		s.applyDeliveryActionResult(action, application.ActionResult{Data: application.TagHistoryRefreshResult{Records: []application.TagRecord{newRecord("wrong", "b")}}})
		if !reflect.DeepEqual(before, s.snapshot.TagHistory) || s.tagHistory.Error == "" {
			t.Fatal("a response from another task changed local history")
		}
	})
	t.Run("retired-task-result-is-ignored", func(t *testing.T) {
		s, action := makeState()
		action.Payload["tagHistoryTaskCreatedAt"] = "older-task"
		before := append([]application.TagRecord(nil), s.snapshot.TagHistory...)
		s.applyDeliveryActionResult(action, application.ActionResult{Data: application.TagHistoryRefreshResult{Records: []application.TagRecord{}}})
		if !reflect.DeepEqual(before, s.snapshot.TagHistory) || s.tagHistory.Error != "" {
			t.Fatal("a retired task response changed local history or current errors")
		}
	})
}
