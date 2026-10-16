//go:build windows

package d3d11

import (
	"log"
	"os"
	"time"
)

var resizeTraceEnabled = os.Getenv("MYGO_RESIZE_TRACE") == "1"

// Opt-in diagnostics keep rendering timings out of normal application logs.
type resizeFrameTrace struct {
	started, drawn, presented time.Time
	w, h                      int
}

func beginResizeFrameTrace(r *Renderer, w, h int, resized, liveResize bool) *resizeFrameTrace {
	if !resizeTraceEnabled || !resized {
		return nil
	}
	log.Printf("mygo-resize begin at=%d hwnd=%x scene=%dx%d old=%dx%d buffers=%dx%d composed=%t live=%t", time.Now().UnixMicro(), r.hwnd, w, h, r.w, r.h, r.bw, r.bh, r.composed, liveResize)
	return &resizeFrameTrace{started: time.Now(), w: w, h: h}
}

func (t *resizeFrameTrace) drew() {
	if t != nil {
		t.drawn = time.Now()
	}
}

func (t *resizeFrameTrace) didPresent() {
	if t != nil {
		t.presented = time.Now()
	}
}

func (t *resizeFrameTrace) finish(r *Renderer) {
	if t != nil {
		log.Printf("mygo-resize end at=%d hwnd=%x scene=%dx%d buffers=%dx%d draw_us=%d present_us=%d dwm_us=%d", time.Now().UnixMicro(), r.hwnd, t.w, t.h, r.bw, r.bh, t.drawn.Sub(t.started).Microseconds(), t.presented.Sub(t.drawn).Microseconds(), time.Since(t.presented).Microseconds())
	}
}
