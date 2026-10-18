package desktop

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/media"
)

func nativeImageRenderTestDocument(t *testing.T, width, height int) *media.ImageDocument {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "image.png")
	if err := os.WriteFile(path, encoded.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	document, err := media.OpenImage(context.Background(), path, media.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = document.Close() })
	return document
}

func TestNativeImageRenderDimensions(t *testing.T) {
	for _, tc := range []struct {
		name                                string
		width, height, zoom                 int
		fit                                 bool
		viewportWidth, viewportHeight       float32
		requestWidth, wantWidth, wantHeight int
	}{
		{name: "original", width: 16, height: 8, zoom: 100, requestWidth: 0, wantWidth: 16, wantHeight: 8},
		{name: "zoom-110", width: 16, height: 8, zoom: 110, requestWidth: 0, wantWidth: 16, wantHeight: 8},
		{name: "zoom-120", width: 16, height: 8, zoom: 120, requestWidth: 0, wantWidth: 16, wantHeight: 8},
		{name: "upper-limit", width: 16, height: 8, zoom: 400, requestWidth: 0, wantWidth: 16, wantHeight: 8},
		{name: "downscale", width: 16, height: 8, zoom: 50, requestWidth: 8, wantWidth: 8, wantHeight: 4},
		{name: "lower-limit", width: 16, height: 8, zoom: 10, requestWidth: 1, wantWidth: 1, wantHeight: 1},
		{name: "single-pixel-downscale", width: 1, height: 1, zoom: 10, requestWidth: 1, wantWidth: 1, wantHeight: 1},
		{name: "fit-small-original", width: 16, height: 8, fit: true, viewportWidth: 1000, viewportHeight: 700, requestWidth: 0, wantWidth: 16, wantHeight: 8},
		{name: "fit-landscape", width: 1200, height: 800, fit: true, viewportWidth: 664, viewportHeight: 496, requestWidth: 600, wantWidth: 600, wantHeight: 400},
		{name: "fit-portrait", width: 800, height: 1200, fit: true, viewportWidth: 864, viewportHeight: 696, requestWidth: 400, wantWidth: 400, wantHeight: 600},
	} {
		t.Run(tc.name, func(t *testing.T) {
			document := nativeImageRenderTestDocument(t, tc.width, tc.height)
			m := &nativeMediaState{kind: "image", image: document, imageInfo: document.Info(), zoom: tc.zoom, fit: tc.fit,
				viewerWidth: tc.viewportWidth, viewerHeight: tc.viewportHeight, renderGeneration: 1, renderLoading: true}
			s := &app{nativeMedia: m}
			width := s.nativeImageRenderWidth(m)
			if width != tc.requestWidth {
				t.Fatalf("render width = %d, want %d", width, tc.requestWidth)
			}
			// Use the real image renderer and UI decoder; no native window is
			// needed to check the bitmap that the UI-thread completion accepts.
			rendered, err := document.RenderPNG(context.Background(), width)
			if err != nil {
				t.Fatal(err)
			}
			bitmap, err := ui.DecodeBitmap(rendered.PNG)
			if err != nil {
				t.Fatal(err)
			}
			key := fmt.Sprintf("image:%d", width)
			m.wantedRenderKey = key
			s.applyNativeImageRender(m, 1, key, bitmap, nil, false)
			if m.bitmap == nil || m.bitmapKey != key || m.renderLoading || m.previewError != "" {
				t.Fatalf("render completion did not publish the bitmap: key=%q loading=%v error=%q", m.bitmapKey, m.renderLoading, m.previewError)
			}
			if w, h := m.bitmap.Size(); w != tc.wantWidth || h != tc.wantHeight {
				t.Fatalf("bitmap = %dx%d, want %dx%d", w, h, tc.wantWidth, tc.wantHeight)
			}
		})
	}
}

func TestNativeImageZoomControlsReuseOriginal(t *testing.T) {
	bitmap := ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 16, 8)))
	m := &nativeMediaState{kind: "image", zoom: 100, imageInfo: media.ImageInfo{Width: 16, Height: 8},
		bitmap: bitmap, bitmapKey: "image:0", wantedRenderKey: "image:0", viewerWidth: 1000, viewerHeight: 700}
	s := &app{nativeMedia: m}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).FillWidth().Height(42).Children(func() {
			s.renderImageControls(c, setTheme(c, false), m)
		})
	}, 1000, 120)
	for _, step := range []struct {
		button string
		zoom   int
		fit    bool
	}{
		{button: "放大", zoom: 110},
		{button: "放大", zoom: 120},
		{button: "原始大小", zoom: 100},
		{button: "适合窗口", zoom: 100, fit: true},
		{button: "原始大小", zoom: 100},
	} {
		if err := tester.Click(step.button); err != nil {
			t.Fatal(err)
		}
		s.requestNativeMediaRender(m, 1000, 700)
		if m.zoom != step.zoom || m.fit != step.fit {
			t.Fatalf("%s: zoom=%d fit=%v, want zoom=%d fit=%v", step.button, m.zoom, m.fit, step.zoom, step.fit)
		}
		if m.bitmap != bitmap || m.wantedRenderKey != "image:0" || m.bitmapKey != "image:0" || m.renderLoading {
			t.Fatalf("%s discarded a reusable original bitmap: bitmap=%p key=%q wanted=%q loading=%v", step.button, m.bitmap, m.bitmapKey, m.wantedRenderKey, m.renderLoading)
		}
	}
	for _, limit := range []struct {
		zoom   int
		button string
	}{{10, "缩小"}, {400, "放大"}} {
		m.zoom = limit.zoom
		tester.Frame()
		if err := tester.Click(limit.button); err != nil {
			t.Fatal(err)
		}
		if m.zoom != limit.zoom || m.bitmap != bitmap {
			t.Fatalf("%s at %d%% changed the limit or discarded the bitmap", limit.button, limit.zoom)
		}
	}
}

func TestNativeImageCachedWidthCancelsOtherRender(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bitmap := ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 16, 8)))
	m := &nativeMediaState{kind: "image", zoom: 100, imageInfo: media.ImageInfo{Width: 16, Height: 8},
		bitmap: bitmap, bitmapKey: "image:0", wantedRenderKey: "image:8", renderLoading: true, renderCancel: cancel, renderGeneration: 2}
	s := &app{nativeMedia: m}
	s.requestNativeMediaRender(m, 1000, 700)
	if ctx.Err() == nil || m.renderCancel != nil || m.renderLoading || m.wantedRenderKey != "image:0" || m.renderGeneration == 2 {
		t.Fatal("returning to the original bitmap did not cancel and invalidate the downscale request")
	}
	// A previous original-size job may have the same key as the new target.
	// Neither it nor the intervening downscale may publish a late error.
	s.applyNativeImageRender(m, 1, "image:0", nil, errors.New("late original"), false)
	s.applyNativeImageRender(m, 2, "image:8", nil, errors.New("late downscale"), false)
	if m.bitmap != bitmap || m.bitmapKey != "image:0" || m.previewError != "" {
		t.Fatal("a cancelled render replaced or hid the cached original")
	}
}

func TestNativeImageCanvasZoomKeepsBitmap(t *testing.T) {
	bitmap := ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 16, 8)))
	m := &nativeMediaState{kind: "image", zoom: 100, imageInfo: media.ImageInfo{Width: 16, Height: 8},
		bitmap: bitmap, bitmapKey: "image:0", wantedRenderKey: "image:0"}
	s := &app{nativeMedia: m}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			s.renderNativeMedia(c, setTheme(c, false), ui.Rect{W: 1000, H: 700})
		})
	}, 1000, 700)
	// Tester.Scroll does not forward modifiers. Exercise the same input
	// handler directly, then let the real canvas reconcile its render state.
	handleNativeImageCanvasInput(m, ui.InputEvent{Kind: ui.InputScroll, Mods: ui.Cmd, DY: 1})
	tester.Frame()
	if m.zoom != 110 {
		t.Fatalf("modified wheel zoom = %d, want 110", m.zoom)
	}
	handleNativeImageCanvasInput(m, ui.InputEvent{Kind: ui.InputKeyDown, Mods: ui.Cmd, Key: ui.KeyEqual})
	tester.Frame()
	if m.zoom != 120 {
		t.Fatalf("keyboard zoom = %d, want 120", m.zoom)
	}
	handleNativeImageCanvasInput(m, ui.InputEvent{Kind: ui.InputKeyDown, Mods: ui.Cmd, Key: ui.KeyMinus})
	tester.Frame()
	if m.zoom != 110 {
		t.Fatalf("keyboard zoom out = %d, want 110", m.zoom)
	}
	handleNativeImageCanvasInput(m, ui.InputEvent{Kind: ui.InputKeyDown, Mods: ui.Cmd, Key: ui.Key0})
	tester.Frame()
	if !m.fit || m.bitmap != bitmap || m.bitmapKey != "image:0" || m.wantedRenderKey != "image:0" || m.renderLoading || tester.HasText("正在生成页面图像") {
		t.Fatal("canvas zoom or fit discarded the image or left the preview loading")
	}
}

func TestNativeImageRenderCompletionRejectsStaleResults(t *testing.T) {
	for _, tc := range []struct {
		name       string
		change     func(*app, *nativeMediaState)
		generation uint64
		cancelled  bool
		loading    bool
	}{
		{name: "older-request-same-key", generation: 2, loading: true},
		{name: "new-target-key", generation: 3, loading: true, change: func(_ *app, m *nativeMediaState) { m.wantedRenderKey = "image:8" }},
		{name: "zoom-before-next-frame", generation: 3, change: func(_ *app, m *nativeMediaState) { m.zoom = 50 }},
		{name: "cancelled", generation: 3, cancelled: true},
		{name: "closed-document", generation: 3, loading: true, change: func(_ *app, m *nativeMediaState) { m.closed = true }},
		{name: "replaced-document", generation: 3, loading: true, change: func(s *app, _ *nativeMediaState) { s.nativeMedia = &nativeMediaState{} }},
	} {
		for _, failed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/error-%v", tc.name, failed), func(t *testing.T) {
				old := ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 8, 4)))
				m := &nativeMediaState{kind: "image", zoom: 100, imageInfo: media.ImageInfo{Width: 16, Height: 8},
					bitmap: old, bitmapKey: "image:8", wantedRenderKey: "image:0", renderGeneration: 3, renderLoading: true}
				s := &app{nativeMedia: m}
				if tc.change != nil {
					tc.change(s, m)
				}
				key := m.wantedRenderKey
				bitmap := ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 16, 8)))
				var err error
				if failed {
					bitmap, err = nil, errors.New("stale failure")
				}
				s.applyNativeImageRender(m, tc.generation, "image:0", bitmap, err, tc.cancelled)
				if m.bitmap != old || m.bitmapKey != "image:8" || m.previewError != "" || m.wantedRenderKey != key || m.renderLoading != tc.loading {
					t.Fatalf("stale completion changed current state: bitmap=%p key=%q wanted=%q loading=%v error=%q", m.bitmap, m.bitmapKey, m.wantedRenderKey, m.renderLoading, m.previewError)
				}
			})
		}
	}
}

func TestNativeImageFitTracksViewport(t *testing.T) {
	m := &nativeMediaState{kind: "image", fit: true, imageInfo: media.ImageInfo{Width: 1200, Height: 800},
		viewerWidth: 1264, viewerHeight: 896, renderGeneration: 1, wantedRenderKey: "image:0", renderLoading: true}
	s := &app{nativeMedia: m}
	if width := s.nativeImageRenderWidth(m); width != 0 {
		t.Fatalf("initial fit width = %d, want original", width)
	}
	m.viewerWidth, m.viewerHeight = 664, 496
	if width := s.nativeImageRenderWidth(m); width != 600 {
		t.Fatalf("resized fit width = %d, want 600", width)
	}
	s.applyNativeImageRender(m, 1, "image:0", nil, errors.New("old viewport"), false)
	if m.previewError != "" || m.renderLoading {
		t.Fatal("old viewport result blocked a render for the new fit size")
	}
}

func TestNativeImageRenderWaitsForWindow(t *testing.T) {
	// A stale cache key without a bitmap is not a usable resource. Before a
	// window exists, leave the missing image retryable on the next frame.
	m := &nativeMediaState{kind: "image", zoom: 100, imageInfo: media.ImageInfo{Width: 16, Height: 8}, bitmapKey: "image:0", wantedRenderKey: "image:0"}
	s := &app{nativeMedia: m}
	for range 2 {
		s.requestNativeMediaRender(m, 1000, 700)
		if m.renderLoading || m.renderCancel != nil || m.renderGeneration != 0 || m.bitmap != nil {
			t.Fatal("request without a window was marked as an active render")
		}
	}
}
