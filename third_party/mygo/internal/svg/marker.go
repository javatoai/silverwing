package svg

import (
	"math"
	"slices"
	"strings"
)

// vertex is a vertex of a path, where markers are drawn: the directions
// of the segments into and out of it, where there are some.
type vertex struct {
	p             point
	in, out       point
	hasIn, hasOut bool
}

// angle returns the direction of a path at a vertex, in degrees: the
// bisector of the directions in and out of it.
func (v *vertex) angle() float64 {
	switch {
	case v.hasIn && v.hasOut:
		a, b := math.Atan2(v.in.y, v.in.x), math.Atan2(v.out.y, v.out.x)
		d := math.Remainder(b-a, 2*math.Pi)
		return (a + d/2) * 180 / math.Pi
	case v.hasIn:
		return math.Atan2(v.in.y, v.in.x) * 180 / math.Pi
	case v.hasOut:
		return math.Atan2(v.out.y, v.out.x) * 180 / math.Pi
	}
	return 0
}

// direction returns the first of ds that is not of zero length.
func direction(ds ...point) point {
	for _, d := range ds {
		if d.x != 0 || d.y != 0 {
			return d
		}
	}
	return point{}
}

// vertices returns the vertices of a path, as markers see them: the
// points that segments start and end at, but where an arc was split into
// curves.
func (pa *path) vertices() []vertex {
	var vs []vertex
	var cur, start point
	first := 0 // the vertex starting the subpath
	segment := func(in, out, e point) {
		if n := len(vs); n > 0 {
			vs[n-1].out, vs[n-1].hasOut = in, true
		}
		vs = append(vs, vertex{p: e, in: out, hasIn: true})
		cur = e
	}
	i := 0
	for k, op := range pa.ops {
		switch op {
		case opMove:
			cur, start = pa.pts[i], pa.pts[i]
			i++
			vs = append(vs, vertex{p: cur})
			first = len(vs) - 1
		case opLine:
			e := pa.pts[i]
			i++
			d := point{e.x - cur.x, e.y - cur.y}
			segment(d, d, e)
		case opCubic:
			c1, c2, e := pa.pts[i], pa.pts[i+1], pa.pts[i+2]
			i += 3
			in := direction(point{c1.x - cur.x, c1.y - cur.y}, point{c2.x - cur.x, c2.y - cur.y}, point{e.x - cur.x, e.y - cur.y})
			out := direction(point{e.x - c2.x, e.y - c2.y}, point{e.x - c1.x, e.y - c1.y}, point{e.x - cur.x, e.y - cur.y})
			if _, found := slices.BinarySearch(pa.arcs, k); found && len(vs) > 0 {
				// The rest of an arc: the vertex moves to its end.
				v := &vs[len(vs)-1]
				v.p, v.in = e, out
				cur = e
				continue
			}
			segment(in, out, e)
		case opClose:
			d := point{start.x - cur.x, start.y - cur.y}
			if d.x == 0 && d.y == 0 && len(vs) > 0 {
				d = vs[len(vs)-1].in
			}
			segment(d, d, start)
			// A closed subpath turns at its start: the markers there
			// bisect the closing segment and the first.
			if first < len(vs)-1 {
				vs[first].in, vs[first].hasIn = d, true
				last := &vs[len(vs)-1]
				last.out, last.hasOut = vs[first].out, vs[first].hasOut
			}
		}
	}
	return vs
}

// markers compiles the markers an element e of style st draws along its
// path pa, of shape sh: those of marker-start at its first vertex,
// marker-mid at the others but the last, and marker-end at the last.
func (c *compiler) markers(e *elem, pa *path, st *style, sh *shape) []*node {
	if st.markerStart == "" && st.markerMid == "" && st.markerEnd == "" {
		return nil
	}
	switch e.name {
	case "path", "line", "polyline", "polygon":
	default:
		return nil
	}
	vs := pa.vertices()
	if len(vs) == 0 {
		return nil
	}
	compiled := map[string]*markerDef{}
	def := func(id string) *markerDef {
		if id == "" {
			return nil
		}
		if m, ok := compiled[id]; ok {
			return m
		}
		m := c.marker(id, sh)
		compiled[id] = m
		return m
	}
	var out []*node
	for i := range vs {
		id := st.markerMid
		switch {
		case i == 0:
			id = st.markerStart
		case i == len(vs)-1:
			id = st.markerEnd
		}
		if m := def(id); m != nil {
			if n := m.at(&vs[i], i == 0, st.strokeWidth); n != nil {
				out = append(out, n)
			}
		}
	}
	return out
}

// markerDef is a marker element compiled for an element it is drawn on.
type markerDef struct {
	// ref is its reference point in its viewport, which goes on the
	// vertex; w and h are the size of the viewport.
	ref        point
	w, h       float64
	userUnits  bool // markerUnits is userSpaceOnUse
	auto       bool // orient is auto or auto-start-reverse
	reverse    bool // orient is auto-start-reverse
	angle      float64
	clip       bool
	content    *node
	clipRegion *clipDef
}

// marker compiles the marker element of id for an element of shape sh,
// whose fill and stroke context-fill and context-stroke are.
func (c *compiler) marker(id string, sh *shape) *markerDef {
	el := c.ids[id]
	if el == nil || el.name != "marker" || slices.Contains(c.using, el) || c.budget <= 0 {
		return nil
	}
	c.using = append(c.using, el)
	defer func() { c.using = c.using[:len(c.using)-1] }()
	ctx := c.ctx
	c.ctx = sh
	defer func() { c.ctx = ctx }()
	vw, vh := c.vw, c.vh
	defer func() { c.vw, c.vh = vw, vh }()

	m := &markerDef{
		w: c.length(el, "markerWidth", c.vw, 3), h: c.length(el, "markerHeight", c.vh, 3),
		userUnits: strings.TrimSpace(el.attrs["markerUnits"]) == "userSpaceOnUse",
	}
	if m.w <= 0 || m.h <= 0 {
		return nil
	}
	switch o := strings.TrimSpace(el.attrs["orient"]); o {
	case "auto":
		m.auto = true
	case "auto-start-reverse":
		m.auto, m.reverse = true, true
	default:
		m.angle = parseAngle(o)
	}
	inner := &node{m: identity}
	if vb, ok := parseViewBox(el.attrs["viewBox"]); ok {
		inner.m = viewBoxTransform(vb, parseAspect(el.attrs["preserveAspectRatio"]), m.w, m.h)
		c.vw, c.vh = vb.w, vb.h
	} else {
		c.vw, c.vh = m.w, m.h
	}
	m.ref = inner.m.apply(point{c.length(el, "refX", c.vw, 0), c.length(el, "refY", c.vh, 0)})
	st := c.domStyle(el)
	m.clip = !st.overflow
	c.group(inner, el, st)
	if len(inner.kids) == 0 {
		return nil
	}
	c.finish(inner, &c.base)
	m.content = inner
	if m.clip {
		var r path
		r.rect(0, 0, m.w, m.h, 0, 0)
		m.clipRegion = &clipDef{m: identity, kids: []*node{{m: identity, shape: &shape{path: r}}}}
	}
	return m
}

// at returns the marker drawn at a vertex, the first of its path when
// start is true, of a stroke as wide as width.
func (m *markerDef) at(v *vertex, start bool, width float64) *node {
	angle := m.angle
	if m.auto {
		angle = v.angle()
		if start && m.reverse {
			angle += 180
		}
	}
	scale := 1.0
	if !m.userUnits {
		scale = width
	}
	if scale <= 0 {
		return nil
	}
	n := &node{
		m:    translate(v.p.x, v.p.y).mul(rotation(angle)).mul(scaling(scale, scale)).mul(translate(-m.ref.x, -m.ref.y)),
		kids: []*node{m.content}, opacity: 1, clip: m.clipRegion,
	}
	n.bbox, n.hasBox = m.content.bbox.transform(m.content.m), m.content.hasBox
	n.single = m.content.flat
	n.flat = n.clip != nil || n.single
	return n
}

// parseAngle parses an angle in degrees, or with a unit.
func parseAngle(v string) float64 {
	scale := 1.0
	switch {
	case strings.HasSuffix(v, "deg"):
		v = v[:len(v)-3]
	case strings.HasSuffix(v, "grad"):
		v, scale = v[:len(v)-4], 0.9
	case strings.HasSuffix(v, "rad"):
		v, scale = v[:len(v)-3], 180/math.Pi
	case strings.HasSuffix(v, "turn"):
		v, scale = v[:len(v)-4], 360
	}
	a, _ := parseNumber(v)
	return a * scale
}
