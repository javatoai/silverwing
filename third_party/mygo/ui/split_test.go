package ui

import (
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/internal/platform"
)

func TestSplitRatioHandleFollowsLayout(t *testing.T) {
	ratio := float32(.3)
	tt := coreNewTester(func(c *context) {
		coreSplitRatio(c, &ratio, func() {}, func() {}).Fill()
	}, 800, 200)
	check := func() {
		t.Helper()
		divider, ok := tt.Find("Divider")
		if !ok {
			t.Fatal("divider missing")
		}
		handle, ok := tt.Find("Divider drag handle")
		if !ok || math.Abs(float64(handle.X+handle.W/2-divider.X-divider.W/2)) > 1 {
			t.Fatalf("handle %+v does not cover divider %+v", handle, divider)
		}
	}
	check()
	tt.SetSize(1000, 200)
	check()
}

func TestSplitRatioClampingRebuildsLayout(t *testing.T) {
	ratio := float32(.25)
	tt := newRepaintTester(func(c *context) {
		coreSplitRatio(c, &ratio, func() {}, func() {}).Fill()
	})
	tt.h.w, ratio = 800, .25
	tt.rt.changed()
	tt.frame(time.Millisecond)
	tt.frame(time.Millisecond)
	ratio = .25
	tt.rt.changed()
	tt.frame(time.Millisecond)
	tt.h.w = 400
	tt.rt.event(platform.SurfaceEvent{Kind: platform.SurfaceResize})
	if built, more := tt.frame(time.Millisecond); !built || !more {
		t.Fatalf("resize built %v, requested follow-up %v", built, more)
	}
	if built, _ := tt.frame(time.Millisecond); !built {
		t.Fatal("clamping the split requested paint only, leaving the pane at its old width")
	}
	divider, _ := tt.Find("Divider")
	if math.Abs(float64(divider.X-ratio*400)) > 1 {
		t.Fatalf("divider at %v, want clamped position %v", divider.X, ratio*400)
	}
}

func TestSplitRatioSubpixelWidthSettles(t *testing.T) {
	ratio := float32(.5)
	tt := newRepaintTester(func(c *context) {
		coreSplitRatio(c, &ratio, func() {}, func() {}).Fill()
	})
	tt.h.w = .5
	tt.rt.event(platform.SurfaceEvent{Kind: platform.SurfaceResize})
	tt.frame(time.Millisecond)
	_, more := tt.frame(time.Millisecond)
	if ratio < 0 || ratio > 1 || more {
		t.Fatalf("subpixel split ratio = %v, requested another frame = %v", ratio, more)
	}
}

func TestSplitRatioDragAndDoubleClickReset(t *testing.T) {
	ratio := float32(.5)
	tt, now := motionTester(func(c *context) {
		coreSplitRatio(c, &ratio, func() { coreBox(c).Grow(1) }, func() { coreBox(c).Grow(1) }).Fill()
	})
	tt.SetSize(800, 200)

	// The synthetic pointer remains pressed while it moves across frames, as
	// a desktop drag does. The divider is centered on the initial 400-DIP split.
	throw(tt, now, 400, 50, 96, 0, 6)
	tt.Release(496, 50)
	if ratio <= .5 || ratio >= .9 {
		t.Fatalf("dragging the divider changed ratio to %v, want a wider first pane", ratio)
	}

	// The handle follows the new split and a double-click restores an even one.
	x := 800 * ratio
	tt.ClickAt(x, 50)
	tt.ClickAt(x, 50)
	if math.Abs(float64(ratio-.5)) > 0.001 {
		t.Fatalf("double-click reset ratio to %v, want .5", ratio)
	}
	ratio = .01
	tt.Frame()
	if ratio*800 < 159 {
		t.Fatalf("splitter allowed a pane narrower than the reading minimum: %v", ratio)
	}
	tt.SetSize(240, 200)
	if math.Abs(float64(ratio-.5)) > .01 {
		t.Fatalf("very narrow splitter did not keep the panes evenly readable: %v", ratio)
	}
}
