// Package svg parses SVG documents and draws them into pixels, for the
// icons and pictures of package ui, in Go alone.
//
// It draws paths and the basic shapes, groups, nested svg elements, use
// and symbol elements, transforms, fills and strokes (with joins, caps
// and dashes) of colors, currentColor and linear and radial gradients,
// opacity, clip paths and masks, markers, text with the Fonts of the
// caller, and style sheets of type, class, id and :root selectors
// joined by descendant and child combinators, with custom properties and
// prefers-color-scheme media queries. Embedded images, patterns and
// filters are not drawn: Unsupported lists what a document has that is
// not. ParseBasic leaves out text, markers and style sheets, so that a
// program whose SVGs are its own simple icons does not link them.
package svg

import (
	"image"
	"image/color"
	"slices"
	"strings"
	"sync"
)

// renderers keeps renderers and their memory between drawings.
var renderers = sync.Pool{New: func() any { return new(renderer) }}

// Doc is a parsed SVG document. Draw may draw it from any goroutine.
type Doc struct {
	root *node
	// dark is the document in the dark color scheme, when its style
	// sheets ask for prefers-color-scheme.
	dark *node
	view box
	par  aspect
	w, h float64
	// current is true when something is painted with currentColor.
	current     bool
	unsupported []string
	// ext draws its text, for a document Parse parsed.
	ext *extras
}

// extras are what Parse does that ParseBasic does not. A program links
// them only when it calls Parse: each app with native UI parses icons
// with ParseBasic, and most never draw text or markers from an SVG.
type extras struct {
	text     func(c *compiler, e *elem, st *style) *textNode
	drawText func(r *renderer, t *textNode, m matrix, dst *layer, alpha float64)
	markers  func(c *compiler, e *elem, pa *path, st *style, sh *shape) []*node
}

// Options are how Draw draws a document.
type Options struct {
	// Current is the color currentColor stands for where the document
	// does not set the color property.
	Current color.NRGBA
	// Stretch stretches the viewBox to the pixels, whatever the
	// document's preserveAspectRatio says.
	Stretch bool
	// Dark draws the document in the dark color scheme, which media
	// queries for prefers-color-scheme: dark match; the light one
	// otherwise.
	Dark bool
	// Fonts lays out and rasterizes text; without it, no text is drawn.
	Fonts Fonts
}

// Parse parses an SVG document.
func Parse(data []byte) (*Doc, error) {
	root, err := parseXML(data)
	if err != nil {
		return nil, err
	}
	ext := &extras{text: (*compiler).text, drawText: (*renderer).text, markers: (*compiler).markers}
	rep := &report{}
	sh := parseSheets(root, rep)
	cascade(root, sh, false, rep)
	d := newCompiler(root, rep, ext).doc()
	if sh.scheme {
		cascade(root, sh, true, rep)
		dark := newCompiler(root, rep, ext).doc()
		d.dark, d.current = dark.root, d.current || dark.current
		if d.dark == nil {
			d.dark = &node{m: identity} // nothing to draw
		}
	}
	reportElems(root, rep)
	slices.Sort(rep.items)
	d.unsupported, d.ext = rep.items, ext
	return d, nil
}

// ParseBasic parses an SVG document as Parse does, but for its text,
// markers and style elements, which it leaves out (Unsupported does not
// list them), and the var() functions of its style attributes, which it
// treats as errors. It is for icons that are part of the program: those
// do without, and a program that never calls Parse links none of them.
func ParseBasic(data []byte) (*Doc, error) {
	root, err := parseXML(data)
	if err != nil {
		return nil, err
	}
	rep := &report{}
	walk(root, func(e *elem) {
		if e.name == "#text" {
			return
		}
		for name, v := range e.attrs {
			if properties[name] {
				e.props = append(e.props, decl{name, strings.TrimSpace(v)})
			}
		}
		if s, ok := e.attrs["style"]; ok {
			e.props = append(e.props, parseDecls(s, rep)...)
		}
	})
	d := newCompiler(root, rep, nil).doc()
	reportElems(root, rep)
	slices.Sort(rep.items)
	d.unsupported = rep.items
	return d, nil
}

// Unsupported lists what the document has that Draw leaves out: elements
// as "<pattern>", properties as "filter", at-rules as "@font-face", and
// selectors as "selector a + b". It is empty when Draw draws all of it.
func (d *Doc) Unsupported() []string { return slices.Clone(d.unsupported) }

// UsesColorScheme reports whether the document draws differently in the
// dark color scheme (Options.Dark).
func (d *Doc) UsesColorScheme() bool { return d.dark != nil }

// Size returns the document's size in pixels: its width and height, or
// those of its viewBox.
func (d *Doc) Size() (w, h float64) { return d.w, d.h }

// UsesCurrentColor reports whether the color Draw takes for currentColor
// changes what it draws.
func (d *Doc) UsesCurrentColor() bool { return d.current }

// Draw draws the document over the pixels of dst, fitting its viewBox in
// them as its preserveAspectRatio says, or as o says.
func (d *Doc) Draw(dst *image.RGBA, o Options) {
	b := dst.Bounds()
	a := d.par
	if o.Stretch {
		a = aspect{none: true}
	}
	d.draw(dst, &o, viewBoxTransform(d.view, a, float64(b.Dx()), float64(b.Dy())))
}

// DrawRotated draws the document as Draw does in a box w×h pixels at the
// center of dst, turned by degrees clockwise around it.
func (d *Doc) DrawRotated(dst *image.RGBA, o Options, w, h, degrees float64) {
	b := dst.Bounds()
	m := translate(float64(b.Dx())/2, float64(b.Dy())/2).mul(rotation(degrees)).mul(translate(-w/2, -h/2))
	d.draw(dst, &o, m.mul(viewBoxTransform(d.view, d.par, w, h)))
}

func (d *Doc) draw(dst *image.RGBA, o *Options, m matrix) {
	b := dst.Bounds()
	w, h := b.Dx(), b.Dy()
	root := d.root
	if o.Dark && d.dark != nil {
		root = d.dark
	}
	if w <= 0 || h <= 0 || root == nil {
		return
	}
	r := renderers.Get().(*renderer)
	defer renderers.Put(r)
	if r.w != w || r.h != h {
		r.w, r.h, r.free = w, h, nil
	}
	current := o.Current
	r.current = rgba{float64(current.R) / 255, float64(current.G) / 255, float64(current.B) / 255, float64(current.A) / 255}
	r.fonts, r.ext = o.Fonts, d.ext
	defer func() { r.fonts, r.ext = nil, nil }()
	l := r.layer()
	defer r.release(l)
	r.node(root, m, l, 1)
	dr := l.dirty
	for y := dr.Min.Y; y < dr.Max.Y; y++ {
		src := l.pix[(y*w+dr.Min.X)*4 : (y*w+dr.Max.X)*4]
		out := dst.Pix[dst.PixOffset(b.Min.X+dr.Min.X, b.Min.Y+y):]
		for i := 0; i < len(src); i += 4 {
			if a := src[i+3]; a == 255 {
				copy(out[i:i+4], src[i:i+4])
			} else if a > 0 {
				over(out[i:i+4], uint32(src[i]), uint32(src[i+1]), uint32(src[i+2]), uint32(a))
			}
		}
	}
}
