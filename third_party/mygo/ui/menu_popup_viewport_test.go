package ui

import (
	"fmt"
	"testing"
	"time"
)

func TestThemedContextMenuLongPointAnchorKeepsLastActionsClickable(t *testing.T) {
	for _, size := range [][4]int{{1168, 854, 373, 414}, {360, 280, 180, 140}, {360, 280, 330, 250}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			chosen := 0
			view := NewTester(func(c *Context) {
				MenuScope(c, "long-point")
				ButtonBase(c).Fill().Label("Target").ContextMenu(func(menu *Menu) {
					for i := 0; i < 35; i++ {
						menu.Item(fmt.Sprintf("entry %02d", i))
					}
					menu.Item("实时编辑")
					menu.Item("只读预览")
					if menu.Item("查看 Markdown 源码").Chosen() {
						chosen++
					}
				})
				ThemedMenus(c, "long-point")
			}, size[0], size[1])
			view.RightClickAt(float32(size[2]), float32(size[3]))
			first, ok := view.Find("entry 00")
			if !ok {
				t.Fatal("menu missing")
			}
			view.Wheel(first.X+8, first.Y+first.H/2, 0, 10000)
			view.Advance(time.Second)
			for _, label := range []string{"实时编辑", "只读预览", "查看 Markdown 源码"} {
				r, ok := view.Find(label)
				if !ok || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > float32(size[0])+1 || r.Y+r.H > float32(size[1])+1 {
					t.Fatalf("last action outside viewport: %q %+v window=%v", label, r, size)
				}
			}
			last, _ := view.Find("查看 Markdown 源码")
			view.ClickAt(last.X+last.W/2, last.Y+last.H/2)
			if chosen != 1 {
				t.Fatalf("visible mouse choice triggered %d times", chosen)
			}
		})
	}
}
