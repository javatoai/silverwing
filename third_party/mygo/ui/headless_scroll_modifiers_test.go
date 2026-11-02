package ui

import "testing"

func TestTesterScrollWithPreservesEventModifiers(t *testing.T) {
	var events []InputEvent
	view := NewTester(func(c *Context) {
		Box(c).Fill().HandleInput(func(event InputEvent) bool {
			if event.Kind != InputScroll {
				return false
			}
			events = append(events, event)
			return true
		})
	}, 200, 200)
	defer view.rt.close()
	view.ScrollWith(Ctrl|Shift, 20, 30, 4, 12)
	if len(events) != 1 || events[0].Mods != Ctrl|Shift || events[0].DX != 4 || events[0].DY != 12 || !events[0].Precise {
		t.Fatalf("modified scroll was not delivered faithfully: %+v", events)
	}
	view.Scroll(20, 30, 1, 2)
	if len(events) != 2 || events[1].Mods != 0 || events[1].DX != 1 || events[1].DY != 2 {
		t.Fatalf("ordinary Scroll semantics changed: %+v", events)
	}
}
