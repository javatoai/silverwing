//go:build windows

package d3d11

import (
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/egoist/mygo/internal/scene"
)

// This diagnostic uses a private hidden window, never system input or the
// user's desktop instance. The trace splits draw, Present and DWM waiting.
func TestContinuousResizeFrameLatency(t *testing.T) {
	measureResizeFrameLatency(t, false)
}

func TestLiveResizeFrameLatency(t *testing.T) {
	measureResizeFrameLatency(t, true)
}

func measureResizeFrameLatency(t *testing.T, liveResize bool) {
	t.Helper()
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	r, err := New(hiddenWindow(t, 960, 720))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release()
	var durations []time.Duration
	for i := range 25 {
		w, h := 960+i*36, 720+i*16
		if i > 12 {
			w, h = 1824-(i-12)*54, 1104-(i-12)*24
		}
		user32.NewProc("SetWindowPos").Call(r.hwnd, 0, 0, 0, uintptr(w), uintptr(h), 0x14)
		start := time.Now()
		frame := &scene.Scene{Width: w, Height: h, Clear: scene.Color{R: 244, G: 247, B: 251, A: 255}}
		var renderErr error
		if liveResize {
			renderErr = r.render(frame, true)
		} else {
			renderErr = r.Render(frame)
		}
		if renderErr != nil {
			t.Fatal(renderErr)
		}
		if i > 0 {
			durations = append(durations, time.Since(start))
		}
		if r.w != w || r.h != h {
			t.Fatalf("frame %d: stale renderer %dx%d, want %dx%d", i, r.w, r.h, w, h)
		}
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	t.Logf("resize frames=%d median=%s p95=%s max=%s warp=%t", len(durations), durations[len(durations)/2], durations[len(durations)*95/100], durations[len(durations)-1], r.warp)
}

func TestResizeLoopIsScopedToWindow(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if windowInResizeLoop(0) {
		t.Fatal("an invalid HWND cannot be in a sizing loop")
	}
	if windowInResizeLoop(hiddenWindow(t, 300, 200)) {
		t.Fatal("a private hidden window is not in an interactive sizing loop")
	}
}

func TestContinuousResizeCoversNewEdges(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	r, err := New(hiddenWindow(t, 300, 200))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Release()
	for _, size := range [][2]int{{300, 200}, {1200, 800}, {1500, 1000}, {960, 760}, {1800, 1100}} {
		w, h := size[0], size[1]
		if err := r.draw(&scene.Scene{Width: w, Height: h, Ops: []scene.Op{
			{Kind: scene.OpFill, Rect: scene.Rect{W: float32(w), H: float32(h)}, Color: scene.Color{R: 244, G: 247, B: 251, A: 255}},
		}}); err != nil {
			t.Fatal(err)
		}
		pixels, stride := r.readBack(t)
		for _, point := range [][2]int{{0, 0}, {w - 1, 0}, {0, h - 1}, {w - 1, h - 1}} {
			offset := point[1]*stride + point[0]*4
			p := pixels[offset : offset+4]
			if p[0] < 240 || p[1] < 240 || p[2] < 240 || p[3] != 255 {
				t.Fatalf("%dx%d new edge %v is not covered: BGRA=%v", w, h, point, p)
			}
		}
		if err := r.present(0); err != nil {
			t.Fatal(err)
		}
	}
}
