package ui

import (
	"encoding/binary"
	"hash/maphash"
	"image"
	"image/color"
	"math"
	"sync/atomic"

	"github.com/egoist/mygo/internal/scene"
	"github.com/egoist/mygo/internal/svg"
	"github.com/egoist/mygo/internal/text"
)

// SVG is a vector image: an icon, a logo, an illustration. It stays sharp
// at any size. Parse it once, not in the view, and show it with Icon, in
// the color of the text, or with Image, in its own colors:
//
//	//go:embed icons/save.svg
//	var saveSVG []byte
//
//	var save = ui.MustParseSVG(saveSVG)
//
//	ui.Icon(c, save)
//
// MyGo draws the paths and basic shapes of SVG, with groups, use and
// symbol elements, transforms, colors, gradients, strokes and their
// dashes, opacity, clip paths, masks, markers, text in the system's
// fonts, and style sheets of type, class, id and :root selectors with
// descendant and child combinators, custom properties (var()) and
// prefers-color-scheme media queries, which follow the theme (WithDark).
// It leaves out embedded images, patterns and filters, which Unsupported
// lists.
type SVG struct {
	imageMark
	doc  *svg.Doc
	id   uint64
	w, h float32
	// scheme is the color scheme the SVG is drawn in: schemeTheme's,
	// light or dark.
	scheme uint8
	// fonts draws its text. ParseSVG sets it, and parseIcon does not, so
	// that an app showing MyGo's own icons alone does not link it.
	fonts func(*text.System) svg.Fonts
}

// Color schemes of SVGs.
const (
	schemeTheme uint8 = iota
	schemeLight
	schemeDark
)

var lastSVG atomic.Uint64

// ParseSVG parses an SVG document.
func ParseSVG(data []byte) (*SVG, error) {
	d, err := svg.Parse(data)
	if err != nil {
		return nil, err
	}
	w, h := d.Size()
	return &SVG{doc: d, id: lastSVG.Add(1), w: float32(w), h: float32(h), fonts: newSVGFonts}, nil
}

// parseIcon parses an icon of MyGo's own, which has no text, markers or
// style elements, without linking what draws those into every app (see
// svg.ParseBasic).
func parseIcon(data []byte) *SVG {
	d, err := svg.ParseBasic(data)
	if err != nil {
		panic("ui: " + err.Error())
	}
	w, h := d.Size()
	return &SVG{doc: d, id: lastSVG.Add(1), w: float32(w), h: float32(h)}
}

// MustParseSVG is ParseSVG for SVGs that are part of the program, such as
// embedded files: it panics when one is in error.
func MustParseSVG(data []byte) *SVG {
	s, err := ParseSVG(data)
	if err != nil {
		panic("ui: " + err.Error())
	}
	return s
}

// ParsePath makes a path of SVG path data, as the d attribute of an SVG
// path element holds it, in its units as DIPs: an icon's outline to fill
// with a gradient, stroke, draw in with Trim or dash with Dash, as any
// path a Painter draws.
//
//	check := ui.MustParsePath("M4 12l5 5L20 6")
//	p.Transform(ui.Scaling(2, 2), r.X, r.Y, func() {
//		p.StrokePath(check.Trim(0, drawn), 2, c.Theme().Accent)
//	})
//
// Its arcs and quadratic curves become cubic curves. Path data in error
// makes the path up to the error, as SVG draws it, which ParsePath returns
// with the error.
func ParsePath(d string) (*Path, error) {
	ops, pts, err := svg.ParsePath(d)
	p := &Path{}
	pt := func(i int) (float32, float32) { return float32(pts[i][0]), float32(pts[i][1]) }
	k := 0
	for _, op := range ops {
		switch op {
		case svg.PathMove:
			p.MoveTo(pt(k))
			k++
		case svg.PathLine:
			p.LineTo(pt(k))
			k++
		case svg.PathCubic:
			c1x, c1y := pt(k)
			c2x, c2y := pt(k + 1)
			x, y := pt(k + 2)
			p.CubeTo(c1x, c1y, c2x, c2y, x, y)
			k += 3
		case svg.PathClose:
			p.Close()
		}
	}
	return p, err
}

// MustParsePath is ParsePath for path data that is part of the program:
// it panics when the data is in error.
func MustParsePath(d string) *Path {
	p, err := ParsePath(d)
	if err != nil {
		panic("ui: " + err.Error())
	}
	return p
}

// Size returns the size the SVG says it has, its width and height or
// those of its viewBox, which Image shows as DIPs.
func (s *SVG) Size() (w, h float32) { return s.imageSize() }

// Unsupported lists what the SVG has that MyGo leaves out when it draws
// it, sorted: elements as "<pattern>", properties as "filter", at-rules
// as "@font-face", media features as "@media (min-width: 600px)" and
// selectors as "selector a + b". It is empty when MyGo draws all of the
// SVG. An app showing SVGs it did not make, as in a diff, can show a
// note, or another view, where it is not:
//
//	if missing := s.Unsupported(); len(missing) > 0 {
//		ui.Text(c, "Not shown: "+strings.Join(missing, ", ")).Muted()
//	}
func (s *SVG) Unsupported() []string {
	if s == nil {
		return nil
	}
	return s.doc.Unsupported()
}

// WithDark returns the SVG drawn in the dark color scheme when dark, and
// in the light one otherwise, wherever it shows: its style sheets' media
// queries for prefers-color-scheme: dark match the dark one. An SVG
// ParseSVG returns follows the theme (Theme.Dark) where Image shows it,
// as a page follows the system's appearance, and Image (the method)
// draws it light; this one does not, for an app whose own setting, or
// whose pictures' backgrounds, say otherwise. Both share the parsed
// document.
func (s *SVG) WithDark(dark bool) *SVG {
	if s == nil {
		return nil
	}
	c := *s
	c.id = lastSVG.Add(1)
	c.scheme = schemeLight
	if dark {
		c.scheme = schemeDark
	}
	return &c
}

// darkIn reports whether the SVG draws in the dark color scheme under a
// theme that is dark or not, where that changes what it draws.
func (s *SVG) darkIn(themeDark bool) bool {
	if !s.doc.UsesColorScheme() {
		return false
	}
	return s.scheme == schemeDark || s.scheme == schemeTheme && themeDark
}

// options returns how to draw the SVG, with current for currentColor,
// in the dark color scheme if dark, with the text of sys.
func (s *SVG) options(current color.NRGBA, dark bool, sys *text.System) svg.Options {
	o := svg.Options{Current: current, Dark: dark}
	if s.fonts != nil {
		o.Fonts = s.fonts(sys)
	}
	return o
}

// svgFonts lays out and rasterizes the text of SVGs with the system's
// text stack, as elements' text, each line on the CPU.
type svgFonts struct{ sys *text.System }

func newSVGFonts(sys *text.System) svg.Fonts { return svgFonts{sys} }

// line shapes a line of text, uncached: Draw may run on any goroutine,
// and pictures are drawn again rarely.
func (f svgFonts) line(s string, font svg.Font) *text.Line {
	l := f.sys.Shape(text.Params{Text: s, NoWrap: true, KeepSpaces: true, Style: text.Style{
		Family: font.Family, Size: float32(font.Size), Weight: font.Weight, Italic: font.Italic, LetterSpacing: float32(font.LetterSpacing),
	}})
	if len(l.Lines) == 0 {
		return nil
	}
	return &l.Lines[0]
}

func (f svgFonts) Measure(s string, font svg.Font) (advance, ascent, descent float64) {
	line := f.line(s, font)
	if line == nil {
		return 0, 0, 0
	}
	for _, g := range line.Glyphs {
		advance += float64(g.Advance)
	}
	return advance, float64(line.Ascent), float64(line.Descent)
}

func (f svgFonts) Rasterize(s string, font svg.Font, x, stroke float64) svg.Bitmap {
	line := f.line(s, font)
	if line == nil {
		return svg.Bitmap{}
	}
	type glyph struct {
		r   image.Rectangle
		pix []byte
	}
	var glyphs []glyph
	var bounds image.Rectangle
	for _, g := range line.Glyphs {
		if g.Object || g.Font == nil {
			continue
		}
		pen := x + float64(line.X+g.X)
		ix := math.Floor(pen)
		left, top, w, h, pix := f.sys.GlyphBitmap(g.Font, g.ID, 1, float32(pen-ix), float32(stroke))
		if w <= 0 || h <= 0 {
			continue
		}
		dy := int(math.Round(float64(g.Y - line.Baseline)))
		r := image.Rect(int(ix)+left, top+dy, int(ix)+left+w, top+dy+h)
		glyphs = append(glyphs, glyph{r, pix})
		bounds = bounds.Union(r)
	}
	if bounds.Empty() {
		return svg.Bitmap{}
	}
	// The glyphs' coverage added, as a run's.
	w := bounds.Dx()
	pix := make([]byte, w*bounds.Dy())
	for _, g := range glyphs {
		gw := g.r.Dx()
		for y := range g.r.Dy() {
			dst := pix[(g.r.Min.Y-bounds.Min.Y+y)*w+g.r.Min.X-bounds.Min.X:]
			for x, v := range g.pix[y*gw : (y+1)*gw] {
				dst[x] = uint8(min(int(dst[x])+int(v), 255))
			}
		}
	}
	return svg.Bitmap{Left: bounds.Min.X, Top: bounds.Min.Y, W: w, H: bounds.Dy(), Pix: pix}
}

// Image draws the SVG in its own colors to an image of w by h pixels,
// fitting it in them as its preserveAspectRatio says, with current for
// currentColor, black when nil: for what takes pixels rather than an
// element, as mygo.NewMenuIconSVG draws a menu's icon. Painter.Icon and
// Icon draw it in the window, at the display's scale.
func (s *SVG) Image(w, h int, current color.Color) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, max(w, 0), max(h, 0)))
	if s == nil || w <= 0 || h <= 0 {
		return img
	}
	cur := color.NRGBA{A: 255}
	if current != nil {
		cur = color.NRGBAModel.Convert(current).(color.NRGBA)
	}
	s.doc.Draw(img, s.options(cur, s.darkIn(false), text.Shared()))
	return img
}

func (s *SVG) imageSize() (float32, float32) {
	if s == nil {
		return 0, 0
	}
	return s.w, s.h
}

// Icon creates an element showing an SVG as an icon: its shapes in the
// color of the text, which it takes from its ancestors as text does
// (TextColor), whatever colors the SVG has. It is as high as the font size
// (FontSize), keeping the SVG's aspect ratio, unless given a size, and
// does not stretch across a column. Icons are decorations that assistive
// technology does not see, unless Label names them.
//
//	ui.Row(c).Gap(6).Children(func() {
//		ui.Icon(c, save)
//		ui.Text(c, "Save")
//	})
func coreIcon(c *context, s *SVG) *node {
	e := c.newElement(kindIcon)
	e.svg = s
	if s != nil && s.h > 0 {
		e.aspect = s.w / s.h
	}
	return e
}

// Icon draws the shapes of an SVG fitted in r, in color c.
func (p *Painter) Icon(s *SVG, r Rect, c Color) { p.drawIcon(s, r, c, 0) }

// Rotate rotates an element clockwise around its transform origin, which
// changes no layout sizes. An Icon keeps turning its SVG artwork around its
// center, as a spinner does:
//
//	spin := ui.Icon(c, loader)
//	spin.Rotate(spin.Loop("spin", time.Second, ui.Linear) * 360)
//
// Use Transform(Rotation(degrees)) to rotate an icon's entire border box.
func (e *node) Rotate(degrees float32) *node {
	finiteTransform(degrees)
	if e.kind == kindIcon {
		e.rotate = degrees
	} else {
		e.transform = e.transform.Then(Rotation(degrees))
	}
	return e
}

// Grayscale draws the element's image, or icon, in shades of gray.
func (e *node) Grayscale() *node { e.gray = true; return e }

// svgs holds what the engine reuses to draw SVGs: the job of drawing an
// icon's mask, the pixels it draws them into, and the pictures of SVGs
// drawn in their own colors.
type svgs struct {
	job      iconJob
	canvas   *image.RGBA
	mask     []byte
	pictures map[pictureKey]*picture
}

// iconJob is the mask of an icon drawIcon asks the text system for, which
// draws it when it is not cached: the SVG fitted in w×h pixels, turned by
// rotate degrees in a mask cw×ch.
type iconJob struct {
	s      *SVG
	w, h   int
	cw, ch int
	rotate float32
	dark   bool
	draw   func() (w, h int, pix []byte)
}

// pictureKey identifies the picture of an SVG drawn w×h pixels, in its own
// colors with color for currentColor, stretched or not.
type pictureKey struct {
	s       *SVG
	w, h    int
	color   Color
	stretch bool
	dark    bool
}

type picture struct {
	img  *scene.Image
	used uint64 // the last frame that drew it
}

var svgSeed = maphash.MakeSeed()

// canvasFor returns a cleared w×h canvas, reusing the engine's memory.
func (rt *engine) canvasFor(w, h int) *image.RGBA {
	c := rt.svgs.canvas
	if c == nil || cap(c.Pix) < w*h*4 {
		c = image.NewRGBA(image.Rect(0, 0, w, h))
		rt.svgs.canvas = c
		return c
	}
	c.Pix, c.Stride, c.Rect = c.Pix[:w*h*4], w*4, image.Rect(0, 0, w, h)
	clear(c.Pix)
	return c
}

// drawIcon paints an SVG's shapes fitted in r, in c, turned by rotate
// degrees around its center, from a mask the text system caches in its
// atlas: once there, the GPU draws the icon in any color without drawing
// its shapes again.
func (p *Painter) drawIcon(s *SVG, r Rect, c Color, rotate float32) {
	if s == nil || c.A == 0 || !p.visible(r, r.W+r.H) {
		return
	}
	d := p.snap(r)
	w, h := int(d.W), int(d.H)
	if w <= 0 || h <= 0 || w > 4096 || h > 4096 {
		return
	}
	// Turned by a sixteenth of a degree at the finest, in a mask as large
	// as it then covers, around the same center.
	rotate = float32(math.Mod(float64(rotate), 360))
	if rotate < 0 {
		rotate += 360
	}
	rotate = float32(math.Round(float64(rotate)*16) / 16)
	if rotate == 360 {
		rotate = 0
	}
	cw, ch := w, h
	if rotate != 0 {
		sin, cos := math.Sincos(float64(rotate) * math.Pi / 180)
		sin, cos = math.Abs(sin), math.Abs(cos)
		cw = int(math.Ceil(float64(w)*cos+float64(h)*sin)) + 2
		ch = int(math.Ceil(float64(w)*sin+float64(h)*cos)) + 2
		// Of the same parity, so that the center stays on the grid.
		cw += (cw - w) & 1
		ch += (ch - h) & 1
	}
	rt := p.rt
	j := &rt.svgs.job
	if j.draw == nil {
		j.draw = rt.rasterizeIcon
	}
	dark := s.darkIn(p.Theme().Dark)
	var buf [21]byte
	buf[0] = 's' // apart from the masks of paths
	if dark {
		buf[0] = 'S'
	}
	binary.LittleEndian.PutUint64(buf[1:], s.id)
	binary.LittleEndian.PutUint32(buf[9:], uint32(w))
	binary.LittleEndian.PutUint32(buf[13:], uint32(h))
	binary.LittleEndian.PutUint32(buf[17:], math.Float32bits(rotate))
	j.s, j.w, j.h, j.cw, j.ch, j.rotate, j.dark = s, w, h, cw, ch, rotate, dark
	gi := rt.text.Mask(maphash.Bytes(svgSeed, buf[:]), j.draw)
	j.s = nil
	if !gi.OK {
		return
	}
	x, y := d.X-float32((cw-w)/2), d.Y-float32((ch-h)/2)
	start := int32(len(p.s.Glyphs))
	c = c.Alpha(p.opacity)
	p.s.Glyphs = append(p.s.Glyphs, scene.Glyph{X: x, Y: y, W: float32(gi.W), H: float32(gi.H), U: gi.X, V: gi.Y, UW: gi.W, VH: gi.H, Color: c.scene(), Wide: p.glyphWide(c)})
	p.add(scene.Op{Kind: scene.OpGlyphs, Start: start, End: start + 1})
}

// rasterizeIcon draws the mask of the icon job: the coverage of its SVG,
// whatever its colors.
func (rt *engine) rasterizeIcon() (int, int, []byte) {
	j := &rt.svgs.job
	img := rt.canvasFor(j.cw, j.ch)
	o := j.s.options(color.NRGBA{A: 255}, j.dark, rt.text)
	if j.rotate != 0 {
		j.s.doc.DrawRotated(img, o, float64(j.w), float64(j.h), float64(j.rotate))
	} else {
		j.s.doc.Draw(img, o)
	}
	n := j.cw * j.ch
	if cap(rt.svgs.mask) < n {
		rt.svgs.mask = make([]byte, n)
	}
	mask := rt.svgs.mask[:n]
	for i := range mask {
		mask[i] = img.Pix[i*4+3]
	}
	return j.cw, j.ch, mask
}

// drawSVG paints an SVG in its own colors, with current for currentColor,
// scaled into box as fit says and placed pos from the middle of the room
// around it (ObjectPosition), clipped to radius and in gray if gray.
func (p *Painter) drawSVG(s *SVG, box Rect, fit Fit, pos [2]float32, radius [4]float32, current Color, gray bool) {
	if s == nil || s.w <= 0 || s.h <= 0 || !p.visible(box, 0) {
		return
	}
	dst := box
	if fit != FillBox {
		scale := float32(1)
		switch fit {
		case Contain:
			scale = min(box.W/s.w, box.H/s.h)
		case Cover:
			scale = max(box.W/s.w, box.H/s.h)
		case ScaleDown:
			scale = min(box.W/s.w, box.H/s.h, 1)
		}
		dst = placeIn(box, s.w*scale, s.h*scale, pos)
	}
	d := p.snap(dst)
	w, h := int(d.W), int(d.H)
	if w <= 0 || h <= 0 || w > 4096 || h > 4096 {
		return
	}
	if !s.doc.UsesCurrentColor() {
		current = Color{}
	}
	// Pictures are bitmaps in sRGB: current draws there as its nearest
	// sRGB color.
	img := p.rt.picture(pictureKey{s: s, w: w, h: h, color: current.SRGB(), stretch: fit == FillBox, dark: s.darkIn(p.Theme().Dark)})
	op := scene.Op{Kind: scene.OpImage, Rect: d, Radii: p.radii(radius), Continuous: continuousCorners, Image: img, Src: scene.Rect{W: float32(w), H: float32(h)}, Opacity: p.opacity, Grayscale: gray}
	if fit != FillBox {
		// Only what falls in the box shows: of Cover and NaturalSize, or
		// placed past its edges (ObjectPosition).
		b := p.snap(box).Intersect(d)
		if b.W <= 0 || b.H <= 0 {
			return
		}
		op.Rect, op.Src = b, scene.Rect{X: b.X - d.X, Y: b.Y - d.Y, W: b.W, H: b.H}
	}
	p.add(op)
}

// picture returns the picture of an SVG, drawing it unless a recent frame
// did.
func (rt *engine) picture(k pictureKey) *scene.Image {
	pics := rt.svgs.pictures
	if pics == nil {
		pics = map[pictureKey]*picture{}
		rt.svgs.pictures = pics
	}
	if pic, ok := pics[k]; ok {
		pic.used = rt.frame
		return pic.img
	}
	// A picture of the SVG at this size in another color, as its color
	// changes, is drawn again in place: the GPU updates its texture
	// instead of making another one.
	var img *scene.Image
	for old, pic := range pics {
		if old.s == k.s && old.w == k.w && old.h == k.h && old.stretch == k.stretch && pic.used != rt.frame {
			delete(pics, old)
			img = pic.img
			clear(img.Pix)
			break
		}
	}
	if img == nil {
		img = scene.NewImageRGBA(k.w, k.h, make([]byte, k.w*k.h*4))
	}
	c := k.color
	o := k.s.options(color.NRGBA{c.R, c.G, c.B, c.A}, k.dark, rt.text)
	o.Stretch = k.stretch
	k.s.doc.Draw(&image.RGBA{Pix: img.Pix, Stride: k.w * 4, Rect: image.Rect(0, 0, k.w, k.h)}, o)
	img.Changed()
	pics[k] = &picture{img: img, used: rt.frame}
	return img
}

// prunePictures forgets the pictures no frame drew for a while.
func (rt *engine) prunePictures() {
	for k, pic := range rt.svgs.pictures {
		if rt.frame-pic.used > 30 {
			delete(rt.svgs.pictures, k)
		}
	}
}
