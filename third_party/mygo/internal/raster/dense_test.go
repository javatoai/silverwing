package raster

import (
	"bytes"
	"fmt"
	"image"
	"runtime"
	"sync/atomic"
	"testing"

	"github.com/egoist/mygo/internal/scene"
)

func shortScene(count int, dense bool) *scene.Scene {
	s := &scene.Scene{Width: 1024, Height: 105, Clear: scene.Color{R: 20, G: 30, B: 40, A: 127}}
	s.Ops = append(s.Ops, scene.Op{Kind: scene.OpPushClip, Rect: scene.Rect{X: .25, Y: .5, W: 1023.5, H: 104}, Radii: [4]float32{6, 9, 12, 3}})
	for i := range count {
		r := scene.Rect{X: float32(i * 13 % 1024), Y: float32(i * 7 % 105), W: 2, H: 7}
		if dense {
			r.Y, r.W, r.H = -.25, 3.5, 106
		}
		s.Ops = append(s.Ops, scene.Op{Kind: scene.OpFill, Rect: r, Color: scene.Color{R: 90, G: 140, B: 230, A: 80}})
	}
	s.Ops = append(s.Ops, scene.Op{Kind: scene.OpPopClip})
	return s
}

func TestDenseShortTargetDrawsAsOne(t *testing.T) {
	old := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(old)
	s := shortScene(4096, true)
	for _, area := range []image.Rectangle{image.Rect(0, 0, s.Width, s.Height), image.Rect(19, 11, 997, 99)} {
		var r Renderer
		defer r.Release()
		got, want := NewImage(s.Width, s.Height), NewImage(s.Width, s.Height)
		bounds := r.opBounds(s)
		r.d.draw(got, s, area, bounds)
		// Browser builds keep GOMAXPROCS at one, even after a request for
		// four. They still check the same pixels through the serial path.
		if runtime.GOMAXPROCS(0) > 1 && (len(r.d.rs) <= 1 || r.d.bands.parts <= 1) {
			t.Fatal("dense short target used only one renderer")
		}
		var one renderer
		one.render(want, s, area, bounds, 0, len(s.Ops), nil, nil)
		if !bytes.Equal(got.Pix, want.Pix) {
			t.Fatalf("drawing %v in bands changed pixels", area)
		}
	}
}

func TestDamagePaddingDoesNotMakeDisjointFillsDense(t *testing.T) {
	old := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(old)
	for _, clipped := range []bool{false, true} {
		t.Run(fmt.Sprint("clipped=", clipped), func(t *testing.T) {
			s := &scene.Scene{Width: 16, Height: 8192}
			for y := range s.Height {
				for x := 0; x < s.Width; x += 2 {
					rc := scene.Rect{X: float32(x), Y: float32(y), W: 1, H: 1}
					if clipped {
						s.Ops = append(s.Ops, scene.Op{Kind: scene.OpPushClip, Rect: rc})
						rc = scene.Rect{W: 16, H: 8192}
					}
					s.Ops = append(s.Ops, scene.Op{Kind: scene.OpFill, Rect: rc, Color: scene.Color{R: 37, G: 99, B: 235, A: 255}})
					if clipped {
						s.Ops = append(s.Ops, scene.Op{Kind: scene.OpPopClip})
					}
				}
			}
			var r Renderer
			defer r.Release()
			r.Render(s)
			if len(r.d.rs) != 1 {
				t.Fatalf("disjoint fills used %d renderers", len(r.d.rs))
			}
			for y := range s.Height {
				for x := range s.Width {
					want := [4]byte{}
					if x%2 == 0 {
						want = [4]byte{37, 99, 235, 255}
					}
					if got := pixel(&r.Image, x, y); got != want {
						t.Fatalf("pixel (%d, %d) = %v, want %v", x, y, got, want)
					}
				}
			}
		})
	}
}

func TestDenseTallTargetBoundsBandCount(t *testing.T) {
	old := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(old)
	s := &scene.Scene{Width: 16, Height: 8192}
	for i := range 8 {
		s.Ops = append(s.Ops, scene.Op{Kind: scene.OpFill, Rect: scene.Rect{W: 16, H: 8192}, Color: scene.Color{R: byte(i), A: 255}})
	}
	var r Renderer
	defer r.Release()
	r.Render(s)
	if runtime.GOMAXPROCS(0) > 1 && (len(r.d.rs) <= 1 || r.d.bands.parts <= 1) {
		t.Fatal("dense tall target did not split its work")
	}
	if r.d.bands.parts > 2*runtime.GOMAXPROCS(0) {
		t.Fatalf("tall target traversed its scene in %d bands", r.d.bands.parts)
	}
	for y := range s.Height {
		for x := range s.Width {
			if got := pixel(&r.Image, x, y); got != [4]byte{7, 0, 0, 255} {
				t.Fatalf("pixel (%d, %d) = %v", x, y, got)
			}
		}
	}
}

func TestDenseMixedTargetDrawsAsOne(t *testing.T) {
	old := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(old)
	s := shortScene(4096, true)
	m := newSceneMaker(42)
	s.MaskAtlas = m.atlas
	k := m.masks[0]
	s.Glyphs = []scene.Glyph{{X: 354.25, Y: 22.5, W: float32(k[2]), H: float32(k[3]), U: k[0], V: k[1], UW: k[2], VH: k[3], Color: scene.Color{R: 220, G: 90, A: 170}}}
	s.Ops = append(s.Ops,
		scene.Op{Kind: scene.OpImage, Rect: scene.Rect{X: 20.25, Y: 4.5, W: 120, H: 94}, Image: m.img, Src: scene.Rect{W: 8, H: 8}},
		scene.Op{Kind: scene.OpShadow, Rect: scene.Rect{X: 160.25, Y: 10.5, W: 64, H: 70}, Radii: scene.Uniform(6), Color: scene.Color{A: 120}, Blur: 6},
		scene.Op{Kind: scene.OpFill, Rect: scene.Rect{X: 250.5, Y: 3.25, W: 70, H: 94}, Paint: scene.PaintLinear, Color: scene.Color{R: 210, A: 180}, Color2: scene.Color{B: 230, A: 90}, Gradient: [4]float32{250, 3, 320, 97}},
		scene.Op{Kind: scene.OpPushClip, Rect: scene.Rect{X: 350.25, Y: 3.5, W: 100, H: 96}, Image: m.img, Src: scene.Rect{W: 8, H: 8}},
		scene.Op{Kind: scene.OpFill, Rect: scene.Rect{X: 350, W: 100, H: 105}, Color: scene.Color{G: 180, A: 160}},
		scene.Op{Kind: scene.OpGlyphs, Start: 0, End: 1},
		scene.Op{Kind: scene.OpPopClip})
	for _, area := range []image.Rectangle{image.Rect(0, 0, s.Width, s.Height), image.Rect(19, 11, 997, 99)} {
		var r Renderer
		defer r.Release()
		got, want := NewImage(s.Width, s.Height), NewImage(s.Width, s.Height)
		bounds := r.opBounds(s)
		r.d.draw(got, s, area, bounds)
		if runtime.GOMAXPROCS(0) > 1 && (len(r.d.rs) <= 1 || r.d.bands.parts <= 1) {
			t.Fatal("mixed target did not split its work")
		}
		var one renderer
		one.render(want, s, area, bounds, 0, len(s.Ops), nil, nil)
		if !bytes.Equal(got.Pix, want.Pix) {
			t.Fatalf("drawing mixed operations in %v changed pixels", area)
		}
	}
}

func TestShortSparseTargetStaysOnOneCore(t *testing.T) {
	old := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(old)
	for _, count := range []int{32, 4096} {
		s := shortScene(count, false)
		// Borders visit their edges, not their transparent interiors.
		for i := range 6 {
			inset := float32(i)
			s.Ops = append(s.Ops, scene.Op{Kind: scene.OpFill,
				Rect:   scene.Rect{X: inset, Y: inset, W: 1024 - 2*inset, H: 105 - 2*inset},
				Border: scene.Uniform(1), BorderColor: scene.Color{R: 255, A: 255}})
		}
		// Clip bounds are not work: clips that enclose no paint must not
		// make an otherwise cheap target wake more workers.
		for range 32 {
			s.Ops = append(s.Ops, scene.Op{Kind: scene.OpPushClip, Rect: scene.Rect{W: 1024, H: 105}}, scene.Op{Kind: scene.OpPopClip})
		}
		var r Renderer
		r.Render(s)
		if len(r.d.rs) != 1 {
			t.Fatalf("sparse target with %d operations used %d renderers", count, len(r.d.rs))
		}
		r.Release()
	}
}

func TestDenseFillsPreserveEffectBands(t *testing.T) {
	old := runtime.GOMAXPROCS(8)
	defer runtime.GOMAXPROCS(old)
	s := &scene.Scene{Width: 256, Height: 2048, Effects: []scene.EffectOp{{Effect: tintEffect}}}
	s.Ops = append(s.Ops, scene.Op{Kind: scene.OpEffect, Rect: scene.Rect{Y: 32, W: 256, H: 64}})
	for range 4 {
		s.Ops = append(s.Ops, scene.Op{Kind: scene.OpFill, Rect: scene.Rect{W: 256, H: 2048}, Color: scene.Color{R: 37, G: 99, B: 235, A: 80}})
	}
	var r Renderer
	defer r.Release()
	var effectBands atomic.Int32
	r.d.bands.do = func(member, part int) {
		j := &r.d.job
		first := j.area.Min.Y + part*j.rows
		if j.px != nil && first < 96 && first+j.rows > 32 {
			effectBands.Add(1)
		}
		r.d.band(member, part)
	}
	r.Render(s)
	if runtime.GOMAXPROCS(0) > 1 && effectBands.Load() < 4 {
		t.Fatalf("dense fills reduced the effect to %d nonempty bands", effectBands.Load())
	}
}

func TestDenseFillsPreserveParallelSchedule(t *testing.T) {
	old := runtime.GOMAXPROCS(8)
	defer runtime.GOMAXPROCS(old)
	s := &scene.Scene{Width: 256, Height: 2048}
	for range 8 {
		s.Ops = append(s.Ops, scene.Op{Kind: scene.OpFill, Rect: scene.Rect{W: 256, H: 2048}, Color: scene.Color{R: 37, G: 99, B: 235, A: 80}})
	}
	var r Renderer
	defer r.Release()
	r.Render(s)
	want := min(runtime.GOMAXPROCS(0), 4)
	if len(r.d.rs) != want {
		t.Fatalf("already-parallel target used %d renderers, want %d", len(r.d.rs), want)
	}
	if want > 1 && r.d.bands.parts != s.Height/bandRows {
		t.Fatalf("already-parallel target changed its %d-row bands", bandRows)
	}
}

func localizedOverdraw() *scene.Scene {
	s := &scene.Scene{Width: 16, Height: 8192}
	for range 4096 {
		s.Ops = append(s.Ops, scene.Op{Kind: scene.OpFill, Rect: scene.Rect{W: 16, H: 16}, Color: scene.Color{R: 99, A: 255}})
	}
	return s
}

func TestLocalizedOverdrawStaysOnOneCore(t *testing.T) {
	old := runtime.GOMAXPROCS(8)
	defer runtime.GOMAXPROCS(old)
	var r Renderer
	defer r.Release()
	r.Render(localizedOverdraw())
	if len(r.d.rs) != 1 {
		t.Fatalf("overdraw confined to one band used %d renderers", len(r.d.rs))
	}
}

func BenchmarkLocalizedOverdraw(b *testing.B) {
	s := localizedOverdraw()
	var r Renderer
	defer r.Release()
	r.Render(s)
	b.ReportAllocs()
	for b.Loop() {
		r.Invalidate()
		r.Render(s)
	}
}

func BenchmarkShortTarget(b *testing.B) {
	for _, dense := range []bool{false, true} {
		for _, canvas := range []bool{false, true} {
			b.Run(fmt.Sprintf("dense=%t/canvas=%t", dense, canvas), func(b *testing.B) {
				s := shortScene(4096, dense)
				var c *scene.Image
				if canvas {
					c = scene.NewCanvas(s.Width, s.Height)
					*c.Scene = *s
					s = &scene.Scene{Width: c.W, Height: c.H, Canvases: []*scene.Image{c}, Ops: []scene.Op{{Kind: scene.OpImage, Rect: scene.Rect{W: float32(c.W), H: float32(c.H)}, Src: scene.Rect{W: float32(c.W), H: float32(c.H)}, Image: c}}}
				}
				var r Renderer
				defer r.Release()
				r.Render(s)
				b.ReportAllocs()
				for b.Loop() {
					if c != nil {
						c.Changed()
					}
					r.Invalidate()
					r.Render(s)
				}
			})
		}
	}
}
