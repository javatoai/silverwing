//go:build windows && (amd64 || arm64)

package windows

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
	"unsafe"

	"github.com/egoist/mygo/internal/gpu/d3d11"
	"github.com/egoist/mygo/internal/platform"
	"github.com/egoist/mygo/internal/scene"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// Unlike a solid root fill, fixed DIP markers, text and grid lines reveal
// compositor scaling while a sizing loop is still running. Only this private
// HWND receives messages; the user's pointer and desktop instance are untouched.
func TestLiveResizeKeepsPixelGeometry(t *testing.T) {
	if os.Getenv("MYGO_RESIZE_VISUAL") != "1" {
		t.Skip("set MYGO_RESIZE_VISUAL=1 for the private visible geometry probe")
	}
	for _, tc := range []struct {
		name   string
		edge   uintptr
		dx, dy int32
	}{
		{"top-left-grow", 4, 20, 12}, {"top-left-shrink", 4, -20, -12},
		{"top-grow", 3, 0, 12}, {"top-shrink", 3, 0, -12},
		{"left-grow", 1, 20, 0}, {"left-shrink", 1, -20, 0},
		{"bottom-right-grow", 8, 20, 12}, {"bottom-right-shrink", 8, -20, -12},
	} {
		if only := os.Getenv("MYGO_RESIZE_GEOMETRY_CASE"); only != "" && tc.name != only {
			continue
		}
		t.Run(tc.name, func(t *testing.T) {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			b := New()
			if err := b.Init(nil, platform.AppOptions{Name: "MyGO resize geometry"}); err != nil {
				t.Fatal(err)
			}
			defer procDestroyWindow.Call(b.appHwnd)
			h := &resizeWindowHandler{}
			nativeWindow, err := b.NewWindow(&platform.WindowOptions{
				Title: "MyGO isolated fixed DIP geometry", X: 320, Y: 250, Width: 420, Height: 280,
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
			textImage := image.NewRGBA(image.Rect(0, 0, 126, 18))
			drawer := font.Drawer{Dst: textImage, Src: image.NewUniform(color.RGBA{R: 190, B: 190, A: 255}), Face: basicfont.Face7x13, Dot: fixed.P(0, 13)}
			drawer.DrawString("Fixed 14 DIP text")
			label := scene.NewImage(textImage)
			scale := float32(w.surface.dpi()) / 96
			markerW, markerH := int(math.Round(float64(40*scale))), int(math.Round(float64(16*scale)))
			slow := false
			var frameErr error
			h.draw = func() {
				width, height, dpi := w.surface.Size()
				if slow {
					time.Sleep(40 * time.Millisecond)
				}
				pw, ph := int(math.Round(width*dpi)), int(math.Round(height*dpi))
				ops := []scene.Op{{Kind: scene.OpFill, Rect: scene.Rect{W: float32(pw), H: float32(ph)}, Color: scene.Color{R: 173, G: 217, B: 251, A: 255}}}
				for x := float32(0); x < float32(pw); x += 24 * scale {
					ops = append(ops, scene.Op{Kind: scene.OpFill, Rect: scene.Rect{X: x, Y: 100 * scale, W: scale, H: float32(ph) - 100*scale}, Color: scene.Color{R: 90, G: 150, B: 210, A: 255}})
				}
				ops = append(ops,
					scene.Op{Kind: scene.OpFill, Rect: scene.Rect{X: 20 * scale, Y: 20 * scale, W: 40 * scale, H: 16 * scale}, Color: scene.Color{R: 240, G: 20, B: 20, A: 255}},
					scene.Op{Kind: scene.OpFill, Rect: scene.Rect{X: 80 * scale, Y: 20 * scale, W: 24 * scale, H: 24 * scale}, Color: scene.Color{R: 20, G: 210, B: 40, A: 255}},
					scene.Op{Kind: scene.OpImage, Rect: scene.Rect{X: 20 * scale, Y: 60 * scale, W: 126 * scale, H: 18 * scale}, Src: scene.Rect{W: 126, H: 18}, Image: label})
				if err := renderer.Render(&scene.Scene{Width: pw, Height: ph, Ops: ops}); frameErr == nil {
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
			dc, _, _ := procGetDC.Call(0)
			defer procReleaseDC.Call(0, dc)
			bmp := newBitmap(dc, 900, 650)
			if bmp == nil {
				t.Fatal("cannot capture private probe")
			}
			defer bmp.free()
			bitBlt := gdi32.NewProc("BitBlt")
			type sample struct {
				sizes              [6]int // marker, icon and text width/height
				cw, ch, black, dpi int
			}
			capture := func() sample {
				var r rect
				var origin point
				procGetClientRect.Call(w.hwnd, uintptr(unsafe.Pointer(&r)))
				procClientToScreen.Call(w.hwnd, uintptr(unsafe.Pointer(&origin)))
				cw, ch := int(r.Right), int(r.Bottom)
				if cw <= 0 || ch <= 0 || cw > 900 || ch > 650 {
					return sample{cw: cw, ch: ch}
				}
				if ok, _, _ := bitBlt.Call(bmp.dc, 0, 0, uintptr(cw), uintptr(ch), dc, uintptr(origin.X), uintptr(origin.Y), 0x00cc0020); ok == 0 {
					return sample{cw: cw, ch: ch}
				}
				bounds := [3]image.Rectangle{}
				for i := range bounds {
					bounds[i] = image.Rectangle{Min: image.Pt(cw, ch), Max: image.Pt(-1, -1)}
				}
				result := sample{cw: cw, ch: ch, dpi: w.surface.dpi()}
				for y := 0; y < ch; y++ {
					for x := 0; x < cw; x++ {
						p := bmp.px[y*900+x]
						r, g, b := p>>16&255, p>>8&255, p&255
						which := -1
						switch {
						case r > 220 && g < 40 && b < 40:
							which = 0
						case r < 40 && g > 190 && b < 60:
							which = 1
						case r > 100 && g < 80 && b > 100:
							which = 2
						}
						if which >= 0 {
							v := &bounds[which]
							v.Min.X = min(v.Min.X, x)
							v.Min.Y = min(v.Min.Y, y)
							v.Max.X = max(v.Max.X, x+1)
							v.Max.Y = max(v.Max.Y, y+1)
						}
						if (x == cw-10 || y == ch-10) && p&0xffffff == 0 {
							result.black++
						}
					}
				}
				for i, v := range bounds {
					result.sizes[i*2], result.sizes[i*2+1] = v.Dx(), v.Dy()
				}
				return result
			}
			// A first-frame handshake excludes startup animation and covered desktops.
			var expected [6]int
			for deadline := time.Now().Add(2 * time.Second); ; {
				s := capture()
				if s.sizes[0] == markerW && s.sizes[1] == markerH && s.sizes[2] == int(24*scale) && s.sizes[3] == int(24*scale) && s.sizes[4] > 0 && s.sizes[5] > 0 && s.black == 0 {
					expected = s.sizes
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("first frame geometry=%v black=%d, want marker %dx%d", s.sizes, s.black, markerW, markerH)
				}
				time.Sleep(10 * time.Millisecond)
			}
			resizeProbeWindow = w.hwnd
			resizeProbeOriginal = windowLong(w.hwnd, -4)
			setWindowLong(w.hwnd, -4, resizeProbeWndProc)
			defer func() { setWindowLong(w.hwnd, -4, resizeProbeOriginal); resizeProbeWindow, resizeProbeOriginal = 0, 0 }()
			var sampling atomic.Bool
			sampling.Store(true)
			results := make(chan []sample, 1)
			go func() {
				runtime.LockOSThread()
				defer runtime.UnlockOSThread()
				var samples []sample
				for sampling.Load() {
					s := capture()
					samples = append(samples, s)
					if s.sizes != expected && os.Getenv("MYGO_RESIZE_GEOMETRY_IMAGE") != "" {
						img := image.NewRGBA(image.Rect(0, 0, s.cw, s.ch))
						for y := 0; y < s.ch; y++ {
							for x := 0; x < s.cw; x++ {
								p := bmp.px[y*900+x]
								img.SetRGBA(x, y, color.RGBA{R: uint8(p >> 16), G: uint8(p >> 8), B: uint8(p), A: 255})
							}
						}
						if f, e := os.Create(os.Getenv("MYGO_RESIZE_GEOMETRY_IMAGE")); e == nil {
							_ = png.Encode(f, img)
							_ = f.Close()
						}
					}
					time.Sleep(5 * time.Millisecond)
				}
				results <- samples
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
				if tc.edge == 4 || tc.edge == 1 {
					outer.Left -= tc.dx
				} else {
					outer.Right += tc.dx
				}
				if tc.edge == 4 || tc.edge == 3 {
					outer.Top -= tc.dy
				} else {
					outer.Bottom += tc.dy
				}
				resizeProbeTimerSizing = true
				procSendMessageW.Call(w.hwnd, wmSizing, tc.edge, uintptr(unsafe.Pointer(&outer)))
				resizeProbeTimerSizing = false
				procSetWindowPos.Call(w.hwnd, 0, uintptr(outer.Left), uintptr(outer.Top), uintptr(outer.Right-outer.Left), uintptr(outer.Bottom-outer.Top), swpNoZOrder|swpNoActivate)
				steps++
			}
			procSetTimer.Call(w.hwnd, 0x6d7a, 60, resizeProbeTimer)
			procSendMessageW.Call(w.hwnd, wmSysCommand, 0xf000|tc.edge, 0)
			procKillTimer.Call(w.hwnd, 0x6d7a)
			resizeProbeStep = nil
			sampling.Store(false)
			samples := <-results
			bad, missing, black := 0, 0, 0
			for _, s := range samples {
				black += s.black
				if s.sizes[0] <= 0 || s.sizes[1] <= 0 || s.sizes[4] <= 0 || s.sizes[5] <= 0 {
					missing++
				} else if s.sizes != expected || s.dpi != int(96*scale) {
					bad++
					t.Logf("geometry mismatch: sizes=%v client=%dx%d dpi=%d", s.sizes, s.cw, s.ch, s.dpi)
				}
			}
			t.Logf("steps=%d samples=%d mismatched=%d missing=%d black=%d geometry=%v dpi=%d", steps, len(samples), bad, missing, black, expected, w.surface.dpi())
			if frameErr != nil {
				t.Fatal(frameErr)
			}
			if steps != 8 || len(samples) == 0 || bad != 0 || missing != 0 || black != 0 {
				t.Fatal("fixed DIP geometry changed during the native sizing loop")
			}
		})
	}
}
