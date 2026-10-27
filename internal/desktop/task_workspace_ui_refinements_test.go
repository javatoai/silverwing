package desktop

import (
	"math"

	"testing"

	"github.com/egoist/mygo/ui"
)

func TestTaskMoreLeavesOtherMenusAlignedRight(t *testing.T) {
	s := &app{}
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		ui.Row(c).Padding(40, 400).Children(func() {
			s.overflowMenu(c, p, "other", "其它更多操作", false, func(m *overflowMenuBuilder) { m.Item("item", "普通菜单项").Chosen() })
		})
	}, 1000, 600)
	trigger, _ := tester.Find("其它更多操作")
	if err := tester.Click("其它更多操作"); err != nil {
		t.Fatal(err)
	}
	item, ok := tester.Find("普通菜单项")
	if !ok || math.Abs(float64(item.X+item.W-trigger.X-trigger.W+6)) > 1 {
		t.Fatalf("default end-aligned menu was reversed: trigger=%+v item=%+v", trigger, item)
	}
}

// Text, icon and blank space are the same button and toggle once.
