package ui

import (
	"math"

	"github.com/egoist/mygo/internal/scene"
	"github.com/egoist/mygo/internal/text"
)

// Text and kept layers (Cache, an opacity's group) under a transform that
// draws them larger or smaller (Scale, AnimatePose, Transition's
// ScaleContent) rasterize at the size they show at, rather than at the
// window's scale, stretched: their glyphs, and the layers' bitmaps, are
// made for the pixels they cover on screen, and the transform left to
// them only turns, skews or moves them, as Chromium rasterizes a layer at
// its ideal contents scale and osu!framework's BufferedContainer draws
// anew on scale. That scale is the transform's largest stretch around the
// text, or the layer, as its derivative there tells (transformScale).
//
// At rest, they rasterize at that scale itself. While it changes from frame
// to frame, as an animation scales them, they rasterize at the scale's
// bucket instead, the step of scaleSteps an octave at or above it, so
// that an animation rasterizes each glyph a few times, not once a frame,
// and leaves the atlas a few sizes of it: the glyphs, drawn as large or a
// little smaller (by 9% at most), stay as sharp. The frame in which the
// scale stops changing asks for one more, which draws them at the scale
// itself (scaleWatch).

// scaleSteps is how many buckets an octave of scales has: 2^(1/8) apart,
// about 9%, so that a glyph rasterized at its bucket shows at least 92% of
// that size, and an animation from 1 to 1.6 rasterizes it at 6 sizes.
const scaleSteps = 8

// Raster scales stay between minRasterScale and maxRasterScale of the
// window's, and glyphs within maxGlyphEm pixels an em, beyond which the
// transform scales what they rasterize as before.
const (
	minRasterScale = 1.0 / 8
	maxRasterScale = 8
	maxGlyphEm     = 512
)

// scaleSlack is how close to a bucket, in steps, a scale rasterizes at
// that bucket rather than the one above it: within rounding.
const scaleSlack = 0.05

// transformScale returns by how much m, a transform in DIPs, draws larger
// around (x, y): its derivative's largest stretch there, the factor of a
// uniform scale, the larger of two. A projective map's derivative changes
// from point to point; at a point behind the viewer it tells nothing,
// and the result is 1.
func transformScale(m scene.Affine, x, y float32) float32 {
	if !m.Set {
		return 1
	}
	a, b, c, d := float64(m.A), float64(m.B), float64(m.C), float64(m.D)
	if m.Perspective() {
		w := float64(m.W(x, y))
		if w <= 1e-6 {
			return 1
		}
		px := (a*float64(x) + c*float64(y) + float64(m.X)) / w
		py := (b*float64(x) + d*float64(y) + float64(m.Y)) / w
		g, h := float64(m.G), float64(m.H)
		a, b, c, d = (a-px*g)/w, (b-py*g)/w, (c-px*h)/w, (d-py*h)/w
	}
	// The largest singular value of [a c; b d].
	sq := a*a + b*b + c*c + d*d
	det := a*d - b*c
	k := math.Sqrt((sq + math.Sqrt(max(sq*sq-4*det*det, 0))) / 2)
	if math.IsNaN(k) || math.IsInf(k, 0) || k <= 0 {
		return 1
	}
	return float32(k)
}

// scaleBucket returns the bucket of scale k: the step of scaleSteps an
// octave at or just below it, within scaleSlack.
func scaleBucket(k float32) float32 {
	n := math.Ceil(math.Log2(float64(k))*scaleSteps - scaleSlack)
	return float32(math.Exp2(n / scaleSteps))
}

// unscaled reports whether k is 1 within the slack of a bucket, at which
// what a transform draws rasterizes as without it.
func unscaled(k float32) bool {
	return math.Abs(math.Log2(float64(k))) < scaleSlack/scaleSteps
}

// scaleWatches are what an element's state keeps of the scales its text
// and its layer rasterized at, once a transform scaled either.
type scaleWatches struct {
	text, layer scaleWatch
}

// scaleWatch tells whether the scale of what an element draws changed
// since the frame before it drew it: k in frame, last in the one before,
// and, for a layer, where its transform put its origin in each (at,
// lastAt), in DIPs.
type scaleWatch struct {
	frame      uint64
	k, last    float32
	at, lastAt [2]float32
}

// note notes k, and at, as the frame's, the first time the frame asks.
func (w *scaleWatch) note(frame uint64, k float32, at [2]float32) (first bool) {
	if w.frame == frame {
		return false
	}
	if w.frame != 0 {
		w.last, w.lastAt = w.k, w.at
	}
	w.frame, w.k, w.at = frame, k, at
	return true
}

// rasterScale returns at how much the window's scale what p's owner draws
// k times larger rasterizes, within most, and whether that is k itself.
// It is k while k stays as it was in the frame before that drew it, and
// in the first frame of an element born in it; its bucket while it
// changes, asking for a frame after, in which it stays.
func (p *Painter) rasterScale(w *scaleWatch, k, most float32, at [2]float32) (q float32, exact bool) {
	k = max(k, minRasterScale)
	most = min(max(most, 1), maxRasterScale)
	exact = true
	if w != nil {
		first := w.note(p.rt.paintFrames, k, at)
		last := w.last
		if last == 0 && p.owner.born != p.rt.frame {
			last = 1 // unscaled until now, as no transform scaled it
		}
		exact = last == 0 || last == k
		if !exact && first && !unscaled(k) && scaleBucket(k) != k {
			// Drawn at its bucket while it changes: at k itself once it
			// stays.
			p.rt.repainting = true
			p.asks()
		}
	} else {
		// No element tells whether it changes, as for a canvas's
		// drawing: its bucket, which does not.
		exact = false
	}
	if !exact {
		k = scaleBucket(k)
	}
	if unscaled(k) {
		return 1, exact
	}
	return min(k, most), exact && k <= most
}

// watches returns the watches of p's owner, made the first time.
func (p *Painter) watches() *scaleWatches {
	st := p.owner
	if st == nil {
		return nil
	}
	if st.scales == nil {
		st.scales = &scaleWatches{}
	}
	return st.scales
}

// glyphSpace is where the glyphs of a text rasterize and lie: at scale
// pixels a DIP, q times the painter's, and then drawn q times smaller
// under its transform; or, where that only scales by q and moves, in the
// frame's own pixels (screen), at whole pixels as any text at rest, their
// places moved by dx, dy pixels, drawn without a transform at all.
type glyphSpace struct {
	scale, q float32
	dx, dy   float32
	screen   bool
}

// glyphSpace returns where the glyphs of l, laid out from (x, y), lie and
// rasterize under the painter's transform. moves tells that inline
// transforms move glyphs of l. Without a transform scaling them, they lie
// in the painter's pixels, at its scale, as they always did.
func (p *Painter) glyphSpace(l *text.Layout, x, y float32, moves bool) glyphSpace {
	if !p.transform.Set || l == nil {
		return glyphSpace{scale: p.scale, q: 1}
	}
	return p.glyphSpaceAt(x+l.Width/2, y+l.Height/2, l.Params.Style.FontSize(), moves)
}

// glyphSpaceAt is glyphSpace for glyphs of size DIPs an em around (x, y).
func (p *Painter) glyphSpaceAt(x, y, size float32, moves bool) glyphSpace {
	gs := glyphSpace{scale: p.scale, q: 1}
	m := p.transform
	if !m.Set {
		return gs
	}
	k := transformScale(m, x, y)
	if unscaled(k) && (p.owner == nil || p.owner.scales == nil) {
		return gs // turned or moved, as ever: nothing to watch
	}
	var w *scaleWatch
	if ws := p.watches(); ws != nil {
		w = &ws.text
	}
	most := float32(maxRasterScale)
	if px := size * p.scale; px > 0 {
		most = maxGlyphEm / px
	}
	q, exact := p.rasterScale(w, k, most, [2]float32{})
	if q == 1 {
		return gs
	}
	gs.scale, gs.q = p.scale*q, q
	if exact && !moves && m.A == q && m.D == q && m.B == 0 && m.C == 0 && !m.Perspective() {
		// Only scaled by q and moved: the glyphs lie where they show.
		gs.screen = true
		gs.dx, gs.dy = m.X*p.scale, m.Y*p.scale
	}
	return gs
}

// inlineTransform returns the transform of the glyph of cluster as
// Painter.inlineTransform moves it, in the painter's pixels, for the glyph
// rasterized in gs: none, unless moves tells that inline transforms move
// glyphs, where gs scales them.
func (gs glyphSpace) inlineTransform(p *Painter, cluster int, moves bool) scene.Affine {
	if gs.q != 1 && !moves {
		return scene.Affine{}
	}
	// In gs's pixels: as the inline element's transform in DIPs is in
	// the painter's pixels, at gs's scale rather than the painter's.
	return p.inlineTransform(cluster).Pixels(gs.scale)
}

// add adds op, of glyphs rasterized in gs, to p's scene: under p's
// transform, the glyphs q times smaller first, or without a transform for
// glyphs lying where they show.
func (gs glyphSpace) add(p *Painter, op scene.Op) {
	if gs.screen {
		p.s.Ops = append(p.s.Ops, op)
		return
	}
	if gs.q != 1 {
		op.Transform = scene.Scaling(1/gs.q, 1/gs.q)
	}
	p.add(op)
}

// maxLayerScaled is the largest side, in pixels, a layer drawn larger
// than at the window's scale (layerScale) takes: the window's at most, as
// one shows on screen, unless the layer is as large at the window's scale.
const maxLayerScaled = 4096

// layerScale returns at how much the window's scale e's kept layer (Cache,
// or its opacity's group), from, an area of its own space, renders: the
// scale e's transform draws it at, as its text (glyphSpace), and whether
// that is the scale itself and e, as in the frame before, stays where it
// was, its bitmap then drawn on whole pixels (layerOp). It is 1 for an
// effect's layer or a blur's, which render in the window's pixels.
func (p *Painter) layerScale(e *node, from Rect) (q float32, still bool) {
	m := e.world
	if !m.Set || e.effect != nil || e.blur > 0 {
		return 1, false
	}
	k := transformScale(m, e.x+e.w/2, e.y+e.h/2)
	if unscaled(k) && e.st.scales == nil {
		return 1, false
	}
	if e.st.scales == nil {
		e.st.scales = &scaleWatches{}
	}
	w := &e.st.scales.layer
	saved := p.owner
	p.owner = e.st
	side := max(from.W, from.H) * p.scale
	most := float32(maxRasterScale)
	if side > 0 {
		most = maxLayerScaled / side
	}
	at := [2]float32{m.X, m.Y}
	first := w.frame != p.rt.paintFrames
	q, exact := p.rasterScale(w, k, most, at)
	if q == 1 {
		p.owner = saved
		return 1, false
	}
	// Still where it was, or new: on whole pixels. Moved, it moves on
	// between them, and the frame after it stops puts it on them.
	still = exact && (w.last == 0 || w.lastAt == at)
	if exact && !still && first {
		p.rt.repainting = true
		p.asks()
	}
	p.owner = saved
	return q, still
}

// scaledLayerOp adds op, an image of a layer rendered q times the
// painter's scale (layerScale) from x0, y0 of its pixels, drawn q times
// smaller under the painter's transform: or, still, where that only
// scales it by q and moves it, on whole pixels.
func (p *Painter) scaledLayerOp(op scene.Op, q float32, still bool) {
	m := p.transform
	near := func(a, b float32) bool { return abs32(a-b) < 1e-4*q }
	if still && !m.Perspective() && near(m.A, q) && near(m.D, q) && near(m.B, 0) && near(m.C, 0) {
		// Its pixels the frame's: on them, as a text's glyphs are.
		x, y := m.Pixels(p.scale).Point(op.Rect.X/q, op.Rect.Y/q)
		op.Rect.X, op.Rect.Y = round(x), round(y)
		p.s.Ops = append(p.s.Ops, op)
		return
	}
	op.Transform = scene.Scaling(1/q, 1/q)
	p.add(op)
}
