package svg

import (
	"slices"
	"strings"
)

// maxRules bounds the rules of a document's style sheets.
const maxRules = 4096

// report collects what a document has that is not drawn, as Unsupported
// lists it.
type report struct{ items []string }

func (r *report) add(item string) {
	if r != nil && !slices.Contains(r.items, item) && len(r.items) < 256 {
		r.items = append(r.items, item)
	}
}

// Media a rule applies in: the light color scheme, the dark one, or both.
const (
	mediaLight uint8 = 1 << iota
	mediaDark
	mediaAll = mediaLight | mediaDark
)

// sheet is the rules of a document's style sheets.
type sheet struct {
	rules []rule
	// scheme is true when a rule depends on prefers-color-scheme.
	scheme bool
}

// parseSheets parses the style sheets of a document.
func parseSheets(root *elem, rep *report) *sheet {
	sh := &sheet{}
	walk(root, func(e *elem) {
		if t := e.attrs["type"]; e.name == "style" && (t == "" || t == "text/css") {
			sh.parse(e.text, mediaAll, rep)
		}
	})
	sh.rules = sh.rules[:min(len(sh.rules), maxRules)]
	return sh
}

// cascade gives every element the declarations of its properties, and
// its custom properties, in the dark color scheme or the light one.
func cascade(root *elem, sh *sheet, dark bool, rep *report) {
	want := mediaLight
	if dark {
		want = mediaDark
	}
	// Rules are filed under the id, first class or type of the element
	// they select, so that an element is matched against the rules it
	// may match only.
	byID, byClass, byTag := map[string][]int{}, map[string][]int{}, map[string][]int{}
	var anyElem []int
	for i := range sh.rules {
		r := &sh.rules[i]
		if r.media&want == 0 {
			continue
		}
		switch s := r.sel.subject(); {
		case s.id != "":
			byID[s.id] = append(byID[s.id], i)
		case len(s.classes) > 0:
			byClass[s.classes[0]] = append(byClass[s.classes[0]], i)
		case s.tag != "":
			byTag[s.tag] = append(byTag[s.tag], i)
		default:
			anyElem = append(anyElem, i)
		}
	}
	var matched []int
	var decls []decl
	walk(root, func(e *elem) {
		e.props = nil
		e.vars = nil
		if e.parent != nil {
			e.vars = e.parent.vars
		}
		if e.name == "#text" {
			return
		}
		// Attributes, the marker shorthand before the properties it sets.
		decls = decls[:0]
		if v, ok := e.attrs["marker"]; ok {
			decls = append(decls, decl{"marker", strings.TrimSpace(v)})
		}
		for name, v := range e.attrs {
			if properties[name] && name != "marker" {
				decls = append(decls, decl{name, strings.TrimSpace(v)})
			} else if unsupportedProps[name] {
				reportProp(name, v, rep)
			}
		}
		matched = matched[:0]
		match := func(candidates []int) {
			for _, i := range candidates {
				if sh.rules[i].sel.match(e) {
					matched = append(matched, i)
				}
			}
		}
		match(anyElem)
		match(byTag[e.name])
		if id := e.attrs["id"]; id != "" {
			match(byID[id])
		}
		for _, c := range strings.Fields(e.attrs["class"]) {
			match(byClass[c])
		}
		// By specificity, then in order.
		slices.SortFunc(matched, func(a, b int) int {
			if d := sh.rules[a].sel.spec - sh.rules[b].sel.spec; d != 0 {
				return d
			}
			return a - b
		})
		for _, i := range slices.Compact(matched) {
			decls = append(decls, sh.rules[i].decls...)
		}
		if s, ok := e.attrs["style"]; ok {
			decls = append(decls, parseDecls(s, rep)...)
		}
		e.vars = customProps(e.vars, decls)
		for _, d := range decls {
			if strings.HasPrefix(d.name, "--") {
				continue
			}
			if strings.Contains(d.value, "var(") {
				v, ok := substitute(d.value, e.vars, 0)
				if !ok {
					// Invalid at computed-value time: as if unset.
					v = "unset"
				}
				d.value = strings.TrimSpace(v)
			}
			e.props = append(e.props, d)
		}
	})
}

// customProps returns the custom properties of an element that inherits
// inherited and declares those of decls.
func customProps(inherited map[string]string, decls []decl) map[string]string {
	var own map[string]string
	for _, d := range decls {
		if strings.HasPrefix(d.name, "--") {
			if own == nil {
				own = map[string]string{}
			}
			own[d.name] = d.value
		}
	}
	if own == nil {
		return inherited
	}
	raw := make(map[string]string, len(inherited)+len(own))
	for k, v := range inherited {
		raw[k] = v
	}
	for k, v := range own {
		raw[k] = v
	}
	// The element's own values may refer to each other: each is
	// substituted in full, and one that cannot be is left out, as CSS's
	// guaranteed-invalid value.
	vars := make(map[string]string, len(raw))
	for k, v := range raw {
		if _, mine := own[k]; !mine {
			vars[k] = v
			continue
		}
		if s, ok := substitute(v, raw, 0); ok {
			vars[k] = s
		}
	}
	return vars
}

// Limits on substituting custom properties: how deep var() functions
// nest, and how long a value they make may grow.
const (
	maxVarDepth = 32
	maxVarLen   = 1 << 14
)

// substitute replaces the var() functions of v with the custom
// properties of vars, or their fallbacks: ok is false when one names a
// property that is not there and has no fallback.
func substitute(v string, vars map[string]string, depth int) (string, bool) {
	if depth > maxVarDepth {
		return "", false
	}
	var b strings.Builder
	for {
		i := strings.Index(v, "var(")
		if i < 0 {
			b.WriteString(v)
			break
		}
		b.WriteString(v[:i])
		end := closeParen(v, i+3)
		if end < 0 {
			return "", false
		}
		args := v[i+4 : end]
		name, fallback, hasFallback := strings.Cut(args, ",")
		name = strings.TrimSpace(name)
		var sub string
		ok := false
		if val, found := vars[name]; found {
			sub, ok = substitute(val, vars, depth+1)
		}
		if !ok && hasFallback {
			sub, ok = substitute(strings.TrimSpace(fallback), vars, depth+1)
		}
		if !ok {
			return "", false
		}
		b.WriteString(sub)
		if b.Len() > maxVarLen {
			return "", false
		}
		v = v[end+1:]
	}
	return b.String(), true
}

// closeParen returns the index of the parenthesis closing the one at
// s[open], or -1.
func closeParen(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return -1
}

// rule is a rule of a style sheet with a single selector, which applies
// in media.
type rule struct {
	sel   selector
	decls []decl
	media uint8
}

// compound is a compound selector: a type, an id, classes and :root, any
// of which may be missing.
type compound struct {
	tag     string
	id      string
	classes []string
	root    bool
}

// selector is compound selectors joined by descendant and child
// combinators. Style sheets with other combinators, pseudo-classes but
// :root or attribute selectors are not matched.
type selector struct {
	parts []compound // the subject last
	// child[i] is true when parts[i+1] is a child of parts[i], rather
	// than a descendant.
	child []bool
	spec  int // specificity
}

func (s *selector) subject() *compound { return &s.parts[len(s.parts)-1] }

func (c *compound) match(e *elem) bool {
	if c.tag != "" && c.tag != e.name || c.id != "" && e.attrs["id"] != c.id || c.root && e.parent != nil {
		return false
	}
	if len(c.classes) > 0 {
		have := strings.Fields(e.attrs["class"])
		for _, cl := range c.classes {
			if !slices.Contains(have, cl) {
				return false
			}
		}
	}
	return true
}

func (s *selector) match(e *elem) bool {
	n := len(s.parts) - 1
	return s.parts[n].match(e) && s.matchFrom(n, e)
}

// matchFrom reports whether the parts before i match the ancestors of e,
// which parts[i] matches.
func (s *selector) matchFrom(i int, e *elem) bool {
	if i == 0 {
		return true
	}
	for p := e.parent; p != nil; p = p.parent {
		if s.parts[i-1].match(p) && s.matchFrom(i-1, p) {
			return true
		}
		if s.child[i-1] {
			break
		}
	}
	return false
}

// parse adds the rules of a style sheet, in media.
func (sh *sheet) parse(s string, media uint8, rep *report) {
	for {
		s = strings.TrimSpace(s)
		switch {
		case s == "":
			return
		case strings.HasPrefix(s, "/*"):
			end := strings.Index(s[2:], "*/")
			if end < 0 {
				return
			}
			s = s[end+4:]
			continue
		case strings.HasPrefix(s, "<!--"):
			s = s[4:]
			continue
		case strings.HasPrefix(s, "-->"):
			s = s[3:]
			continue
		case s[0] == '@':
			// An at-rule ends at a semicolon or with its block.
			i := strings.IndexAny(s, ";{")
			if i < 0 {
				return
			}
			name := strings.ToLower(s[1:nameEnd(s, 1)])
			if s[i] == ';' {
				if name == "import" {
					rep.add("@import")
				}
				s = s[i+1:]
				continue
			}
			end := blockEnd(s, i)
			switch name {
			case "media":
				if m := sh.media(stripComments(s[len("@media"):i]), rep); m&media != 0 {
					sh.parse(strings.TrimSuffix(s[i+1:end], "}"), m&media, rep)
				}
			case "font-face", "keyframes", "supports", "layer", "container", "property":
				rep.add("@" + name)
			}
			s = s[end:]
			continue
		}
		open := strings.IndexByte(s, '{')
		if open < 0 {
			return
		}
		end := blockEnd(s, open)
		body := strings.TrimSuffix(s[open+1:end], "}")
		decls := parseDecls(stripComments(body), rep)
		for _, part := range strings.Split(stripComments(s[:open]), ",") {
			part = strings.TrimSpace(part)
			if sel, ok := parseSelector(part); ok {
				sh.rules = append(sh.rules, rule{sel, decls, media})
			} else if part != "" && !dynamic(part) {
				rep.add("selector " + part)
			}
		}
		s = s[end:]
	}
}

// media evaluates a media query list: the media of mediaAll in which it
// is true. Media types but all and screen, and features but
// prefers-color-scheme, are false.
func (sh *sheet) media(q string, rep *report) uint8 {
	var out uint8
	for _, query := range strings.Split(q, ",") {
		m := mediaAll
		f := strings.Fields(strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(query, "(", " ( "), ")", " ) ")))
		not := len(f) > 0 && f[0] == "not"
		if len(f) > 0 && (f[0] == "not" || f[0] == "only") {
			f = f[1:]
		}
		for i := 0; i < len(f); i++ {
			switch t := f[i]; t {
			case "and":
			case "all", "screen":
			case "(":
				end := slices.Index(f[i:], ")")
				if end < 0 {
					return 0
				}
				feature := strings.Join(f[i+1:i+end], "")
				i += end
				switch feature {
				case "prefers-color-scheme:dark":
					m &= mediaDark
					sh.scheme = true
				case "prefers-color-scheme:light":
					m &= mediaLight
					sh.scheme = true
				default:
					rep.add("@media (" + strings.Join(f[i-end+1:i], " ") + ")")
					m = 0
				}
			default:
				m = 0 // print, and media types MyGo does not know
			}
		}
		if not {
			m = mediaAll &^ m
		}
		out |= m
	}
	return out
}

// dynamic reports whether a selector selects by a state that a picture
// does not have, as :hover, which leaving out draws right.
func dynamic(sel string) bool {
	for _, p := range []string{":hover", ":active", ":focus", ":visited", ":target"} {
		if strings.Contains(sel, p) {
			return true
		}
	}
	return false
}

// blockEnd returns the index after the block opening at s[open].
func blockEnd(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '{':
			depth++
		case '}':
			if depth--; depth == 0 {
				return i + 1
			}
		}
	}
	return len(s)
}

func stripComments(s string) string {
	for {
		i := strings.Index(s, "/*")
		if i < 0 {
			return s
		}
		j := strings.Index(s[i+2:], "*/")
		if j < 0 {
			return s[:i]
		}
		s = s[:i] + " " + s[i+2+j+2:]
	}
}

func isNameChar(c byte) bool {
	return c == '-' || c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= 0x80
}

func nameEnd(s string, i int) int {
	for i < len(s) && isNameChar(s[i]) {
		i++
	}
	return i
}

// parseSelector parses compound selectors joined by white space and >.
func parseSelector(s string) (selector, bool) {
	var sel selector
	s = strings.ReplaceAll(s, ">", " > ")
	child := false
	for _, f := range strings.Fields(s) {
		if f == ">" {
			if len(sel.parts) == 0 || child {
				return selector{}, false
			}
			child = true
			continue
		}
		c, spec, ok := parseCompound(f)
		if !ok {
			return selector{}, false
		}
		if len(sel.parts) > 0 {
			sel.child = append(sel.child, child)
		}
		child = false
		sel.parts = append(sel.parts, c)
		sel.spec += spec
	}
	if len(sel.parts) == 0 || child {
		return selector{}, false
	}
	return sel, true
}

// parseCompound parses a compound selector, and returns its specificity.
func parseCompound(s string) (compound, int, bool) {
	var c compound
	spec := 0
	i := 0
	if s[0] == '*' {
		i = 1
	} else if isNameChar(s[0]) {
		i = nameEnd(s, 0)
		c.tag = s[:i]
		spec++
	}
	for i < len(s) {
		ch := s[i]
		j := nameEnd(s, i+1)
		if j == i+1 {
			return compound{}, 0, false
		}
		switch ch {
		case '.':
			c.classes = append(c.classes, s[i+1:j])
			spec += 100
		case '#':
			c.id = s[i+1 : j]
			spec += 10000
		case ':':
			if strings.ToLower(s[i+1:j]) != "root" {
				return compound{}, 0, false
			}
			c.root = true
			spec += 100
		default:
			return compound{}, 0, false
		}
		i = j
	}
	return c, spec, true
}

// parseDecls parses declarations, such as those of a style attribute: of
// the properties MyGo draws, and custom ones.
func parseDecls(s string, rep *report) []decl {
	var out []decl
	for _, part := range splitDecls(s) {
		name, value, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if i := strings.Index(strings.ToLower(value), "!important"); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
		if strings.HasPrefix(name, "--") {
			out = append(out, decl{name, value})
			continue
		}
		name = strings.ToLower(name)
		switch {
		case properties[name] && value != "":
			out = append(out, decl{name, value})
		case unsupportedProps[name] || name == "transform":
			reportProp(name, value, rep)
		}
	}
	return out
}

// splitDecls splits declarations at the semicolons outside parentheses,
// which data URLs have in them.
func splitDecls(s string) []string {
	var out []string
	depth, start := 0, 0
	for i := range len(s) {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth = max(depth-1, 0)
		case ';':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}

// unsupportedProps are the properties that change what a document looks
// like that MyGo does not draw.
var unsupportedProps = map[string]bool{
	"filter": true, "mix-blend-mode": true, "vector-effect": true,
	"text-decoration": true, "word-spacing": true, "writing-mode": true, "baseline-shift": true,
	"font-variant": true, "font-stretch": true, "textLength": true, "lengthAdjust": true,
}

// reportProp reports a property MyGo does not draw, unless its value is
// one that changes nothing.
func reportProp(name, value string, rep *report) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "none", "normal", "auto", "0", "baseline", "horizontal-tb", "lr", "lr-tb", "initial", "inherit", "unset":
		return
	}
	rep.add(name)
}
