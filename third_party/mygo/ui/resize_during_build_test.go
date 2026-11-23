package ui

import "testing"

// Check the resizing frame itself, not a subsequent frame that catches up.
func TestResizeDuringBuildUsesCurrentSurface(t *testing.T) {
	h := &headless{w: 300, h: 200, scale: 1}
	var targetW, targetH float32 = 300, 200
	rt := newRuntime(func(c *context) {
		Column(makeContext(c)).Size(targetW, targetH).Background(RGB(255, 255, 255))
		h.w, h.h = targetW, targetH
	}, h)
	for _, size := range [][2]float32{{300, 200}, {300, 400}, {300, 160}, {380, 260}} {
		targetW, targetH = size[0], size[1]
		rt.runFrame()
		if h.last.Width != int(targetW) || h.last.Height != int(targetH) {
			t.Fatalf("submitted %dx%d for surface %vx%v", h.last.Width, h.last.Height, targetW, targetH)
		}
	}
}
