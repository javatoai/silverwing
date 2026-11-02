package ui

import "testing"

func TestTrackedScrollbarInteractionIncludesPressBeforeMovement(t *testing.T) {
	for _, kind := range []string{"track", "thumb"} {
		t.Run(kind, func(t *testing.T) {
			var state ScrollState
			v := coreNewTester(func(c *context) {
				coreScroll(c).Fill().TrackScroll(&state).Children(func() { coreBox(c).Height(1000).Shrink(0) })
			}, 200, 200)
			state.Y = 120
			v.Frame()
			state.Y = 0
			v.Frame()
			if state.InteractionVersion() != 0 {
				t.Fatal("programmatic scroll counted as scrollbar input")
			}
			y := float32(12)
			if kind == "track" {
				y = 100
			}
			v.Move(197, y)
			v.Press(197, y)
			if state.InteractionVersion() != 1 {
				t.Fatal("scrollbar press was not recorded")
			}
			if kind == "thumb" {
				if state.Y != 0 {
					t.Fatal("fixture did not press the original thumb")
				}
				v.Move(197, 100)
			}
			v.Release(197, 100)
			if state.Y == 0 {
				t.Fatal("scrollbar did not move")
			}
		})
	}
}

func TestAncestorTrackedScrollSeesUntrackedChildScrollbar(t *testing.T) {
	var state ScrollState
	var inner, content Rect
	v := coreNewTester(func(c *context) {
		coreScroll(c).Fill().TrackScroll(&state).Children(func() {
			coreColumn(c).Children(func() {
				coreScrollHorizontal(c).Height(100).Draw(func(_ *Painter, r Rect) { inner = r }).Children(func() { coreBox(c).Width(1800).Height(90).Shrink(0).Draw(func(_ *Painter, r Rect) { content = r }) })
				coreBox(c).Height(2000).Shrink(0)
			})
		})
	}, 800, 500)
	x, y := inner.X+12, inner.Y+inner.H-3
	v.Move(x, y)
	v.Press(x, y)
	if state.InteractionVersion() != 1 {
		t.Fatal("untracked descendant scrollbar did not mark tracked ancestor")
	}
	v.Move(x+100, y)
	v.Release(x+100, y)
	if content.X >= 0 || state.Y != 0 {
		t.Fatalf("child bar did not independently scroll: child=%v root=%v", content.X, state.Y)
	}
}
