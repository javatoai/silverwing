package ui

import "testing"

func TestThemedKeyboardContextMenuClosesOnEscape(t *testing.T) {
	tester := NewTester(func(c *Context) {
		MenuScope(c, "keyboard-refresh")
		Column(c).Fill().Padding(8).Children(func() {
			ButtonBase(c.Key("first")).Size(140, 40).Label("First")
			ButtonBase(c.Key("requirement")).Size(140, 40).Label("Requirement").ContextMenu(func(menu *Menu) { menu.Item("Refresh") })
		})
		ThemedMenus(c, "keyboard-refresh")
	}, 240, 260)
	tester.Key(0, KeyTab)
	tester.Key(0, KeyTab)
	tester.Key(Shift, KeyF10)
	if tester.rt.menu.popup == nil {
		t.Fatal("keyboard menu did not open")
	}
	tester.Key(0, KeyEscape)
	if tester.rt.menu.popup != nil {
		t.Fatal("Escape dismissed the item tooltip instead of closing the menu")
	}
	if !tester.Focused("Requirement") {
		t.Fatal("closing the keyboard menu did not restore its owner focus")
	}
}
