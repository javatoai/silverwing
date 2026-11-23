package raster

import (
	"bytes"
	"fmt"
	"image"
	"testing"

	"github.com/egoist/mygo/internal/scene"
)

func imagePixelsFixture(w, h int) *scene.Image {
	pixels := make([]byte, 4*w*h)
	for i := 0; i < w*h; i++ {
		a := []int{0, 63, 128, 255}[i%4]
		pixels[4*i], pixels[4*i+1], pixels[4*i+2], pixels[4*i+3] = byte(i*13%(a+1)), byte(i*7%(a+1)), byte(i*23%(a+1)), byte(a)
	}
	return scene.NewImageRGBA(w, h, pixels)
}

// At 1:1, FilterPixelated and bilinear filtering both sample pixel centres.
// Pixelated deliberately stays on the general path, giving these cases a
// reference that still samples and computes coverage for every pixel.
func TestImagePixelsMatchSampling(t *testing.T) {
	img := imagePixelsFixture(23, 17)
	clips := [][]scene.Op{
		nil,
		{{Kind: scene.OpPushClip, Rect: scene.Rect{X: 3.25, Y: 2.75, W: 19.5, H: 16.5}}},
		{{Kind: scene.OpPushClip, Rect: scene.Rect{X: 2, Y: 1, W: 22, H: 20}, Radii: scene.Uniform(6), Continuous: true},
			{Kind: scene.OpPushClip, Rect: scene.Rect{X: 4.5, Y: 3.25, W: 15, H: 14}, Radii: scene.Uniform(3)}},
		{{Kind: scene.OpPushClip, Rect: scene.Rect{W: 21, H: 18}, Transform: scene.Translation(7, 2).Mul(scene.Rotation(12))}},
		{{Kind: scene.OpPushClip, Rect: scene.Rect{W: 30, H: 24}, Color: scene.Color{A: 255}, Color2: scene.Color{}, Paint: scene.PaintLinear, Gradient: [4]float32{0, 0, 30, 24}}},
	}
	for clip, ops := range clips {
		for _, opacity := range []float32{0, .15, .5, 1, 1.25} {
			for _, origin := range [][2]float32{{0, 0}, {5, 4}, {-5, -3}, {28, 20}} {
				t.Run(fmt.Sprintf("clip=%d/opacity=%g/origin=%v", clip, opacity, origin), func(t *testing.T) {
					s := &scene.Scene{Width: 32, Height: 26, Clear: scene.Color{R: 47, G: 90, B: 130, A: 170}}
					s.Ops = append(s.Ops, ops...)
					idx := len(s.Ops)
					s.Ops = append(s.Ops, scene.Op{Kind: scene.OpImage, Rect: scene.Rect{X: origin[0], Y: origin[1], W: 17, H: 12},
						Src: scene.Rect{X: 3, Y: 2, W: 17, H: 12}, Image: img, Opacity: opacity})
					for range ops {
						s.Ops = append(s.Ops, scene.Op{Kind: scene.OpPopClip})
					}
					got, want := NewImage(s.Width, s.Height), NewImage(s.Width, s.Height)
					Render(got, s)
					s.Ops[idx].Pixelated = true
					Render(want, s)
					if !bytes.Equal(got.Pix, want.Pix) {
						for i := range got.Pix {
							if got.Pix[i] != want.Pix[i] {
								t.Fatalf("byte %d: got %d, sampled %d", i, got.Pix[i], want.Pix[i])
							}
						}
					}
				})
			}
		}
	}
}

func TestImagePixelsRejectsFilteredCases(t *testing.T) {
	img := imagePixelsFixture(23, 17)
	base := scene.Op{Kind: scene.OpImage, Image: img, Rect: scene.Rect{W: 20, H: 12}, Src: scene.Rect{W: 20, H: 12}}
	for name, change := range map[string]func(*scene.Op){
		"scaled":       func(op *scene.Op) { op.Rect.W = 21 },
		"fractional":   func(op *scene.Op) { op.Rect.X = .25 },
		"source":       func(op *scene.Op) { op.Src.X = .5 },
		"extent":       func(op *scene.Op) { op.Rect.W, op.Src.W = 19.5, 19.5 },
		"flipped":      func(op *scene.Op) { op.Src.X, op.Src.W = 20, -20 },
		"out-of-range": func(op *scene.Op) { op.Src.Y = 10 },
		"tinted":       func(op *scene.Op) { op.Color = scene.Color{R: 160, A: 255} },
		"gray":         func(op *scene.Op) { op.Grayscale = true },
		"pixelated":    func(op *scene.Op) { op.Pixelated = true },
		"rounded":      func(op *scene.Op) { op.Radii = scene.Uniform(4) },
		"large":        func(op *scene.Op) { op.Rect.X = 1 << 24 },
		"surface":      func(op *scene.Op) { copy := *img; copy.Surface = &scene.Surface{}; op.Image = &copy },
		"no-pixels":    func(op *scene.Op) { copy := *img; copy.Pix = nil; op.Image = &copy },
	} {
		t.Run(name, func(t *testing.T) {
			op := base
			change(&op)
			var r renderer
			if r.imagePixels(&op, 1) {
				t.Fatal("filtered image took the direct pixel path")
			}
		})
	}
}

func TestImagePixelsRestoreAndDamage(t *testing.T) {
	img := imagePixelsFixture(23, 17)
	pix := bytes.Clone(img.Pix)
	img.Restore = func() []byte { return bytes.Clone(pix) }
	img.Pix = nil
	s := &scene.Scene{Width: 80, Height: 48, Ops: []scene.Op{{Kind: scene.OpImage, Image: img,
		Rect: scene.Rect{X: 3, Y: 5, W: 23, H: 17}, Src: scene.Rect{W: 23, H: 17}}}}
	var r Renderer
	defer r.Release()
	r.Render(s)
	if len(img.Pix) != len(pix) {
		t.Fatal("image pixels were not restored")
	}
	s.Ops[0].Rect.X++
	r.Render(s)
	want := NewImage(s.Width, s.Height)
	s.Ops[0].Pixelated = true
	Render(want, s)
	if !bytes.Equal(r.Image.Pix, want.Pix) {
		t.Fatal("incremental image differs from a sampled frame")
	}
}

func BenchmarkImagePixels(b *testing.B) {
	for _, kind := range []string{"opaque", "translucent", "sparse", "scaled"} {
		b.Run(kind, func(b *testing.B) {
			const w, h = 1024, 128
			img := imagePixelsFixture(w, h)
			for i := 0; i < len(img.Pix); i += 4 {
				switch kind {
				case "opaque":
					img.Pix[i+3] = 255
				case "sparse":
					if i/4%32 != 0 {
						clear(img.Pix[i : i+4])
					} else {
						img.Pix[i+3] = 255
					}
				}
			}
			op := scene.Op{Kind: scene.OpImage, Image: img, Rect: scene.Rect{W: w, H: h}, Src: scene.Rect{W: w, H: h}}
			if kind == "scaled" {
				op.Rect.W = w - .5
			}
			dst := NewImage(w, h)
			r := renderer{dst: dst, area: image.Rect(0, 0, w, h)}
			r.updateBounds()
			b.ReportAllocs()
			for b.Loop() {
				r.image(&op)
			}
		})
	}
}
