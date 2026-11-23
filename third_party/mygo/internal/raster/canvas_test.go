package raster

import (
	"bytes"
	"fmt"
	"reflect"
	"runtime"
	"testing"
	"weak"

	"github.com/egoist/mygo/internal/scene"
)

// A redraw starts transparent, including after a different canvas used the
// renderer, and draws children before their parents. Unchanged canvases keep
// their pixels even when the scene that made them has since been cleared.
func TestCanvasRedraws(t *testing.T) {
	for _, incremental := range []bool{false, true} {
		t.Run(fmt.Sprintf("incremental=%v", incremental), func(t *testing.T) {
			child, parent := scene.NewCanvas(32, 24), scene.NewCanvas(64, 48)
			s := &scene.Scene{Width: 80, Height: 64}
			var r Renderer
			defer r.Release()
			for step, size := range [][2]int{{32, 24}, {17, 13}, {96, 60}, {32, 24}} {
				child.Resize(size[0], size[1])
				child.Scene.Reset(size[0], size[1], scene.Color{})
				child.Scene.Ops = append(child.Scene.Ops, scene.Op{Kind: scene.OpFill,
					Rect:  scene.Rect{X: float32(step) + .25, Y: 2.5, W: 11, H: 8},
					Color: scene.Color{R: 230, G: 70, B: 35, A: 140}})
				parent.Scene.Reset(parent.W, parent.H, scene.Color{})
				parent.Scene.Ops = append(parent.Scene.Ops,
					scene.Op{Kind: scene.OpImage, Rect: scene.Rect{X: 4, Y: 3, W: 32, H: 24},
						Src: scene.Rect{W: float32(child.W), H: float32(child.H)}, Image: child},
					scene.Op{Kind: scene.OpFill, Rect: scene.Rect{X: 45, Y: 20, W: 8, H: 8},
						Color: scene.Color{G: 210, A: 255}})
				parent.Changed()
				s.Ops = []scene.Op{{Kind: scene.OpImage, Rect: scene.Rect{X: 3, Y: 5, W: 64, H: 48},
					Src: scene.Rect{W: 64, H: 48}, Image: parent}}
				// Listing a child as well must not draw it twice.
				s.Canvases = []*scene.Image{parent, child}
				var got []byte
				if incremental {
					r.Render(s)
					got = bytes.Clone(r.Image.Pix)
				} else {
					dst := NewImage(s.Width, s.Height)
					Render(dst, s)
					got = dst.Pix
				}
				// Render each child into fresh memory, independent of scratch
				// retained between canvas redraws, then compose the frame.
				for _, c := range []*scene.Image{child, parent} {
					fresh := NewImage(c.W, c.H)
					Render(fresh, c.Scene)
					want := fresh.RGBA()
					if !bytes.Equal(c.Pix, want) {
						t.Fatalf("step %d: canvas %dx%d differs from a fresh render", step, c.W, c.H)
					}
					if c.PixVersion != c.Version() {
						t.Fatal("canvas pixels do not hold its current version")
					}
				}
				want := NewImage(s.Width, s.Height)
				Render(want, s)
				if !bytes.Equal(got, want.Pix) {
					t.Fatalf("step %d: frame differs from a fresh render", step)
				}
				if step == 2 {
					r.Release()
				}
			}
			before := bytes.Clone(parent.Pix)
			parent.Scene.Reset(parent.W, parent.H, scene.Color{})
			r.Render(s)
			if !bytes.Equal(parent.Pix, before) {
				t.Fatal("unchanged canvas lost its pixels")
			}
		})
	}
}

func TestCanvasRedrawKeepsWindowDamage(t *testing.T) {
	c := scene.NewCanvas(40, 32)
	c.Scene.Ops = []scene.Op{{Kind: scene.OpFill, Rect: scene.Rect{W: 20, H: 16}, Color: scene.Color{R: 160, A: 255}}}
	s := &scene.Scene{Width: 100, Height: 80, Canvases: []*scene.Image{c},
		Ops: []scene.Op{{Kind: scene.OpFill, Rect: scene.Rect{W: 100, H: 80}, Color: scene.Color{B: 120, A: 255}}}}
	var r Renderer
	defer r.Release()
	r.Render(s)
	before := bytes.Clone(r.Image.Pix)
	c.Scene.Ops[0].Color.G = 80
	c.Changed()
	if damage := r.Render(s); len(damage) != 0 {
		t.Fatalf("redrawing a canvas the frame does not show damaged it: %v", damage)
	}
	if !bytes.Equal(r.Image.Pix, before) {
		t.Fatal("canvas redraw replaced the window's pixels")
	}
	if c.Pix[1] != 80 {
		t.Fatal("canvas was not redrawn when the window had no damage")
	}
	// The canvas's temporary BGRA pixels and bounds must leave with the
	// window's frame on idle or close, as its main image does.
	r.Release()
	if !reflect.ValueOf(&r).Elem().IsZero() {
		t.Fatal("Release kept rendering scratch memory")
	}
}

func TestCanvasRemovalReleasesEffectInputs(t *testing.T) {
	var r Renderer
	defer r.Release()
	s := &scene.Scene{Width: 8, Height: 8}
	draw := func() weak.Pointer[scene.Image] {
		input := scene.NewImageRGBA(64, 64, make([]byte, 4*64*64))
		c := scene.NewCanvas(8, 8)
		c.Scene.Effects = []scene.EffectOp{{
			Effect: &scene.Effect{Pixels: func() scene.EffectPixels { return &testPixels{} }},
			Images: [4]*scene.Image{input},
		}}
		c.Scene.Ops = []scene.Op{{Kind: scene.OpEffect, Rect: scene.Rect{W: 8, H: 8}}}
		s.Canvases = []*scene.Image{c}
		r.Render(s)
		return weak.Make(input)
	}
	var inputs []weak.Pointer[scene.Image]
	for range 16 {
		inputs = append(inputs, draw())
	}
	s.Reset(8, 8, scene.Color{})
	r.Render(s)
	runtime.GC()
	for i, input := range inputs {
		if input.Value() != nil {
			t.Fatalf("discarded canvas %d still retains its effect input", i)
		}
	}
	runtime.KeepAlive(&r)
}

func TestCanvasRemovalReleasesBandInputs(t *testing.T) {
	old := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(old)
	var r Renderer
	defer r.Release()
	s := &scene.Scene{Width: 8, Height: 8}
	draw := func() weak.Pointer[scene.Image] {
		input := scene.NewImageRGBA(8, 8, make([]byte, 4*8*8))
		for i := 3; i < len(input.Pix); i += 4 {
			input.Pix[i] = 128
		}
		c := scene.NewCanvas(256, 128)
		for range 32 {
			c.Scene.Ops = append(c.Scene.Ops, scene.Op{Kind: scene.OpFill, Rect: scene.Rect{W: 256, H: 128}, Color: scene.Color{R: 90, A: 100}})
		}
		c.Scene.Ops = append(c.Scene.Ops,
			scene.Op{Kind: scene.OpPushClip, Rect: scene.Rect{W: 256, H: 128}, Image: input, Src: scene.Rect{W: 8, H: 8}},
			scene.Op{Kind: scene.OpImage, Rect: scene.Rect{X: 10, Y: 10, W: 220, H: 100}, Image: input, Src: scene.Rect{W: 8, H: 8}, Blur: 2},
			scene.Op{Kind: scene.OpPopClip})
		s.Canvases = []*scene.Image{c}
		r.Render(s)
		if runtime.GOMAXPROCS(0) > 1 && len(r.canvas.rs) <= 1 {
			t.Fatal("canvas did not use band renderers")
		}
		return weak.Make(input)
	}
	var inputs []weak.Pointer[scene.Image]
	for range 8 {
		inputs = append(inputs, draw())
	}
	s.Reset(8, 8, scene.Color{})
	r.Render(s)
	runtime.GC()
	for i, input := range inputs {
		if input.Value() != nil {
			t.Fatalf("discarded canvas %d still retains a band input", i)
		}
	}
	runtime.KeepAlive(&r)
}

func TestCanvasRedrawReadsPreviousPixels(t *testing.T) {
	c := scene.NewCanvas(16, 8)
	c.Scene.Ops = []scene.Op{{Kind: scene.OpFill, Rect: scene.Rect{W: 4, H: 8}, Color: scene.Color{R: 190, G: 80, A: 255}}}
	s := &scene.Scene{Width: 16, Height: 8, Canvases: []*scene.Image{c}}
	var r Renderer
	defer r.Release()
	r.Render(s)
	before := bytes.Clone(c.Pix)
	c.Scene.Ops = []scene.Op{{Kind: scene.OpImage, Rect: scene.Rect{X: 4, W: 12, H: 8},
		Src: scene.Rect{W: 12, H: 8}, Image: c}}
	c.Changed()
	r.Render(s)
	for y := range c.H {
		row := c.Pix[4*y*c.W : 4*(y+1)*c.W]
		if !bytes.Equal(row[:16], make([]byte, 16)) || !bytes.Equal(row[16:], before[4*y*c.W:4*(y*c.W+12)]) {
			t.Fatalf("row %d: drawing the previous image overwrote its source", y)
		}
	}
}

func BenchmarkCanvasRedraw(b *testing.B) {
	for _, ops := range []int{32, 4096} {
		b.Run(fmt.Sprintf("ops=%d", ops), func(b *testing.B) {
			c := scene.NewCanvas(1024, 128)
			for i := range ops {
				c.Scene.Ops = append(c.Scene.Ops, scene.Op{Kind: scene.OpFill,
					Rect:  scene.Rect{X: float32(i * 13 % 1024), Y: float32(i * 7 % 128), W: 2, H: 7},
					Color: scene.Color{R: 90, G: 140, B: 230, A: 180}})
			}
			s := &scene.Scene{Width: 1024, Height: 128, Canvases: []*scene.Image{c},
				Ops: []scene.Op{{Kind: scene.OpImage, Rect: scene.Rect{W: 1024, H: 128},
					Src: scene.Rect{W: 1024, H: 128}, Image: c}}}
			var r Renderer
			defer r.Release()
			r.Render(s)
			b.ReportAllocs()
			for b.Loop() {
				c.Changed()
				r.Render(s)
			}
		})
	}
}
