package ui

import (
	"math"
	"strings"
	"testing"

	"github.com/egoist/mygo/internal/platform"
)

func spaceClientScrollTester(nested bool) (*primitiveClient, *Tester, *ScrollState, *ScrollState) {
	client := &primitiveClient{text: "A😀", selection: TextInputSelection{Range: TextInputRange{Start: 3, End: 3}}}
	outer, inner := &ScrollState{Y: 300}, &ScrollState{Y: 120}
	view := coreNewTester(func(c *context) {
		root := coreScroll(c).Fill().TrackScroll(outer)
		if !nested {
			root.HandleTextInput(client).AutoFocus().HandleInput(func(InputEvent) bool { return false })
			root.Children(func() { coreBox(c).Height(1800).Shrink(0) })
			return
		}
		root.Children(func() {
			coreColumn(c).AlignSelf(Stretch).Children(func() {
				coreBox(c).Height(300).Shrink(0)
				coreScroll(c).Height(220).Shrink(0).TrackScroll(inner).Children(func() {
					coreColumn(c).AlignSelf(Stretch).Children(func() {
						coreBox(c).Height(120).Shrink(0)
						coreBox(c).Height(60).Shrink(0).HandleTextInput(client).AutoFocus().HandleInput(func(InputEvent) bool { return false })
						coreBox(c).Height(1000).Shrink(0)
					})
				})
				coreBox(c).Height(900).Shrink(0)
			})
		})
	}, 360, 260)
	if !nested {
		inner = nil
	}
	return client, view, outer, inner
}

func TestTextInputClientSpaceNeverPagesBeforeCommit(t *testing.T) {
	for _, nested := range []bool{false, true} {
		for _, mods := range []Modifiers{0, Shift} {
			name := "root/space"
			if nested {
				name = "nested/space"
			}
			if mods == Shift {
				name += "/shift"
			}
			t.Run(name, func(t *testing.T) {
				client, view, outer, inner := spaceClientScrollTester(nested)
				defer view.rt.close()
				if view.h.ime.Client == nil || !view.h.ime.Active {
					t.Fatal("fixture has no focused native input client")
				}
				adapter := view.h.ime.Client
				beforeOuter, beforeInner := outer.Y, float32(0)
				if inner != nil {
					beforeInner = inner.Y
				}
				assertScroll := func(stage string) {
					t.Helper()
					if math.Abs(float64(outer.Y-beforeOuter)) > .05 || inner != nil && math.Abs(float64(inner.Y-beforeInner)) > .05 {
						t.Fatalf("%s paged a text-input scroll ancestor: outer=%+v inner=%+v", stage, outer, inner)
					}
				}
				view.send(platform.SurfaceEvent{Kind: platform.KeyPressed, Key: platform.Key(KeySpace), Mods: platform.Modifiers(mods)})
				assertScroll("KeyDown")
				view.send(platform.SurfaceEvent{Kind: platform.KeyPressed, Key: platform.Key(KeySpace), Mods: platform.Modifiers(mods), Repeat: true})
				assertScroll("repeated KeyDown")
				if client.text != "A😀" || client.changes != 0 || client.selection.Range != (TextInputRange{Start: 3, End: 3}) {
					t.Fatal("Space key handling synthesized a text mutation before native commit")
				}
				view.Type(" ")
				assertScroll("text commit")
				view.send(platform.SurfaceEvent{Kind: platform.KeyReleased, Key: platform.Key(KeySpace), Mods: platform.Modifiers(mods)})
				assertScroll("KeyUp")
				if client.text != "A😀 " || client.changes != 1 || client.selection.Range != (TextInputRange{Start: 4, End: 4}) || view.h.ime.Client != adapter {
					t.Fatalf("native Space commit/client identity changed: text=%q selection=%+v changes=%d", client.text, client.selection, client.changes)
				}
			})
		}
	}
}

func TestTextInputClientSpaceDuringCompositionKeepsMarkAndViewport(t *testing.T) {
	client, view, outer, inner := spaceClientScrollTester(true)
	defer view.rt.close()
	view.Compose("候选😀", 3)
	if client.mark == nil {
		t.Fatal("fixture did not enter native preedit")
	}
	mark, text, selection := *client.mark, client.text, client.selection
	beforeOuter, beforeInner := outer.Y, inner.Y
	view.Key(0, KeySpace)
	if outer.Y != beforeOuter || inner.Y != beforeInner || client.mark == nil || *client.mark != mark || client.text != text || client.selection != selection {
		t.Fatal("Space paged an ancestor or intercepted the marked text")
	}
	view.Type("提交")
	if client.mark != nil || client.text != "A😀提交" {
		t.Fatalf("native composition commit changed: %q", client.text)
	}
}

func TestTextInputClientPageKeysAndUserScrollRemainAvailable(t *testing.T) {
	for _, nested := range []bool{false, true} {
		client, view, outer, inner := spaceClientScrollTester(nested)
		active := outer
		if inner != nil {
			active = inner
		}
		before := active.Y
		view.Key(0, KeyPageDown)
		if active.Y <= before {
			t.Fatalf("real PageDown no longer scrolls: %+v", active)
		}
		view.Key(0, KeyPageUp)
		if math.Abs(float64(active.Y-before)) > .05 {
			t.Fatalf("real PageUp no longer reverses the page: %+v", active)
		}
		view.Scroll(30, 40, 0, 40)
		if active.Y <= before || client.text != "A😀" || client.changes != 0 {
			t.Fatal("text-client Space protection disabled user scrolling or changed text")
		}
		view.rt.close()
	}
}

func TestSpacePagingWithoutTextClientAndNativeControlsRetainContracts(t *testing.T) {
	t.Run("reader", func(t *testing.T) {
		scroll := ScrollState{Y: 300}
		view := coreNewTester(func(c *context) {
			coreScroll(c).Fill().Focusable().AutoFocus().TrackScroll(&scroll).Children(func() { coreBox(c).Height(1800).Shrink(0) })
		}, 360, 260)
		defer view.rt.close()
		before := scroll.Y
		view.Key(0, KeySpace)
		if scroll.Y <= before || view.h.ime.Active {
			t.Fatal("Space no longer pages an ordinary reader without an input client")
		}
		view.Key(Shift, KeySpace)
		if math.Abs(float64(scroll.Y-before)) > .05 {
			t.Fatal("Shift+Space no longer pages the reader upward")
		}
	})
	for _, menu := range []bool{false, true} {
		scroll, clicks := ScrollState{Y: 300}, 0
		view := coreNewTester(func(c *context) {
			coreScroll(c).Fill().TrackScroll(&scroll).Children(func() {
				coreColumn(c).Children(func() {
					coreBox(c).Height(300).Shrink(0)
					if menu {
						coreMenuButton(c, "Actions", func(m *Menu) { m.Item("Run") }).AutoFocus()
					} else if coreButton(c, "Action").AutoFocus().Clicked() {
						clicks++
					}
					coreBox(c).Height(1000).Shrink(0)
				})
			})
		}, 360, 260)
		before := scroll.Y
		view.Key(0, KeySpace)
		if scroll.Y != before {
			t.Fatal("Space on a native button/menu paged its ancestor")
		}
		if menu && !strings.Contains(strings.Join(view.Menu(), " "), "Run") || !menu && clicks != 1 {
			t.Fatalf("native Space control activation changed: menu=%v clicks=%d items=%v", menu, clicks, view.Menu())
		}
		view.rt.close()
	}
}
