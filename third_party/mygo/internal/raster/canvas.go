package raster

import (
	"image"

	"github.com/egoist/mygo/internal/scene"
)

// canvasScratch is shared by every canvas a renderer redraws. EachCanvas
// visits children before parents, so only one scratch image is needed.
// Keep it separate from the last frame and its differ: drawing a canvas
// must not replace the damage history of the window that shows it.
type canvasScratch struct {
	image Image
	diff  scene.Differ
	rs    []renderer
}

// drawCanvases draws into their pixels the canvases s draws anew or shows,
// those changed since, each before the canvases and the scene that show
// it.
func (r *Renderer) drawCanvases(s *scene.Scene) {
	s.EachCanvas(func(c *scene.Image) {
		if c.PixVersion == c.Version() && len(c.Pix) == 4*c.W*c.H {
			return
		}
		cs := c.Scene
		cs.Width, cs.Height = c.W, c.H
		if r.canvas == nil {
			r.canvas = &canvasScratch{}
		}
		scratch := r.canvas
		scratch.image.Resize(c.W, c.H)
		scratch.diff.Reach = 1
		scratch.diff.Diff(cs)
		// Effects and folded ops stay local. Reuse only the band renderers'
		// scratch, dropping their scene and image references after the draw.
		d := drawer{rs: scratch.rs}
		defer func() {
			for i := range d.rs {
				r := &d.rs[i]
				r.s, r.dst, r.blurred = nil, nil, nil
				// Popped clips remain in the backing array and can own masks.
				clear(r.clips[:cap(r.clips)])
				r.clips = r.clips[:0]
			}
			scratch.rs = d.rs
		}()
		d.draw(&scratch.image, cs, image.Rect(0, 0, c.W, c.H), scratch.diff.Bounds())
		if len(c.Pix) != 4*c.W*c.H {
			c.Pix = make([]byte, 4*c.W*c.H)
		}
		// From BGRA to the RGBA of images.
		for i := 0; i+3 < len(scratch.image.Pix); i += 4 {
			p := scratch.image.Pix[i : i+4]
			c.Pix[i], c.Pix[i+1], c.Pix[i+2], c.Pix[i+3] = p[2], p[1], p[0], p[3]
		}
		c.PixVersion = c.Version()
	})
}
