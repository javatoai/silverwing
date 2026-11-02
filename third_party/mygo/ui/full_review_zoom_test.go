package ui

import (
	"math"
	"testing"

	"github.com/egoist/mygo/internal/platform"
)

func TestFullReviewPreciseWheelZoomFallsBackOnlyWhenUnhandled(t *testing.T) {
	for _, consume := range []bool{false, true} {
		for _, factor := range []float32{1, 2} {
			t.Run(fmtFullReviewZoom(consume, factor), func(t *testing.T) {
				zoomCalls, scrollCalls := 0, 0
				var scrolled InputEvent
				view := NewTester(func(c *Context) {
					Column(c).Fill().HandleInput(func(e InputEvent) bool {
						if e.Kind == InputScroll {
							scrollCalls++
							scrolled = e
							return true
						}
						return false
					}).Children(func() {
						Box(c).Fill().HandleInput(func(e InputEvent) bool {
							if e.Kind == InputZoom {
								zoomCalls++
								return consume
							}
							return false
						})
					})
				}, 400, 300)
				view.SetZoom(factor)
				view.send(platform.SurfaceEvent{Kind: platform.PointerZoom, X: 100, Y: 100, Scale: 1.1, DY: -24.75, Mods: platform.Modifiers(Ctrl), Precise: true})
				if zoomCalls != 1 {
					t.Fatalf("zoom delivered %d times", zoomCalls)
				}
				if consume {
					if scrollCalls != 0 {
						t.Fatal("consumed zoom also scrolled")
					}
					return
				}
				if scrollCalls != 1 || math.Abs(float64(scrolled.DY+24.75/factor)) > 0.001 || scrolled.Mods&Ctrl == 0 || !scrolled.Precise {
					t.Fatalf("legacy wheel was lost or transformed twice: calls=%d event=%+v", scrollCalls, scrolled)
				}
			})
		}
	}
}

func fmtFullReviewZoom(consume bool, factor float32) string {
	if consume {
		if factor == 1 {
			return "consumed"
		}
		return "consumed-content-zoom"
	}
	if factor == 1 {
		return "legacy"
	}
	return "legacy-content-zoom"
}

func TestFullReviewNativePinchDoesNotInventLegacyScroll(t *testing.T) {
	scrolls := 0
	view := NewTester(func(c *Context) {
		Box(c).Fill().HandleInput(func(e InputEvent) bool {
			if e.Kind == InputScroll {
				scrolls++
			}
			return false
		})
	}, 300, 200)
	view.Pinch(20, 20, 1.2)
	if scrolls != 0 {
		t.Fatal("native pinch without wheel payload acquired scrolling")
	}
}
