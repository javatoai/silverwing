package svg

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Limits on what a document may hold.
const (
	maxElems = 1 << 18
	maxDepth = 256
)

// elem is an element of the document.
type elem struct {
	name   string
	attrs  map[string]string
	kids   []*elem
	parent *elem
	// text is the text of a style element, or of a run of characters of
	// a text element, which is an elem named "#text".
	text string
	// props are the declarations of its properties, in the order they
	// apply: attributes, style sheets, then the style attribute.
	props []decl
	// vars are the custom properties it has, its own and those it
	// inherits, with the var() functions in them substituted.
	vars map[string]string
}

type decl struct{ name, value string }

// parseXML parses the document's elements.
func parseXML(data []byte) (*elem, error) {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	d.CharsetReader = charsetReader
	var root *elem
	var stack []*elem
	count, skip := 0, 0
	// inText counts the elements open in a text element, which keep the
	// characters in them.
	inText := 0
	for {
		tok, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("svg: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if skip > 0 || len(stack) >= maxDepth {
				skip++
				continue
			}
			if count++; count > maxElems {
				return nil, errors.New("svg: too many elements")
			}
			e := &elem{name: t.Name.Local, attrs: make(map[string]string, len(t.Attr))}
			for _, a := range t.Attr {
				// Namespaced attributes belong to editors, but for
				// xlink:href, which href takes precedence over, and
				// xml:space.
				if a.Name.Space == "xml" || a.Name.Space == "http://www.w3.org/XML/1998/namespace" {
					if a.Name.Local == "space" {
						e.attrs["xml:space"] = a.Value
					}
					continue
				}
				if a.Name.Space != "" && a.Name.Local != "href" {
					continue
				}
				if _, ok := e.attrs[a.Name.Local]; ok && a.Name.Space != "" {
					continue
				}
				e.attrs[a.Name.Local] = a.Value
			}
			if n := len(stack); n > 0 {
				e.parent = stack[n-1]
				e.parent.kids = append(e.parent.kids, e)
			} else {
				root = e
			}
			stack = append(stack, e)
			if inText > 0 || e.name == "text" {
				inText++
			}
		case xml.EndElement:
			if skip > 0 {
				skip--
				continue
			}
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			if inText > 0 {
				inText--
			}
			if len(stack) == 0 {
				return finishDOM(root)
			}
		case xml.CharData:
			n := len(stack)
			switch {
			case skip > 0 || n == 0:
			case stack[n-1].name == "style":
				stack[n-1].text += string(t)
			case inText > 0:
				if count++; count > maxElems {
					return nil, errors.New("svg: too many elements")
				}
				p := stack[n-1]
				p.kids = append(p.kids, &elem{name: "#text", text: string(t), parent: p})
			}
		}
	}
	return finishDOM(root)
}

func finishDOM(root *elem) (*elem, error) {
	if root == nil || root.name != "svg" {
		return nil, errors.New("svg: no svg element")
	}
	return root, nil
}

// charsetReader reads the single-byte encodings old editors declare; other
// encodings are read as UTF-8.
func charsetReader(label string, r io.Reader) (io.Reader, error) {
	switch strings.ToLower(label) {
	case "iso-8859-1", "latin1", "latin-1", "windows-1252", "cp1252":
		data, err := io.ReadAll(r)
		if err != nil {
			return nil, err
		}
		var b strings.Builder
		for _, c := range data {
			b.WriteRune(rune(c))
		}
		return strings.NewReader(b.String()), nil
	}
	return r, nil
}

func walk(e *elem, fn func(*elem)) {
	fn(e)
	for _, k := range e.kids {
		walk(k, fn)
	}
}

// unsupportedElems are the elements whose drawing MyGo leaves out, of
// those that draw where they are. Patterns and filters draw where
// something refers to them: the compiler reports them there.
var unsupportedElems = map[string]bool{
	"image": true, "foreignObject": true, "textPath": true, "video": true, "canvas": true, "iframe": true,
	"animate": true, "animateMotion": true, "animateTransform": true, "animateColor": true, "set": true,
}

// reportElems reports the elements of a document MyGo does not draw.
func reportElems(root *elem, rep *report) {
	walk(root, func(e *elem) {
		if unsupportedElems[e.name] {
			rep.add("<" + e.name + ">")
		}
	})
}
