package raster

import (
	"bytes"
	"fmt"
	"image"
	"testing"

	"github.com/egoist/mygo/internal/scene"
)

// referenceFills computes coverage for every pixel, without solid spans. Its
// scenes contain plain fills and clips, including fractional and curved edges.
func referenceFills(s *scene.Scene) *Image {
	dst := NewImage(s.Width, s.Height)
	r := renderer{dst: dst, s: s, area: image.Rect(0, 0, s.Width, s.Height)}
	r.clear(s.Clear)
	r.updateBounds()
	for i := range s.Ops {
		op := &s.Ops[i]
		switch op.Kind {
		case scene.OpPushClip:
			cs := scene.ClipShapeOf(op, s.Stops)
			c := clip{shape: newShape(cs.Rect, scene.Corners(cs.Rect, cs.Radii, cs.Continuous)), spans: -1}
			c.round = hasRadii(c.radii)
			c.transform = cs.Transform
			c.inverse, c.valid = cs.Transform.Inverse()
			if c.masked = cs.Masks(); c.masked {
				c.mask = newClipMasking(&cs)
			}
			r.clips = append(r.clips, c)
			r.updateBounds()
		case scene.OpPopClip:
			r.clips = r.clips[:len(r.clips)-1]
			r.updateBounds()
		case scene.OpFill:
			shape := newShape(op.Rect, scene.Corners(op.Rect, op.Radii, op.Continuous))
			ink := op.Color.Premul(1)
			x0, y0, x1, y1 := r.pixelBounds(op.Rect)
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					cov := coverage(&shape, float32(x)+.5, float32(y)+.5) * r.clipCoverage(x, y)
					at := y*dst.Stride + 4*x
					blend(dst.Pix[at:at+4], ink, cov)
				}
			}
		}
	}
	return dst
}

func TestFillSpansMatchPerPixelCoverage(t *testing.T) {
	for _, size := range [][2]int{{83, 71}, {520, 600}} {
		for _, phase := range []float32{0, .125, .5, .875} {
			s := &scene.Scene{Width: size[0], Height: size[1], Clear: scene.Color{R: 19, G: 37, B: 53, A: 127}}
			outer := scene.Rect{X: 3 + phase, Y: 5 + phase, W: float32(size[0]) - 7.5, H: float32(size[1]) - 11.25}
			s.Ops = append(s.Ops, scene.Op{Kind: scene.OpPushClip, Rect: outer})
			// Popping one sibling must discard its cached rows before another
			// clip at the same depth reuses the scratch space.
			for j := range 4 {
				inner := scene.Rect{X: 9 - phase, Y: 7 + float32(j)*11 + phase, W: outer.W - 13.25, H: outer.H - 19.5}
				clipOp := scene.Op{Kind: scene.OpPushClip, Rect: inner}
				if j > 0 {
					clipOp.Radii = [4]float32{8, 4, 11, 3}
					clipOp.Continuous = j == 2
				}
				if j == 3 {
					clipOp.Transform = scene.Translation(6.25, -3.5).Mul(scene.Rotation(3))
				}
				s.Ops = append(s.Ops, clipOp)
				for k := range 5 {
					r := scene.Rect{X: float32(k*13) - .5 + phase, Y: float32(k) + phase, W: 24.5, H: float32(size[1])}
					// Opaque ink avoids the existing one-level difference between
					// integer span blending and floating-point edge blending.
					op := scene.Op{Kind: scene.OpFill, Rect: r, Color: scene.Color{R: byte(37 + 19*k), G: 99, B: 235, A: 255}}
					if k == 3 {
						op.Radii = [4]float32{3, 2, 1, 4}
					}
					s.Ops = append(s.Ops, op)
				}
				s.Ops = append(s.Ops, scene.Op{Kind: scene.OpPopClip})
			}
			s.Ops = append(s.Ops, scene.Op{Kind: scene.OpPopClip})
			want := referenceFills(s)
			var r Renderer
			for range 2 {
				r.Invalidate()
				r.Render(s)
				if !bytes.Equal(r.Image.Pix, want.Pix) {
					for i, v := range want.Pix {
						if r.Image.Pix[i] != v {
							t.Fatalf("size %v phase %g, byte %d: got %d, want %d", size, phase, i, r.Image.Pix[i], v)
						}
					}
				}
			}
			r.Release()
		}
	}
}

func TestWideClipSpans(t *testing.T) {
	for _, radius := range []float32{0, 3e9} {
		s := &scene.Scene{Width: 8, Height: 8, Ops: []scene.Op{
			{Kind: scene.OpPushClip, Rect: scene.Rect{W: 8e9, H: 8e9}, Radii: [4]float32{radius, radius, radius, radius}},
			{Kind: scene.OpFill, Rect: scene.Rect{W: 8, H: 8}, Color: scene.Color{R: 255, A: 255}},
			{Kind: scene.OpFill, Rect: scene.Rect{W: 8, H: 8}, Color: scene.Color{R: 255, A: 255}},
			{Kind: scene.OpPopClip},
		}}
		want := referenceFills(s)
		got := NewImage(8, 8)
		Render(got, s)
		if !bytes.Equal(got.Pix, want.Pix) {
			t.Fatalf("clip radius %g changed pixels when reused", radius)
		}
	}
}

// Many small clipped groups must not initialize full-height row caches.
func BenchmarkSmallClipGroups(b *testing.B) {
	s := &scene.Scene{Width: 1024, Height: 1024}
	for y := 0; y < s.Height; y += 16 {
		for x := 0; x < s.Width; x += 16 {
			rc := scene.Rect{X: float32(x), Y: float32(y), W: 16, H: 16}
			s.Ops = append(s.Ops,
				scene.Op{Kind: scene.OpPushClip, Rect: rc},
				scene.Op{Kind: scene.OpFill, Rect: rc, Color: scene.Color{R: 37, G: 99, B: 235, A: 255}},
				scene.Op{Kind: scene.OpPopClip})
		}
	}
	var r Renderer
	defer r.Release()
	r.Render(s)
	b.ReportAllocs()
	for b.Loop() {
		r.Invalidate()
		r.Render(s)
	}
}

func BenchmarkClippedFills(b *testing.B) {
	for _, count := range []int{32, 4096} {
		for _, kind := range []string{"square", "round", "continuous"} {
			b.Run(fmt.Sprintf("%s/%d", kind, count), func(b *testing.B) {
				s := &scene.Scene{Width: 1024, Height: 128}
				c := scene.Op{Kind: scene.OpPushClip, Rect: scene.Rect{X: .25, Y: .5, W: 1023.5, H: 127}}
				if kind != "square" {
					c.Radii = [4]float32{12, 12, 12, 12}
					c.Continuous = kind == "continuous"
				}
				s.Ops = append(s.Ops, c)
				for i := range count {
					s.Ops = append(s.Ops, scene.Op{Kind: scene.OpFill, Rect: scene.Rect{X: float32(i%1024) + .25, W: 2, H: 128}, Color: scene.Color{R: 37, G: 99, B: 235, A: 80}})
				}
				s.Ops = append(s.Ops, scene.Op{Kind: scene.OpPopClip})
				var r Renderer
				defer r.Release()
				r.Render(s)
				b.ReportAllocs()
				for b.Loop() {
					r.Invalidate()
					r.Render(s)
				}
			})
		}
	}
}
