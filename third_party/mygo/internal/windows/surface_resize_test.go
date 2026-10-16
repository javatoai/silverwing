//go:build windows && (amd64 || arm64)

package windows

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/egoist/mygo/internal/gpu/d3d11"
	"github.com/egoist/mygo/internal/platform"
	"github.com/egoist/mygo/internal/scene"
)

var resizeProbeStep func()
var resizeProbeWindow, resizeProbeOriginal uintptr
var resizeProbeTimerSizing bool
var resizeProbeCallWindowProc = user32.NewProc("CallWindowProcW")
var resizeProbeWndProc = syscall.NewCallback(func(hwnd, message, wp, lp uintptr) uintptr {
	if hwnd == resizeProbeWindow && message == wmSizing && !resizeProbeTimerSizing {
		// System pointer position must not move the isolated probe. Its
		// timer supplies all accepted drag rectangles, without SendInput.
		procGetWindowRect.Call(hwnd, lp)
	}
	result, _, _ := resizeProbeCallWindowProc.Call(resizeProbeOriginal, hwnd, message, wp, lp)
	return result
})
var resizeProbeTimer = syscall.NewCallback(func(hwnd, msg, id, at uintptr) uintptr {
	if resizeProbeStep != nil {
		resizeProbeStep()
	}
	return 0
})

type resizeWindowHandler struct {
	platform.WindowHandler
	surface *surface
	resizes [][2]int32
	draw    func()
}

func (*resizeWindowHandler) Resized() {}
func (*resizeWindowHandler) Moved()   {}
func (*resizeWindowHandler) Closed()  {}
func (*resizeWindowHandler) Focused() {}
func (*resizeWindowHandler) Blurred() {}
func (h *resizeWindowHandler) SurfaceEvent(ev platform.SurfaceEvent) bool {
	if h.surface != nil && ev.Kind == platform.SurfaceResize {
		var r rect
		procGetClientRect.Call(h.surface.hwnd, uintptr(unsafe.Pointer(&r)))
		h.resizes = append(h.resizes, [2]int32{r.Right, r.Bottom})
	}
	if ev.Kind == platform.SurfaceFrame && h.draw != nil {
		h.draw()
	}
	return false
}

// This opt-in probe shows only its own small, non-activating window. It
// samples the real desktop compositor while native resize messages run;
// CaptureContent alone cannot detect an exposed but not yet presented edge.
func TestLiveResizePresentationDuringSlowFrames(t *testing.T) {
	if os.Getenv("MYGO_RESIZE_VISUAL") != "1" {
		t.Skip("set MYGO_RESIZE_VISUAL=1 to sample a private visible window")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	b := New()
	if err := b.Init(nil, platform.AppOptions{Name: "MyGO resize pixel probe"}); err != nil {
		t.Fatal(err)
	}
	defer procDestroyWindow.Call(b.appHwnd)
	h := &resizeWindowHandler{}
	nativeWindow, err := b.NewWindow(&platform.WindowOptions{
		Title: "MyGO isolated resize probe", X: 30, Y: 30, Width: 220, Height: 150,
		UseContentSize: true, Surface: true, Resizable: true, Movable: true, Closable: true,
	}, h)
	if err != nil {
		t.Fatal(err)
	}
	w := nativeWindow.(*window)
	defer w.destroy()
	h.surface = w.surface
	renderer, err := d3d11.New(w.surface.hwnd)
	if err != nil {
		t.Fatal(err)
	}
	defer renderer.Release()
	slow := false
	var frameErr error
	h.draw = func() {
		width, height, scale := w.surface.Size()
		if slow {
			// Model a real UI rebuilding a substantial frame, separately
			// from any driver allocation delay on this particular machine.
			time.Sleep(120 * time.Millisecond)
		}
		pw, ph := int(math.Round(width*scale)), int(math.Round(height*scale))
		// Match native UI: clear transparently, then paint only the actual
		// root bounds. An opaque Clear would also color the swap chain's
		// spare buffer area and hide the missing-frame regression.
		err := renderer.Render(&scene.Scene{Width: pw, Height: ph, Ops: []scene.Op{
			{Kind: scene.OpFill, Rect: scene.Rect{W: float32(pw), H: float32(ph)}, Color: scene.Color{R: 173, G: 217, B: 251, A: 255}},
		}})
		if frameErr == nil {
			frameErr = err
		}
	}
	w.SetAlwaysOnTop(true)
	w.ShowInactive()
	procRedrawWindow.Call(w.surface.hwnd, 0, 0, rdwInvalidate|rdwUpdateNow)
	procDwmFlush.Call()
	if frameErr != nil {
		t.Fatal(frameErr)
	}
	// Let the window's show animation finish. Check the known frame color
	// so a covered window or a locked desktop cannot yield a false pass.
	dc, _, _ := procGetDC.Call(0)
	getPixel := gdi32.NewProc("GetPixel")
	for deadline := time.Now().Add(2 * time.Second); ; {
		var r rect
		var origin point
		procGetClientRect.Call(w.hwnd, uintptr(unsafe.Pointer(&r)))
		procClientToScreen.Call(w.hwnd, uintptr(unsafe.Pointer(&origin)))
		pixel, _, _ := getPixel.Call(dc, uintptr(origin.X+r.Right-10), uintptr(origin.Y+r.Bottom-10))
		if pixel == 0xfbd9ad {
			break
		}
		if time.Now().After(deadline) {
			procReleaseDC.Call(0, dc)
			t.Fatalf("private probe is not visibly presenting its known frame: pixel=%#x", pixel)
		}
		time.Sleep(10 * time.Millisecond)
	}
	procReleaseDC.Call(0, dc)
	resizeProbeWindow = w.hwnd
	resizeProbeOriginal = windowLong(w.hwnd, -4 /* GWLP_WNDPROC */)
	setWindowLong(w.hwnd, -4, resizeProbeWndProc)
	defer func() {
		setWindowLong(w.hwnd, -4, resizeProbeOriginal)
		resizeProbeWindow, resizeProbeOriginal = 0, 0
	}()
	var sampling atomic.Bool
	sampling.Store(true)
	samples := make(chan [][4]int64, 1)
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		dc, _, _ := procGetDC.Call(0)
		defer procReleaseDC.Call(0, dc)
		getPixel := gdi32.NewProc("GetPixel")
		var results [][4]int64
		for sampling.Load() {
			var r rect
			var origin point
			procGetClientRect.Call(w.hwnd, uintptr(unsafe.Pointer(&r)))
			procClientToScreen.Call(w.hwnd, uintptr(unsafe.Pointer(&origin)))
			pixel, _, _ := getPixel.Call(dc, uintptr(origin.X+r.Right-10), uintptr(origin.Y+r.Bottom-10))
			results = append(results, [4]int64{time.Now().UnixMicro(), int64(r.Right), int64(r.Bottom), int64(pixel)})
			time.Sleep(time.Millisecond)
		}
		samples <- results
	}()
	slow = true
	steps := 0
	resizeProbeStep = func() {
		if steps == 8 {
			procKillTimer.Call(w.hwnd, 0x6d7a)
			procPostMessageW.Call(w.hwnd, wmLButtonUp, 0, 0)
			return
		}
		var outer rect
		procGetWindowRect.Call(w.hwnd, uintptr(unsafe.Pointer(&outer)))
		outer.Right += 35
		outer.Bottom += 22
		resizeProbeTimerSizing = true
		procSendMessageW.Call(w.hwnd, wmSizing, 8, uintptr(unsafe.Pointer(&outer)))
		resizeProbeTimerSizing = false
		procSetWindowPos.Call(w.hwnd, 0, uintptr(outer.Left), uintptr(outer.Top), uintptr(outer.Right-outer.Left), uintptr(outer.Bottom-outer.Top), swpNoZOrder|swpNoActivate)
		steps++
	}
	procSetTimer.Call(w.hwnd, 0x6d7a, 40, resizeProbeTimer)
	// Run Windows' actual sizing modal loop. Only this window's timer and
	// messages drive the rectangle; no mouse movement or system input is
	// injected. WM_LBUTTONUP exits the loop after the final timer step.
	procSendMessageW.Call(w.hwnd, wmSysCommand, 0xf008 /* SC_SIZE | WMSZ_BOTTOMRIGHT */, 0)
	procKillTimer.Call(w.hwnd, 0x6d7a)
	resizeProbeStep = nil
	procDwmFlush.Call()
	sampling.Store(false)
	results := <-samples
	if steps != 8 {
		t.Fatalf("native sizing modal loop processed %d steps, want 8", steps)
	}
	black := 0
	invalid := 0
	other := 0
	for _, sample := range results {
		if sample[3] == 0 {
			black++
		}
		if sample[3] == 0xffffffff {
			invalid++
		}
		if sample[3] != 0xfbd9ad && sample[3] != 0 && sample[3] != 0xffffffff {
			// Geometry queries and desktop composition are not atomic. Keep
			// these transition samples visible in the evidence, rather than
			// treating the absence of black as exact color continuity.
			other++
		}
	}
	t.Logf("desktop samples=%d black=%d invalid=%d other=%d resize events=%d", len(results), black, invalid, other, len(h.resizes))
	if output := os.Getenv("MYGO_RESIZE_SAMPLES"); output != "" {
		if err := os.WriteFile(output, []byte(fmt.Sprintln(results)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if frameErr != nil {
		t.Fatal(frameErr)
	}
	if len(results) == 0 || black != 0 || invalid != 0 {
		t.Fatalf("new client edges were exposed before their full frame: %d/%d black samples", black, len(results))
	}
}

func TestSurfacePreparedBeforeLiveGrowth(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	b := New()
	if err := b.Init(nil, platform.AppOptions{Name: "MyGO resize regression"}); err != nil {
		t.Fatal(err)
	}
	defer procDestroyWindow.Call(b.appHwnd)
	h := &resizeWindowHandler{}
	nativeWindow, err := b.NewWindow(&platform.WindowOptions{
		Title: "MyGO resize regression", Width: 300, Height: 200,
		UseContentSize: true, Surface: true, Resizable: true, Movable: true, Closable: true,
	}, h)
	if err != nil {
		t.Fatal(err)
	}
	w := nativeWindow.(*window)
	defer w.destroy()
	h.surface = w.surface
	for _, tc := range []struct {
		name       string
		dx, dy     int32
		left, stop bool
	}{
		{"bottom-right growth", 240, 160, false, false},
		{"top-left growth", 120, 100, true, false},
		{"grow width and shrink height", 90, -60, false, false},
		{"shrink width and grow height", -70, 80, true, false},
		{"shrink both", -50, -40, false, false},
		{"cancel growth", 100, 80, false, true},
	} {
		func() {
			t.Log(tc.name)
			h.resizes = nil
			var before, outer rect
			procGetClientRect.Call(w.hwnd, uintptr(unsafe.Pointer(&before)))
			procGetWindowRect.Call(w.hwnd, uintptr(unsafe.Pointer(&outer)))
			if tc.left {
				outer.Left -= tc.dx
				outer.Top -= tc.dy
			} else {
				outer.Right += tc.dx
				outer.Bottom += tc.dy
			}
			procSendMessageW.Call(w.hwnd, wmSizing, 8, uintptr(unsafe.Pointer(&outer)))
			var parent, prepared rect
			procGetClientRect.Call(w.hwnd, uintptr(unsafe.Pointer(&parent)))
			procGetClientRect.Call(w.surface.hwnd, uintptr(unsafe.Pointer(&prepared)))
			if parent != before {
				t.Fatal("preparation changed the parent before Windows applied its drag rectangle")
			}
			wantPrepared := [2]int32{before.Right + max(tc.dx, 0), before.Bottom + max(tc.dy, 0)}
			if [2]int32{prepared.Right, prepared.Bottom} != wantPrepared {
				t.Fatalf("new client area is exposed before its frame: surface=%+v, want %v", prepared, wantPrepared)
			}
			// The same proposed rectangle must not rebuild or allocate twice.
			count := len(h.resizes)
			procSendMessageW.Call(w.hwnd, wmSizing, 8, uintptr(unsafe.Pointer(&outer)))
			if len(h.resizes) != count {
				t.Fatal("the same proposed size rebuilt its surface twice")
			}
			want := [2]int32{before.Right + tc.dx, before.Bottom + tc.dy}
			if tc.stop {
				want = [2]int32{before.Right, before.Bottom}
			} else {
				procSetWindowPos.Call(w.hwnd, 0, uintptr(outer.Left), uintptr(outer.Top), uintptr(outer.Right-outer.Left), uintptr(outer.Bottom-outer.Top), swpNoZOrder|swpNoActivate)
			}
			procSendMessageW.Call(w.hwnd, wmExitSizeMove, 0, 0)
			var final rect
			procGetClientRect.Call(w.surface.hwnd, uintptr(unsafe.Pointer(&final)))
			if [2]int32{final.Right, final.Bottom} != want {
				t.Fatalf("final surface bounds=%+v, want %v", final, want)
			}
		}()
	}
}
