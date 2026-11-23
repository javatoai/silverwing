package ui

import (
	"fmt"
	"image"
	"math"
	"testing"
	"time"

	"github.com/egoist/mygo/internal/platform"
	"github.com/egoist/mygo/internal/scene"
)

// scaleCard is a card with text and a rule, laid out s times its size,
// as the cards of #1840: a title, a line of body text and a rule.
func scaleCard(c *context, s float32) *node {
	black := RGB(0, 0, 0)
	return coreBox(c).Size(320*s, 120*s).Background(RGB(255, 255, 255)).Border(1*s, black).Radius(16 * s).Children(func() {
		coreText(c, "Scaled text 0123").Absolute().Left(15 * s).Top(11 * s).FontSize(20 * s).FontWeight(700).TextColor(black)
		coreText(c, "The quick brown fox jumps").Absolute().Left(15 * s).Top(43 * s).FontSize(14 * s).TextColor(black)
		coreBox(c).Absolute().Left(15*s).Top(83*s).Size(290*s, 1*s).Background(black)
	})
}

// scaleTester is a Tester whose clock moves only as frame says, as
// repaintTester's.
type scaleTester struct {
	*Tester
	clock time.Time
}

func newScaleTester(view func(c *context), w, h int, scale float32) *scaleTester {
	st := &scaleTester{clock: time.Unix(1000, 0)}
	st.Tester = coreNewTester(func(c *context) {
		coreBox(c).Fill().Background(RGB(230, 230, 230)).Children(func() { view(c) })
	}, w, h)
	st.rt.clock = func() time.Time { return st.clock }
	if scale != 1 {
		st.SetScale(scale)
	}
	st.Frame()
	return st
}

// frame draws the frame the surface asks for after d, and reports whether
// it asked for another.
func (t *scaleTester) frame(d time.Duration) (more bool) {
	t.clock = t.clock.Add(d)
	t.h.requested.Store(false)
	t.rt.event(platform.SurfaceEvent{Kind: platform.SurfaceFrame})
	return t.h.requested.Load()
}

// textArea returns the part of m holding the text of a scaleCard 1.6 times
// its size whose corner is at (20, 20) DIPs, at scale pixels a DIP.
func textArea(m *image.RGBA, scale float32) *image.RGBA {
	px := func(v float32) int { return int(v * scale) }
	return m.SubImage(image.Rect(px(20+8), px(20+8), px(20+500), px(20+130))).(*image.RGBA)
}

// upscaled returns m scaled up k times as the GPU samples a bitmap,
// bilinearly between the centers of its pixels.
func upscaled(m *image.RGBA, k int) *image.RGBA {
	b := m.Bounds()
	out := image.NewRGBA(image.Rect(0, 0, b.Dx()*k, b.Dy()*k))
	at := func(x, y, c int) float64 {
		x, y = min(max(x, 0), b.Dx()-1), min(max(y, 0), b.Dy()-1)
		return float64(m.Pix[m.PixOffset(b.Min.X+x, b.Min.Y+y)+c])
	}
	for y := range out.Rect.Dy() {
		for x := range out.Rect.Dx() {
			sx, sy := (float64(x)+0.5)/float64(k)-0.5, (float64(y)+0.5)/float64(k)-0.5
			x0, y0 := int(math.Floor(sx)), int(math.Floor(sy))
			fx, fy := sx-float64(x0), sy-float64(y0)
			for c := range 4 {
				v := (at(x0, y0, c)*(1-fx)+at(x0+1, y0, c)*fx)*(1-fy) + (at(x0, y0+1, c)*(1-fx)+at(x0+1, y0+1, c)*fx)*fy
				out.Pix[out.PixOffset(x, y)+c] = uint8(v + 0.5)
			}
		}
	}
	return out
}

// glyphOps returns the ops of glyphs of s.
func glyphOps(s *scene.Scene) []scene.Op {
	var ops []scene.Op
	for _, op := range s.Ops {
		if op.Kind == scene.OpGlyphs {
			ops = append(ops, op)
		}
	}
	return ops
}

// TestScaledTextIsAsSharpAsLaidOut: text in a card scaled 1.6 times
// (Scale) rasterizes at the size it shows at, so that it is as sharp as
// the card laid out 1.6 times its size, at the window's scale 1 and 1.25,
// scaled from its corner or its center, and its glyphs lie on the frame's
// pixels, drawn without a transform.
func TestScaledTextIsAsSharpAsLaidOut(t *testing.T) {
	const k = 1.6
	for _, scale := range []float32{1, 1.25} {
		laid := newScaleTester(func(c *context) {
			scaleCard(c, k).Absolute().Left(20).Top(20)
		}, 600, 260, scale)
		ref := sharpness(textArea(laid.Image(), scale))
		for _, centered := range []bool{false, true} {
			name := fmt.Sprintf("scale %v, centered %v", scale, centered)
			var card *node
			scaled := newScaleTester(func(c *context) {
				card = scaleCard(c, 1)
				if centered {
					// Around its center, 160×60 DIPs in: where the
					// corner of the card scaled goes is a fraction of a
					// pixel away from a whole one at scale 1.25.
					card.Absolute().Left(20+96).Top(20+36).Scale(k, k)
				} else {
					card.Absolute().Left(20).Top(20).TransformOrigin(0, 0).Scale(k, k)
				}
			}, 600, 260, scale)
			got := sharpness(textArea(scaled.Image(), scale))
			t.Logf("%s: sharpness %.3f of the card laid out at 1.6", name, got/ref)
			// At least as sharp. Transformed glyphs take grayscale
			// coverage, and where the system smooths text laid out on an
			// opaque background in subpixels (ClearType), the card laid
			// out measures about 10% less sharp than the scaled one.
			if got < ref*0.95 || got > ref*1.25 {
				t.Errorf("%s: sharpness %.0f, the card laid out at 1.6 %.0f: want at least 95%% of it, and not over 125%%", name, got, ref)
			}
			for _, op := range glyphOps(&scaled.rt.scene) {
				if op.Transform.Set {
					t.Errorf("%s: glyphs drawn under %+v, want them on the frame's pixels", name, op.Transform)
				}
			}
		}
	}
}

// TestScaledTextResidual: text scaled and turned rasterizes at the
// scale, and the transform left to its glyphs only turns them; text only
// turned or moved rasterizes at the window's scale, under its transform as
// ever, and keeps nothing to watch.
func TestScaledTextResidual(t *testing.T) {
	const k = 1.6
	var text *node
	turn, grow := float32(10), float32(k)
	tt := newScaleTester(func(c *context) {
		coreBox(c).Absolute().Left(100).Top(60).Rotate(turn).Scale(grow, grow).Children(func() {
			text = coreText(c, "Turned and scaled").FontSize(14)
		})
	}, 400, 200, 1)
	plain := newScaleTester(func(c *context) {
		coreBox(c).Absolute().Left(100).Top(60).Children(func() {
			coreText(c, "Turned and scaled").FontSize(14)
		})
	}, 400, 200, 1)
	// Laid out at the size the transform shows it at. Glyph bitmaps are
	// padded by a few pixels that do not scale (CoreText's), so compare
	// with those rather than with k times the unscaled ones.
	big := newScaleTester(func(c *context) {
		coreBox(c).Absolute().Left(100).Top(60).Children(func() {
			coreText(c, "Turned and scaled").FontSize(14 * k)
		})
	}, 400, 200, 1)
	tallest := func(s *scene.Scene) float32 {
		var h float32
		for _, g := range s.Glyphs {
			h = max(h, g.H)
		}
		return h
	}
	hs, h1, hk := tallest(&tt.rt.scene), tallest(&plain.rt.scene), tallest(&big.rt.scene)
	if hs < hk-2 || hs > hk+2 {
		t.Errorf("glyphs %v pixels tall, %v laid out at %v times the size (%v unscaled): want them as tall", hs, hk, k, h1)
	}
	ops := glyphOps(&tt.rt.scene)
	if len(ops) != 1 {
		t.Fatalf("%d ops of glyphs, want 1", len(ops))
	}
	m := ops[0].Transform
	if r := transformScale(m, 0, 0); r < 0.999 || r > 1.001 {
		t.Errorf("the glyphs' transform %+v scales them by %v, want only a turn", m, r)
	}
	for _, g := range tt.rt.scene.Glyphs {
		if g.Transform.Set {
			t.Errorf("a glyph has a transform of its own: %+v", g.Transform)
		}
	}
	for _, still := range []struct {
		what       string
		turn, grow float32
	}{
		{"turned", 10, 1},
		{"moved", 0, 1},
	} {
		turn, grow = still.turn, still.grow
		text.st.scales = nil // as a text that was never scaled
		w := text.st.world
		tt.rt.changed()
		tt.Frame()
		if text.st.world == w {
			t.Fatalf("%s: the transform did not change", still.what)
		}
		if text.st.scales != nil {
			t.Errorf("%s: text not scaled keeps scales to watch", still.what)
		}
		if h := tallest(&tt.rt.scene); h != h1 {
			t.Errorf("%s: glyphs %v pixels tall, want %v as unscaled", still.what, h, h1)
		}
		ops := glyphOps(&tt.rt.scene)
		if len(ops) != 1 || ops[0].Transform != text.st.world.Pixels(1) {
			t.Errorf("%s: glyphs drawn under %+v, want the text's transform %+v", still.what, ops, text.st.world)
		}
	}
}

// rasterScaleOf returns at how much the window's scale the glyphs of the
// scene's first op of glyphs rasterized, from the scale k their text
// shows at, and whether they lie on the frame's pixels.
func rasterScaleOf(t *testing.T, s *scene.Scene, k float32) (q float32, screen bool) {
	t.Helper()
	ops := glyphOps(s)
	if len(ops) == 0 {
		t.Fatal("no glyphs")
	}
	if !ops[0].Transform.Set {
		return k, true
	}
	return k / transformScale(ops[0].Transform, 0, 0), false
}

// TestScaleAnimationRasterizesBuckets: text whose pose scales it from 1 to
// 1.6 rasterizes, while it grows, at the steps of scaleBucket, a few
// times rather than once a frame; the frame in which it stops asks for
// one more, which rasterizes it at 1.6 itself, as the text scaled so from
// the start, and then the window asks for none.
func TestScaleAnimationRasterizesBuckets(t *testing.T) {
	grown := false
	var text *node
	view := func(c *context) {
		p := Pose{}
		if grown {
			p = Pose{Scale: 1.6}
		}
		coreBox(c).Absolute().Left(40).Top(40).TransformOrigin(0, 0).AnimatePose(p, Animation{Duration: 160 * time.Millisecond, Ease: Linear}).Children(func() {
			text = coreText(c, "Word by word").FontSize(18)
		})
	}
	tt := newScaleTester(view, 300, 140, 1)
	grown = true
	tt.rt.requestFrame()
	tt.frame(0)
	scales := map[float32]bool{}
	frames := 0
	for more := true; more; frames++ {
		if frames > 40 {
			t.Fatal("the animation does not end")
		}
		k := transformScale(text.st.world, 0, 0)
		q, screen := rasterScaleOf(t, &tt.rt.scene, k)
		if k < 1.6 && !unscaled(q) {
			if b := scaleBucket(q); math.Abs(float64(b-q)) > 1e-4 || screen {
				t.Errorf("at %v, rasterized at %v, on the frame's pixels %v: want a bucket's scale", k, q, screen)
			}
			if q < k*float32(math.Exp2(-scaleSlack/scaleSteps))*0.999 || q > k*float32(math.Exp2(1.0/scaleSteps))*1.001 {
				t.Errorf("at %v, rasterized at %v: want the bucket at or above it", k, q)
			}
			scales[float32(math.Round(float64(q)*1000)/1000)] = true
		}
		more = tt.frame(8 * time.Millisecond)
	}
	if k := transformScale(text.st.world, 0, 0); k != 1.6 {
		t.Fatalf("the pose ends at %v", k)
	}
	if q, screen := rasterScaleOf(t, &tt.rt.scene, 1.6); q != 1.6 || !screen {
		t.Errorf("at rest, rasterized at %v, on the frame's pixels %v: want 1.6 itself, on them", q, screen)
	}
	if len(scales) > 6 {
		t.Errorf("rasterized at %d scales over %d frames: %v", len(scales), frames, scales)
	}
	t.Logf("%d frames, rasterized at %d scales: %v", frames, len(scales), scales)
	if tt.frame(8 * time.Millisecond) {
		t.Error("at rest, the window asks for frames")
	}
	still := newScaleTester(view, 300, 140, 1) // grown from its first frame
	if d := sameImage(tt.Image(), still.Image()); d != "" {
		t.Errorf("at rest, the text grown differs from the text built grown: %s", d)
	}
}

// TestCacheScaledLayer: a card kept by Cache and scaled 1.6 times renders
// its layer at 1.6 times the window's scale, the pixels it shows on, and
// is as sharp as the card scaled without Cache, and as the card laid out
// at 1.6; scaled by an animation, its layer is drawn again at the steps
// of scaleBucket, not every frame, and at 1.6 once it rests.
func TestCacheScaledLayer(t *testing.T) {
	const k = 1.6
	for _, scale := range []float32{1, 1.25} {
		laid := newScaleTester(func(c *context) { scaleCard(c, k).Absolute().Left(20).Top(20) }, 600, 260, scale)
		ref := sharpness(textArea(laid.Image(), scale))
		for _, centered := range []bool{false, true} {
			name := fmt.Sprintf("scale %v, centered %v", scale, centered)
			var card *node
			cached := newScaleTester(func(c *context) {
				card = scaleCard(c, 1).Cache()
				if centered {
					card.Absolute().Left(20+96).Top(20+36).Scale(k, k)
				} else {
					card.Absolute().Left(20).Top(20).TransformOrigin(0, 0).Scale(k, k)
				}
			}, 600, 260, scale)
			cv := card.st.effectLayer
			if cv == nil {
				t.Fatalf("%s: no layer", name)
			}
			if s := cv.img.Scene.Scale; s != scale*k {
				t.Errorf("%s: layer rendered at %v pixels a DIP, want %v", name, s, scale*k)
			}
			got := sharpness(textArea(cached.Image(), scale))
			t.Logf("%s: sharpness %.3f of the card laid out at 1.6", name, got/ref)
			if got < ref*0.9 || got > ref*1.1 {
				t.Errorf("%s: sharpness %.0f, the card laid out at 1.6 %.0f: want them within 10%%", name, got, ref)
			}
			for _, op := range cached.rt.scene.Ops {
				if op.Kind == scene.OpImage && op.Image == cv.img && (op.Transform.Set || op.Rect.X != float32(math.Round(float64(op.Rect.X)))) {
					t.Errorf("%s: layer drawn at %+v under %+v, want it on whole pixels", name, op.Rect, op.Transform)
				}
			}
		}
	}

	grown := false
	var card *node
	tt := newScaleTester(func(c *context) {
		p := Pose{}
		if grown {
			p = Pose{Scale: k}
		}
		card = scaleCard(c, 1).Cache().Absolute().Left(20).Top(20).TransformOrigin(0, 0).
			AnimatePose(p, Animation{Duration: 160 * time.Millisecond, Ease: Linear})
	}, 600, 260, 1)
	layer := card.st.effectLayer.img
	if layer.Scene.Scale != 1 {
		t.Fatalf("unscaled, the layer renders at %v", layer.Scene.Scale)
	}
	grown = true
	tt.rt.requestFrame()
	tt.frame(0)
	v, draws, frames := layer.Version(), 0, 0
	for more := true; more; frames++ {
		if frames > 40 {
			t.Fatal("the animation does not end")
		}
		if layer.Version() != v {
			draws++
			v = layer.Version()
		}
		more = tt.frame(8 * time.Millisecond)
	}
	if layer.Version() != v {
		draws++
	}
	if layer.Scene.Scale != k {
		t.Errorf("at rest, the layer renders at %v, want %v", layer.Scene.Scale, k)
	}
	t.Logf("layer drawn %d times in %d frames", draws, frames)
	if draws > 8 || draws*2 > frames {
		t.Errorf("layer drawn %d times in %d frames, want it at a few steps", draws, frames)
	}
	if tt.frame(8 * time.Millisecond) {
		t.Error("at rest, the window asks for frames")
	}
}

// TestScaledGroupLayer: an element faded as a group (Opacity), whose
// layer holds a background under text, renders it at the scale its
// transform shows it at, as a layer Cache keeps.
func TestScaledGroupLayer(t *testing.T) {
	var card *node
	newScaleTester(func(c *context) {
		card = scaleCard(c, 1).Opacity(0.8).Absolute().Left(20).Top(20).TransformOrigin(0, 0).Scale(2, 2)
	}, 700, 300, 1.25)
	cv := card.st.effectLayer
	if cv == nil {
		t.Fatal("no group layer")
	}
	if s := cv.img.Scene.Scale; s != 2.5 {
		t.Errorf("group layer rendered at %v pixels a DIP, want 2.5", s)
	}
}

func TestTransformScale(t *testing.T) {
	for _, c := range []struct {
		m    scene.Affine
		want float32
	}{
		{scene.Affine{}, 1},
		{scene.Scaling(1.6, 1.6), 1.6},
		{scene.Rotation(30).Mul(scene.Scaling(2, 2)), 2},
		{scene.Scaling(2, 0.5), 2},
		{scene.Rotation(45), 1},
		{scene.Translation(10.5, 3), 1},
	} {
		if got := transformScale(c.m, 0, 0); math.Abs(float64(got-c.want)) > 1e-5 {
			t.Errorf("transformScale(%+v) = %v, want %v", c.m, got, c.want)
		}
	}
	// In perspective, it is larger nearer the viewer.
	p := scene.Projective(1, 0, 0, 1, 0, 0, 0.002, 0)
	if near, far := transformScale(p, -200, 0), transformScale(p, 200, 0); near <= 1 || far >= 1 {
		t.Errorf("in perspective, %v nearer, %v farther: want more and less than 1", near, far)
	}
	for k, want := range map[float32]float32{1: 1, 1.001: 1, 1.05: float32(math.Exp2(1.0 / 8)), 1.6: float32(math.Exp2(6.0 / 8)), 0.95: 1, 0.5: 0.5} {
		if got := scaleBucket(k); math.Abs(float64(got-want)) > 1e-5 {
			t.Errorf("scaleBucket(%v) = %v, want %v", k, got, want)
		}
	}
}

// TestScaledInlineTransform: a word of a paragraph moved by a transform of
// its own (an inline element's), in a paragraph scaled 2 times, is as
// sharp as in the paragraph laid out 2 times larger, its glyphs only moved
// by the transform of their own.
func TestScaledInlineTransform(t *testing.T) {
	para := func(c *context, s float32) {
		coreText(c, "Lyrics ").FontSize(16 * s).Children(func() {
			coreText(c, "word by word").Translate(0, -3*s)
		})
	}
	laid := newScaleTester(func(c *context) {
		coreBox(c).Absolute().Left(10).Top(20).Children(func() { para(c, 2) })
	}, 360, 100, 1)
	scaled := newScaleTester(func(c *context) {
		coreBox(c).Absolute().Left(10).Top(20).TransformOrigin(0, 0).Scale(2, 2).Children(func() { para(c, 1) })
	}, 360, 100, 1)
	ref, got := sharpness(laid.Image()), sharpness(scaled.Image())
	t.Logf("sharpness %.3f of the paragraph laid out at 2", got/ref)
	if got < ref*0.9 || got > ref*1.1 {
		t.Errorf("sharpness %.0f, the paragraph laid out at 2 %.0f: want them within 10%%", got, ref)
	}
	moved := 0
	for _, g := range scaled.rt.scene.Glyphs {
		if !g.Transform.Set {
			continue
		}
		moved++
		if r := transformScale(g.Transform, 0, 0); r < 0.999 || r > 1.001 {
			t.Errorf("a glyph's own transform %+v scales it by %v, want it only moved", g.Transform, r)
		}
	}
	if moved == 0 {
		t.Error("no glyph moved by the inline element's transform")
	}
}
