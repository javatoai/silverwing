package svg

import (
	"image"
	"math"
	"strings"
)

// Font is the font of a run of text.
type Font struct {
	// Family is a list of families, as CSS's font-family writes it.
	Family string
	// Size is the em size in pixels.
	Size float64
	// Weight is from 1 to 1000: 400 is normal, 700 bold.
	Weight int
	Italic bool
	// LetterSpacing adds pixels after every character.
	LetterSpacing float64
}

// Fonts lays out and rasterizes lines of text, as Draw draws the text of
// documents with them: package ui gives the system's text stack. Draw
// calls it from the goroutine drawing.
type Fonts interface {
	// Measure returns how far a line of text s in font f moves the pen,
	// and the font's ascent and descent, in pixels.
	Measure(s string, f Font) (advance, ascent, descent float64)
	// Rasterize returns the coverage of a line of text s in font f, its
	// pen on the baseline x pixels right of the left edge of a pixel (0
	// ≤ x < 1), or with stroke > 0 that of a stroke stroke pixels wide
	// along the outlines of its glyphs.
	Rasterize(s string, f Font, x, stroke float64) Bitmap
}

// Bitmap is the coverage of a line of text: W×H bytes, row by row, the
// first Left pixels right of the pixel the pen starts in and Top pixels
// below the baseline (Top is usually negative).
type Bitmap struct {
	Left, Top, W, H int
	Pix             []byte
}

// textNode is a text element: runs of characters, each in one style and
// at one position, which Draw lays out with the Fonts it is given.
type textNode struct {
	runs []textRun
}

// textRun is a run of a text element's characters.
type textRun struct {
	s    string
	font Font // its size in user units
	// x and y place the run, where absX and absY say they do, and dx and
	// dy move it; otherwise it goes on from the run before.
	x, y, dx, dy float64
	absX, absY   bool
	// anchor is the text-anchor of the chunk of text the run starts, if
	// it is placed.
	anchor   uint8
	baseline uint8
	hidden   bool
	shape    shape // its paint; no path
}

// tchar is a character of a text element, in the style of the element it
// is in.
type tchar struct {
	r     rune
	st    *style
	space bool // a space that white space collapses
}

// text compiles a text element of style st.
func (c *compiler) text(e *elem, st *style) *textNode {
	var chars []tchar
	type placed struct {
		el    *elem
		start int
	}
	var lists []placed
	var visit func(el *elem, st *style, preserve bool)
	visit = func(el *elem, st *style, preserve bool) {
		if v, ok := el.attrs["xml:space"]; ok {
			preserve = v == "preserve"
		}
		preserve = preserve || st.preserve
		lists = append(lists, placed{el, len(chars)})
		for _, k := range el.kids {
			switch k.name {
			case "#text":
				for _, r := range k.text {
					switch r {
					case '\n', '\r', '\t':
						r = ' '
					}
					if r == ' ' && !preserve && (len(chars) == 0 || chars[len(chars)-1].space) {
						continue
					}
					chars = append(chars, tchar{r, st, r == ' ' && !preserve})
				}
			case "tspan", "a":
				if c.budget <= 0 {
					return
				}
				c.budget--
				kst := c.style(k, st)
				if kst.display {
					p := new(style)
					*p = kst
					visit(k, p, preserve)
				}
			case "textPath":
				c.rep.add("<textPath>")
			}
		}
	}
	visit(e, st, false)
	for len(chars) > 0 && chars[len(chars)-1].space {
		chars = chars[:len(chars)-1]
	}
	if len(chars) == 0 {
		return nil
	}
	// The positions the elements give their characters, inner elements
	// over outer ones.
	n := len(chars)
	xs, ys := make([]float64, n), make([]float64, n)
	for i := range xs {
		xs[i], ys[i] = math.NaN(), math.NaN()
	}
	dxs, dys := make([]float64, n), make([]float64, n)
	for _, p := range lists {
		for k, list := range [4][]float64{xs, ys, dxs, dys} {
			base := c.vw
			if k%2 == 1 {
				base = c.vh
			}
			for i, v := range c.lengths(p.el.attrs[[4]string{"x", "y", "dx", "dy"}[k]], base) {
				if p.start+i < n {
					list[p.start+i] = v
				}
			}
		}
		if p.el.attrs["rotate"] != "" {
			c.rep.add("rotate")
		}
	}
	if math.IsNaN(xs[0]) {
		xs[0] = 0
	}
	if math.IsNaN(ys[0]) {
		ys[0] = 0
	}
	t := &textNode{}
	var b strings.Builder
	for i, ch := range chars {
		placedHere := !math.IsNaN(xs[i]) || !math.IsNaN(ys[i]) || dxs[i] != 0 || dys[i] != 0
		if i == 0 || placedHere || ch.st != chars[i-1].st {
			if len(t.runs) > 0 {
				t.runs[len(t.runs)-1].s = b.String()
				b.Reset()
			}
			t.runs = append(t.runs, c.textRun(ch.st, xs[i], ys[i], dxs[i], dys[i]))
		}
		b.WriteRune(ch.r)
	}
	t.runs[len(t.runs)-1].s = b.String()
	return t
}

// textRun starts a run of characters of style st at a position.
func (c *compiler) textRun(st *style, x, y, dx, dy float64) textRun {
	family := st.fontFamily
	if family == "" {
		family = "serif" // as browsers draw SVG
	}
	r := textRun{
		font: Font{Family: family, Size: st.fontSize, Weight: st.fontWeight, Italic: st.italic, LetterSpacing: st.letterSpacing},
		x:    x, y: y, dx: dx, dy: dy, absX: !math.IsNaN(x), absY: !math.IsNaN(y),
		anchor: st.anchor, baseline: st.baseline, hidden: !st.visible,
		shape: shape{
			fill: c.paint(st.fill, st), stroke: c.paint(st.stroke, st),
			fillOpacity: st.fillOpacity, strokeOpacity: st.strokeOpacity,
			width: st.strokeWidth, strokeFirst: st.strokeFirst,
		},
	}
	if st.align != baselineAuto {
		r.baseline = st.align
	}
	return r
}

// lengths parses a list of lengths, percentages of base, up to the first
// in error.
func (c *compiler) lengths(v string, base float64) []float64 {
	var out []float64
	for _, f := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r' }) {
		l, ok := parseLength(f, base)
		if !ok {
			break
		}
		out = append(out, l)
	}
	return out
}

// textBox is where a run of text is laid out: its pen on the baseline,
// its advance, and its ascent and descent, in user units.
type textBox struct {
	pen                    point
	advance, ascent, depth float64
}

// layout lays out the runs of t, measured by measure in user units, into
// boxes, and returns the box around them.
func (t *textNode) layout(measure func(r *textRun) (advance, ascent, descent float64), boxes []textBox) box {
	var pen point
	chunk := 0
	anchor := func(end int) {
		if end <= chunk {
			return
		}
		k := 0.0
		switch t.runs[chunk].anchor {
		case anchorMiddle:
			k = 0.5
		case anchorEnd:
			k = 1
		}
		shift := (pen.x - boxes[chunk].pen.x) * k
		for i := chunk; i < end; i++ {
			boxes[i].pen.x -= shift
		}
	}
	for i := range t.runs {
		r := &t.runs[i]
		if (r.absX || r.absY) && i > 0 {
			anchor(i)
			chunk = i
		}
		if r.absX {
			pen.x = r.x
		}
		if r.absY {
			pen.y = r.y
		}
		pen.x += r.dx
		pen.y += r.dy
		adv, asc, desc := measure(r)
		size := r.font.Size
		var shift float64 // of the baseline, down
		switch r.baseline {
		case baselineMiddle:
			shift = 0.25 * size // half an x-height
		case baselineCentral:
			shift = (asc - desc) / 2
		case baselineHanging:
			shift = 0.8 * asc
		case baselineTop:
			shift = asc
		case baselineBottom:
			shift = -desc
		case baselineMath:
			shift = 0.5 * asc
		}
		boxes[i] = textBox{point{pen.x, pen.y + shift}, adv, asc, desc}
		pen.x += adv
	}
	anchor(len(t.runs))
	var b box
	for i, x := range boxes {
		r := box{x.pen.x, x.pen.y - x.ascent, x.advance, x.ascent + x.depth}
		if i == 0 {
			b = r
		} else {
			b = b.union(r)
		}
	}
	return b
}

// estimate returns the box around the text, with characters of usual
// widths, for what is in its objectBoundingBox units before Draw
// measures it.
func (t *textNode) estimate() box {
	return t.layout(func(r *textRun) (float64, float64, float64) {
		n := float64(len([]rune(r.s)))
		return n * (0.55*r.font.Size + r.font.LetterSpacing), 0.8 * r.font.Size, 0.2 * r.font.Size
	}, make([]textBox, len(t.runs)))
}

// maxFontPixels is the largest size text is rasterized at: larger text is
// rasterized at it and scaled.
const maxFontPixels = 512

// text draws a text element, mapped to pixels by m, into dst with an
// opacity.
func (r *renderer) text(t *textNode, m matrix, dst *layer, alpha float64) {
	fonts := r.fonts
	if fonts == nil {
		return
	}
	// The scale text is rasterized at, in pixels per user unit.
	k := math.Sqrt(math.Abs(m[0]*m[3] - m[1]*m[2]))
	if !(k > 0) || math.IsInf(k, 0) {
		return
	}
	scaled := func(f Font) (Font, float64) {
		s := k
		if f.Size*s > maxFontPixels {
			s = maxFontPixels / f.Size
		}
		f.Size *= s
		f.LetterSpacing *= s
		return f, s
	}
	if cap(r.boxes) < len(t.runs) {
		r.boxes = make([]textBox, len(t.runs))
	}
	boxes := r.boxes[:len(t.runs)]
	bbox := t.layout(func(run *textRun) (float64, float64, float64) {
		f, s := scaled(run.font)
		if f.Size <= 0 {
			return 0, 0, 0
		}
		adv, asc, desc := fonts.Measure(run.s, f)
		return adv / s, asc / s, desc / s
	}, boxes)
	for i := range t.runs {
		run := &t.runs[i]
		f, s := scaled(run.font)
		if run.hidden || f.Size < 0.5 {
			continue
		}
		sh := &run.shape
		// Runs off the canvas, with room for glyphs reaching past their
		// box, are left out, and so are those too large to rasterize.
		x := &boxes[i]
		pad := run.font.Size + sh.width
		on := box{x.pen.x - pad, x.pen.y - x.ascent - pad, x.advance + 2*pad, x.ascent + x.depth + 2*pad}.transform(m)
		if on.x > float64(r.w) || on.y > float64(r.h) || on.x+on.w < 0 || on.y+on.h < 0 || on.w*on.h*s*s/(k*k) > 1<<24 {
			continue
		}
		draw := func(p paint, opacity, stroke float64) {
			if p.kind == paintNone {
				return
			}
			// The pen in pixels, and the transform from the pixels of the
			// bitmap, at the left edge of the pixel the pen starts in, to
			// those of the canvas.
			pen := m.apply(boxes[i].pen)
			upright := m[1] == 0 && m[2] == 0 && m[0] > 0 && math.Abs(m[0]-m[3]) < 1e-9 && s == k
			var bm matrix
			var x float64
			if upright {
				x = pen.x - math.Floor(pen.x)
				bm = translate(math.Floor(pen.x), math.Round(pen.y))
			} else {
				bm = m.mul(translate(boxes[i].pen.x, boxes[i].pen.y)).mul(scaling(1/s, 1/s))
			}
			b := fonts.Rasterize(run.s, f, x, stroke*s)
			if b.W <= 0 || b.H <= 0 || len(b.Pix) < b.W*b.H {
				return
			}
			if rect, ok := r.coverBitmap(&b, bm, upright); ok {
				r.paint(dst, rect, p, alpha*opacity, m, bbox)
			}
		}
		stroke := func() {
			if sh.width > 0 {
				draw(sh.stroke, sh.strokeOpacity, sh.width)
			}
		}
		if sh.strokeFirst {
			stroke()
		}
		draw(sh.fill, sh.fillOpacity, 0)
		if !sh.strokeFirst {
			stroke()
		}
	}
}

// coverBitmap puts the coverage of a bitmap of text into r.cov, its
// pixels mapped to the canvas's by m, and returns the rectangle of the
// canvas it covers. An upright m moves the bitmap by whole pixels;
// others are sampled.
func (r *renderer) coverBitmap(b *Bitmap, m matrix, upright bool) (image.Rectangle, bool) {
	src := image.Rect(b.Left, b.Top, b.Left+b.W, b.Top+b.H)
	var rect image.Rectangle
	if upright {
		rect = src.Add(image.Pt(int(m[4]), int(m[5])))
	} else {
		bb := box{float64(src.Min.X), float64(src.Min.Y), float64(b.W), float64(b.H)}.transform(m)
		rect = image.Rect(int(math.Floor(bb.x)), int(math.Floor(bb.y)), int(math.Ceil(bb.x+bb.w)), int(math.Ceil(bb.y+bb.h)))
	}
	rect = rect.Intersect(image.Rect(0, 0, r.w, r.h))
	if rect.Empty() {
		return rect, false
	}
	w, h := rect.Dx(), rect.Dy()
	if cap(r.cov) < w*h {
		r.cov = make([]byte, w*h)
	}
	r.cov = r.cov[:w*h]
	if upright {
		ox, oy := int(m[4])+b.Left, int(m[5])+b.Top
		for y := range h {
			row := b.Pix[(rect.Min.Y+y-oy)*b.W+rect.Min.X-ox:]
			copy(r.cov[y*w:(y+1)*w], row[:w])
		}
		return rect, true
	}
	inv, ok := m.invert()
	if !ok {
		return rect, false
	}
	at := func(x, y int) float64 {
		if x < 0 || y < 0 || x >= b.W || y >= b.H {
			return 0
		}
		return float64(b.Pix[y*b.W+x])
	}
	for y := range h {
		for x := range w {
			// Bilinearly, between the centers of the bitmap's pixels.
			q := inv.apply(point{float64(rect.Min.X+x) + 0.5, float64(rect.Min.Y+y) + 0.5})
			u, v := q.x-float64(b.Left)-0.5, q.y-float64(b.Top)-0.5
			x0, y0 := math.Floor(u), math.Floor(v)
			fx, fy := u-x0, v-y0
			i, j := int(x0), int(y0)
			c := (at(i, j)*(1-fx)+at(i+1, j)*fx)*(1-fy) + (at(i, j+1)*(1-fx)+at(i+1, j+1)*fx)*fy
			r.cov[y*w+x] = uint8(c + 0.5)
		}
	}
	return rect, true
}
