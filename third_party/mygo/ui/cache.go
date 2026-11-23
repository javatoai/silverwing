package ui

import "github.com/egoist/mygo/internal/scene"

// Cache keeps what the element paints, with its children, in a bitmap of
// the GPU that frames draw as one image, as Flutter's RepaintBoundary and
// SwiftUI's drawingGroup do: for a still subtree that costs much to draw,
// as a card of gradients, blurred images and large shadows, under or
// around a drawing that moves. A frame redrawing where something above or
// next to it changed then draws the bitmap there, not each of its layers,
// and moving or rotating the element (Translate, Rotate, AnimatePose)
// moves the bitmap, not what it holds.
//
// The bitmap holds the element's box, and what it and those inside it
// paint around it: shadows, focus rings, and children beyond the box that
// it does not clip, as Draw callbacks paint (within the box and the
// shadows' reach), in the pixels it shows on: at the window's scale times
// the scale the element's transform draws it at (Scale, AnimatePose,
// Transition's ScaleContent), so that scaled up it is as sharp as the
// subtree laid out larger. At rest it is drawn at that scale itself, on
// whole pixels where the transform only scales and moves it; while the
// scale changes, as an animation grows it, at steps of about 9% (at or
// above the scale), drawn again only as the scale crosses one, and once
// more at the scale itself in the frame after it stops. It is drawn
// again, too, when what the subtree paints changes: built anew with other
// content, laid out at another size, at another window scale or in
// another theme, or as something inside it moves on its own
// (Painter.AnimationFrame, a pose, a transition), as then it is every
// frame. Painting the window still paints the subtree, to tell; frames
// that only paint the drawings that asked for them
// (Painter.AnimationFrame, Painter.After) do not.
//
// As in an effect's bitmap (Effect), its text is smoothed in grayscale
// where the subtree paints no opaque background under it, web views
// inside it do not show, and a subtree larger than 8192 pixels across is
// drawn as it is. A bitmap drawn larger than the window's scale is at
// most 4096 pixels across, and 8 times the window's scale: beyond, it is
// stretched as before. Turned, skewed or in perspective, the bitmap is
// resampled, and shows a little softer than the shapes it holds would.
func (e *node) Cache() *node { e.cache = keptLayer{}; return e }

// layerKeeper renders an element's layer only when what it paints
// changed (Cache): the methods are linked into apps that use it alone.
type layerKeeper interface {
	// area returns the area of e's layer, in its own space: its box and
	// what it and those inside it paint around it.
	area(e *node) Rect
	// draw draws cv as Canvas.drawPart does, unless what fn paints is what
	// it painted last.
	draw(cv *Canvas, p *Painter, scale float32, w, h int, fn func(cp *Painter))
}

type keptLayer struct{}

func (keptLayer) area(e *node) Rect {
	box := Rect{e.x, e.y, e.w, e.h}
	grown := func(r Rect, m float32) Rect { return Rect{r.X - m, r.Y - m, r.W + 2*m, r.H + 2*m} }
	// As setInk: the shadows' margin, and as much again as a focus ring.
	area := grown(box, e.shadowMargin()+4)
	if e.flags&(flagClipX|flagClipY|flagScrollX|flagScrollY) != 0 {
		return area // all inside within the box
	}
	inv, ok := e.world.Inverse()
	if !ok {
		return area
	}
	var walk func(n *node, clip Rect, clipped bool)
	walk = func(n *node, clip Rect, clipped bool) {
		for ch := n.first; ch != nil; ch = ch.next {
			if ch.flags&flagInvisible != 0 || ch.hidden || ch.isInline() {
				continue
			}
			// In e's space: e's transform taken off theirs.
			m := inv.Mul(ch.world)
			r := transformRect(m, grown(Rect{ch.x, ch.y, ch.w, ch.h}, ch.shadowMargin()+4))
			if clipped {
				r = intersect(r, clip)
			}
			if r.W > 0 && r.H > 0 {
				area = union(area, r)
			}
			if ch.flags&(flagClipX|flagClipY|flagScrollX|flagScrollY) != 0 {
				cr, _ := ch.clipRect()
				c := transformRect(m, cr)
				if clipped {
					c = intersect(c, clip)
				}
				walk(ch, c, true)
			} else {
				walk(ch, clip, clipped)
			}
		}
	}
	walk(e, Rect{}, false)
	return area
}

// layerCache is what a canvas kept by Cache holds to tell whether what it
// is drawn with changed: the last scene, which differ remembers, and
// another to paint the next into.
type layerCache struct {
	spare  *scene.Scene
	differ scene.Differ
}

func (keptLayer) draw(cv *Canvas, p *Painter, scale float32, w, h int, fn func(cp *Painter)) {
	c := cv.cache
	if c == nil {
		c = &layerCache{spare: &scene.Scene{}}
		cv.cache = c
	}
	last := cv.img.Scene
	cv.img.Scene = c.spare
	cv.paint(p, scale, w, h, fn)
	s := cv.img.Scene
	c.spare = last
	// Kept, the scene is that of the frame all the same, whose glyphs the
	// atlases hold, for a renderer drawing the canvas anew.
	damage, known := c.differ.Diff(s)
	c.differ.Remember(s)
	if !known || len(damage) > 0 || s.Scale != last.Scale || s.Drawn != last.Drawn {
		cv.drawn(p)
	}
}
