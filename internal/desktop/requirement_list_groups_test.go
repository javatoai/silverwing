package desktop

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func requirementGroupItem(kind, id string) application.Requirement {
	return application.Requirement{ID: id, Type: kind, Title: "Card " + id, ProjectKey: "project-a", URL: "https://project.feishu.cn/space/" + kind + "/detail/" + id, WorkItem: domain.ParticipatedWorkItem{ID: id, Type: kind, ProjectKey: "project-a"}}
}

func TestRequirementGroupsFixedOrderAndStableProjection(t *testing.T) {
	items := []application.Requirement{
		requirementGroupItem("othertask", "t"), requirementGroupItem("userstory", "s2"),
		requirementGroupItem("bug", "b2"), requirementGroupItem("technical", "i"),
		requirementGroupItem("custom", "x"), requirementGroupItem("userstory", "s1"),
		requirementGroupItem(" BUG ", "b1"), requirementGroupItem("", "empty"),
	}
	items[0].Title = "Story title must not change Task type"
	items[4].TypeLabel = "User Story"
	before := append([]application.Requirement(nil), items...)
	groups := groupRequirementList(items)
	wantNames := []string{"Story", "Technical", "Bug", "Task", "Other"}
	wantIDs := [][]string{{"s2", "s1"}, {"i"}, {"b2", "b1"}, {"t"}, {"x", "empty"}}
	if len(groups) != len(wantNames) {
		t.Fatal("missing groups", groups)
	}
	for i, group := range groups {
		ids := make([]string, 0, len(group.items))
		for _, item := range group.items {
			ids = append(ids, item.ID)
		}
		if group.title != wantNames[i] || !reflect.DeepEqual(ids, wantIDs[i]) {
			t.Fatalf("unstable group %d: %s %v", i, group.title, ids)
		}
	}
	if !reflect.DeepEqual(before, items) {
		t.Fatal("group projection reordered or changed source items")
	}
	if groups := groupRequirementList(nil); len(groups) != 0 {
		t.Fatal("empty list has group headers")
	}
	groups = groupRequirementList([]application.Requirement{items[0], items[1]})
	if len(groups) != 2 || groups[0].title != "Story" || groups[1].title != "Task" {
		t.Fatal("empty groups not omitted")
	}
}

func TestRequirementGroupsUISelectionHeadersAndMenus(t *testing.T) {
	for _, width := range []int{180, 340} {
		for _, dark := range []bool{false, true} {
			for _, focused := range []bool{false, true} {
				t.Run(fmt.Sprintf("%d/%t/%t", width, dark, focused), func(t *testing.T) {
					s, _, _ := refreshTestFixture(t)
					s.requirementUnit.state.requirements = []application.Requirement{
						requirementGroupItem("othertask", "t"), requirementGroupItem("userstory", "s2"),
						requirementGroupItem("bug", "b"), requirementGroupItem("technical", "i"),
						requirementGroupItem("userstory", "s1"), requirementGroupItem("custom", "x"),
					}
					s.requirementUnit.state.selectedRequirement = 0
					s.requirementUnit.state.selectedRequirementKey = requirementKey(s.requirementUnit.state.requirements[0])
					s.requirementUnit.state.requirements[4].Status = "技术设计中"
					s.requirementUnit.state.requirements[4].WorkItem.MySprintEstimateDays = refreshTestString("2.5")
					v := ui.NewTester(func(c *ui.Context) {
						ui.MenuScope(c, "group-menu")
						ui.Column(c).Fill().Children(func() { s.requirementList(c, setTheme(c, dark), focused) })
						ui.ThemedMenus(c, "group-menu")
					}, width, 850)
					last := float32(-1)
					for _, title := range []string{"Story · 2", "Technical · 1", "Bug · 1", "Task · 1", "Other · 1"} {
						r, ok := v.Find(title)
						if !ok || r.Y <= last || r.X+r.W > float32(width) {
							t.Fatal("header order/bounds", title, r)
						}
						last = r.Y
					}
					if err := v.Click("Story · 2"); err != nil {
						t.Fatal(err)
					}
					if s.requirementUnit.state.selectedRequirement != 0 || !v.HasText("Card s1") || !v.HasText("Card s2") {
						t.Fatal("header collapsed content or changed selection")
					}
					if !v.HasText("估时：2.5 天") || v.HasText("User Story") || !v.HasText("技术设计中") {
						t.Fatal("card estimate or status lost")
					}
					if err := v.Click("Card s1"); err != nil {
						t.Fatal(err)
					}
					if s.requirementUnit.state.selectedRequirement != 4 || s.requirementUnit.state.selectedRequirementKey != requirementKey(s.requirementUnit.state.requirements[4]) || s.requirementUnit.state.requirementsFocusedDetail != focused {
						t.Fatal("grouped click selected display index instead of original item")
					}
					if err := v.RightClick("Card t"); err != nil {
						t.Fatal(err)
					}
					if !v.HasText("刷新") {
						t.Fatal("card refresh menu missing")
					}
					v.Key(0, ui.KeyEscape)
					if s.requirementUnit.state.selectedRequirement != 4 || s.materialsUnit.state.documentContent != "unsaved draft" {
						t.Fatal("context menu changed selection or existing draft")
					}
				})
			}
		}
	}
}
