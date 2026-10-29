package application

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"silverwing-go/internal/domain"
)

func TestRequirementListTypedViewPreservesJSONAndDegradedData(t *testing.T) {
	status := "开发中"
	item := domain.ParticipatedWorkItem{ID: "123", Title: "需求", ProjectKey: "project", ProjectName: "项目", Type: "story", TypeLabel: "需求", URL: "https://project.feishu.cn/demo/userstory/detail/123", Status: &status, Developers: []domain.RequirementPerson{{Name: "开发"}}, QCOwners: []domain.RequirementPerson{{Name: "测试"}}}
	raw := map[string]any{"items": []domain.ParticipatedWorkItem{item}, "failures": []string{"部分项目不可用"}, "completedQueries": 3, "sprints": []RequirementSprint{{ProjectKey: "project", ID: "900", Title: "Sprint"}}, "selectedSprintKey": "project:900", "fetchedAt": int64(100)}
	result, err := requirementListResult(raw)
	if err != nil {
		t.Fatal(err)
	}
	list := result.Data
	if !strings.Contains(result.Message, "部分查询失败") || len(list.Items) != 1 || len(list.Failures) != 1 || list.CompletedQueries != 3 || list.FetchedAt != 100 || list.SelectedSprintKey == nil || *list.SelectedSprintKey != "project:900" {
		t.Fatalf("partial query metadata changed: %+v", result)
	}
	if list.Items[0].Project != item.ProjectName || list.Items[0].Status != status || !reflect.DeepEqual(list.Items[0].Participants, []string{"开发", "测试"}) || !reflect.DeepEqual(list.Items[0].WorkItem, item) {
		t.Fatalf("domain-to-view projection changed: %+v", list.Items[0])
	}
	legacy := result.ToActionResult()
	decoded, err := ResultData[RequirementList](legacy)
	if err != nil || !reflect.DeepEqual(decoded, list) {
		t.Fatalf("native list required another codec: %+v %v", decoded, err)
	}
	encoded, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"items"`, `"failures"`, `"completedQueries"`, `"sprints"`, `"selectedSprintKey"`, `"fetchedAt"`} {
		if !strings.Contains(string(encoded), key) {
			t.Errorf("legacy field absent: %s in %s", key, encoded)
		}
	}
	if _, err := requirementListResult(map[string]any{"items": "malformed"}); err == nil {
		t.Fatal("malformed list suppressed")
	}
}
func TestRequirementCompatibilityAdapterRetainsTaskResolutionAndRawItem(t *testing.T) {
	app, _, _, _ := taskContextScopeFixture(t)
	request, err := RequirementListRequestFromAction(Action{Kind: "requirements.list", TaskID: "missing-task"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.ReadRequirementList(context.Background(), request, nil); err == nil || !strings.Contains(err.Error(), "missing-task") {
		t.Fatalf("task context was discarded before requirement read: %v", err)
	}
	// The JSON edge keeps existing integration coercion/validation semantics.
	// A native caller can supply a typed Item without constructing this map.
	raw := map[string]any{"id": float64(123), "type": "story"}
	read, err := RequirementReadRequestFromAction(Action{Payload: map[string]any{"item": raw, "fullRefresh": true}})
	if err != nil || !read.FullRefresh || !reflect.DeepEqual(read.legacy["item"], raw) {
		t.Fatalf("legacy item changed: %+v %v", read, err)
	}
}
