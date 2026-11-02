package windows

import (
	"math"
	"testing"

	"github.com/egoist/mygo/internal/platform"
)

func TestFullReviewWheelZoomRetainsLegacyMotion(t *testing.T) {
	for _, delta := range []float64{30, -30, 120, -120} {
		for _, vertical := range []bool{false, true} {
			for _, control := range []bool{false, true} {
				base := platform.SurfaceEvent{X: 23, Y: 47, Mods: platform.ModCtrl}
				ev := pointerWheelEvent(base, vertical, delta, 7, control)
				zoom := vertical && int(delta)%120 != 0 && control
				wantKind := platform.PointerScroll
				if zoom {
					wantKind = platform.PointerZoom
				}
				if ev.Kind != wantKind || ev.X != 23 || ev.Y != 47 || ev.Mods != base.Mods || ev.Precise != (int(delta)%120 != 0) {
					t.Fatalf("wheel metadata changed: %+v", ev)
				}
				distance := delta / 120 * 7 * 33
				if vertical {
					if ev.DY != -distance || ev.DX != 0 {
						t.Fatalf("vertical motion lost: %+v", ev)
					}
				} else {
					if ev.DX != distance || ev.DY != 0 {
						t.Fatalf("horizontal motion lost: %+v", ev)
					}
				}
				if zoom && math.Abs(ev.Scale-math.Exp(delta/120*math.Ln2/3)) > 1e-9 {
					t.Fatal("zoom ratio changed")
				}
			}
		}
	}
	// Native MK_CONTROL can describe a gesture while the physical key is up.
	ev := pointerWheelEvent(platform.SurfaceEvent{}, true, 30, 3, true)
	if ev.Kind != platform.PointerZoom || ev.Mods != 0 || ev.DY != -24.75 {
		t.Fatal("gesture invented physical modifiers or lost fallback")
	}
}
