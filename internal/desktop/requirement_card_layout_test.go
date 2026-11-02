package desktop

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

func TestRequirementCardEstimateOnlyAndUniformHeight(t *testing.T) {
	for _, kind := range []string{"userstory", "technical", "bug", "othertask", "custom"} {
		t.Run(kind, func(t *testing.T) {
			s, _, _ := refreshTestFixture(t)
			item := requirementGroupItem(kind, "101")
			s.requirementUnit.state.requirements = []application.Requirement{item}
			v := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Fill().Children(func() { s.requirementListItem(c, setTheme(c, false), s.requirementUnit.state.requirements[0], false) })
			}, 280, 200)
			short, ok := v.Find(item.Title)
			if !ok {
				t.Fatal("card missing")
			}
			for _, value := range []*string{nil, refreshTestString(""), refreshTestString("  ")} {
				s.requirementUnit.state.requirements[0].WorkItem.MySprintEstimateDays = value
				v.Frame()
				if !v.HasText("估时：-") || v.HasText("User Story") || v.HasText("Tech Improvement") || v.HasText("Task") || v.HasText("Bug") {
					t.Fatal("card lacks unknown estimate or retains a type label", v.Texts())
				}
				r, _ := v.Find(item.Title)
				if r.H != short.H {
					t.Fatal("unknown estimate changed card height")
				}
			}
			for _, value := range []string{"0", "2", " 2.5 "} {
				s.requirementUnit.state.requirements[0].WorkItem.MySprintEstimateDays = refreshTestString(value)
				v.Frame()
				estimate := "估时：" + strings.TrimSpace(value) + " 天"
				card, _ := v.Find(item.Title)
				line, found := v.Find(estimate)
				if !found || card.H != short.H || line.Y <= card.Y || line.X < card.X || line.X+line.W > card.X+card.W {
					t.Fatal("estimate changed height or lost its bounded second line", estimate, card, line)
				}
				if *s.requirementUnit.state.requirements[0].WorkItem.MySprintEstimateDays != value {
					t.Fatal("layout rewrote estimate data")
				}
			}
			s.requirementUnit.state.requirements[0].WorkItem.MySprintEstimateDays = nil
			v.Frame()
			back, _ := v.Find(item.Title)
			if back.H != short.H || !v.HasText("估时：-") {
				t.Fatal("estimate removal lost placeholder or changed height")
			}
		})
	}
}

func TestRequirementCardLongTitleAndStatusFit(t *testing.T) {
	for _, width := range []int{160, 340} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/%t", width, dark), func(t *testing.T) {
				s, _, _ := refreshTestFixture(t)
				item := requirementGroupItem("userstory", "101")
				item.Title = strings.Repeat("完整的需求标题与附加说明", 8)
				item.Status = "开发完成，等待联调测试与验收确认"
				item.WorkItem.MySprintEstimateDays = refreshTestString("0")
				s.requirementUnit.state.requirements = []application.Requirement{item}
				s.requirementUnit.state.selectedRequirementKey = requirementKey(item)
				v := ui.NewTester(func(c *ui.Context) {
					ui.Column(c).Fill().Children(func() { s.requirementListItem(c, setTheme(c, dark), item, false) })
				}, width, 200)
				card, found := v.Find(item.Title)
				status, hasStatus := v.Find(item.Status)
				if !found || card.W > float32(width) || !hasStatus || status.W > 74 || status.X < 0 || status.X+status.W > float32(width) {
					t.Fatal("long card content overflows", card, status)
				}
				if !v.HasText("估时：0 天") {
					t.Fatal("zero estimate hidden")
				}
				if err := v.Click(item.Title); err != nil {
					t.Fatal(err)
				}
				if s.requirementUnit.state.selectedRequirementKey != requirementKey(item) {
					t.Fatal("long content broke selection")
				}
			})
		}
	}
}
