package ui

import (
	"testing"

	"github.com/egoist/mygo/internal/platform"
)

func TestThemedContextMenuRebuildsDuringPaintAnimation(t *testing.T) {
	tester := NewTester(func(c *Context) {
		MenuScope(c, "animated-popup")
		ButtonBase(c).Size(120, 40).Label("Target").ContextMenu(func(menu *Menu) {
			menu.Item("Run action")
		})
		Box(c).Size(1, 1).Draw(func(p *Painter, _ Rect) { p.AnimationFrame() })
		ThemedMenus(c, "animated-popup")
	}, 320, 240)
	for attempt := 0; attempt < 3; attempt++ {
		target, ok := tester.Find("Target")
		if !ok {
			t.Fatal("missing target")
		}
		x, y := float64(target.X+target.W/2), float64(target.Y+target.H/2)
		// Use native surface frames, not Tester.settle's forced builds: a
		// paint-only frame must be promoted when the popup becomes visible.
		for _, kind := range []platform.SurfaceEventKind{platform.PointerDown, platform.PointerUp} {
			tester.rt.event(platform.SurfaceEvent{Kind: kind, X: x, Y: y, Button: 1})
			tester.rt.surfaceFrame()
		}
		tester.rt.surfaceFrame()
		if !tester.HasText("Run action") {
			t.Fatal("paint animation postponed the popup until another input event")
		}
		tester.Key(0, KeyEscape)
	}
}

type inlineMenuChoiceHost struct{ *headless }

func (h *inlineMenuChoiceHost) popupMenu(_ *platform.Menu, _, _ float32, chosen func(int)) {
	chosen(1)
}

func TestNativeMenuChoiceRebuildsDuringFrame(t *testing.T) {
	// Some hosts can return a menu choice synchronously (nested native event
	// loops). The callback still needs a new build after the current pass.
	chosen := 0
	tester := NewTester(func(c *Context) {
		ButtonBase(c).Size(120, 40).Label("Target").ContextMenu(func(menu *Menu) {
			if menu.Item("Run").Chosen() {
				chosen++
			}
		})
	}, 320, 240)
	tester.rt.host = &inlineMenuChoiceHost{tester.h}
	if err := tester.RightClick("Target"); err != nil {
		t.Fatal(err)
	}
	if chosen != 1 {
		t.Fatalf("a synchronous native menu choice was delivered %d times, want 1", chosen)
	}
}

func TestThemedContextMenuAppearsWithoutAnotherEvent(t *testing.T) {
	tester := NewTester(func(c *Context) {
		MenuScope(c, "immediate-popup")
		ButtonBase(c).Size(120, 40).Label("Target").ContextMenu(func(menu *Menu) {
			menu.Item("Run action")
		})
		ThemedMenus(c, "immediate-popup")
	}, 320, 240)
	for attempt := 0; attempt < 3; attempt++ {
		if err := tester.RightClick("Target"); err != nil {
			t.Fatal(err)
		}
		if !tester.HasText("Run action") {
			t.Fatal("menu waits for another event instead of scheduling its first visible frame")
		}
		tester.Key(0, KeyEscape)
		if tester.HasText("Run action") {
			t.Fatal("Escape did not close the menu")
		}
	}
}

func TestThemedMenuChoiceReachesOwnerOnce(t *testing.T) {
	for _, button := range []bool{false, true} {
		t.Run(map[bool]string{false: "context", true: "button"}[button], func(t *testing.T) {
			chosen := 0
			tester := NewTester(func(c *Context) {
				MenuScope(c, "choice-owner")
				target := ButtonBase(c).Size(120, 40).Label("Target")
				target.Children(func() { Text(c, "Target") })
				build := func(menu *Menu) {
					if menu.Item("Run action").Chosen() {
						chosen++
					}
				}
				if button {
					target.Menu(build)
				} else {
					target.ContextMenu(build)
				}
				ThemedMenus(c, "choice-owner")
			}, 320, 240)
			var err error
			if button {
				err = tester.Click("Target")
			} else {
				err = tester.RightClick("Target")
			}
			if err != nil {
				t.Fatal(err)
			}
			tester.Frame()
			if err := tester.Click("Run action"); err != nil {
				t.Fatal(err)
			}
			tester.Frame()
			if chosen != 1 {
				t.Fatalf("menu delivered %d choices, want 1", chosen)
			}
			tester.Frame()
			if chosen != 1 || tester.HasText("Run action") {
				t.Fatal("choice repeated or menu stayed open")
			}
		})
	}
}
