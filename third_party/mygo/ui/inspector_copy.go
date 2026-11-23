//go:build !mygo_noinspector

package ui

import (
	"slices"
	"strings"
)

// The inspector copies what it shows as text, as Chrome's developer tools
// copy an element or its styles: for an issue, a chat or an editor. The
// tree's rows have a context menu of the copies, and Cmd+C (Ctrl+C) in the
// tree copies the element chosen.

// rowMenu builds the context menu of the tree's rows of the node id. Opening
// it chooses the element, as in Chrome, for its styles to be those copied.
func (in *inspector) rowMenu(c *context, m *Menu, id uint64) {
	in.choose(id)
	if m.Item("Copy element").Chosen() {
		in.copyElement(c, id)
	}
	m.Separator()
	// The details are of the element chosen as the frame after the menu
	// opened laid out, which are those of id by the time an item is chosen.
	if m.Item("Copy styles").Chosen() && in.sel.id == id {
		c.WriteClipboard(in.stylesText())
	}
	if m.Item("Copy computed").Chosen() && in.sel.id == id {
		c.WriteClipboard(in.computedText())
	}
	if m.Item("Copy properties").Chosen() && in.sel.id == id {
		c.WriteClipboard(in.propertiesText())
	}
}

// copyElement puts the element id on the clipboard as markup.
func (in *inspector) copyElement(c *context, id uint64) {
	if i := in.nodeOf(id); i >= 0 {
		c.WriteClipboard(in.elementText(i))
	}
}

// elementText returns node i and what is inside it as the tree's markup,
// every node open, each indented by two spaces from the one around it,
// and without cutting texts and attributes short.
func (in *inspector) elementText(i int32) string {
	var b strings.Builder
	var open []int32 // the nodes whose closing tag is to come, innermost last
	base := in.nodes[i].depth
	closeTo := func(depth int32) {
		for len(open) > 0 && in.nodes[open[len(open)-1]].depth >= depth {
			n := &in.nodes[open[len(open)-1]]
			open = open[:len(open)-1]
			b.WriteString(strings.Repeat("  ", int(n.depth-base)))
			b.WriteString("</" + n.name + ">\n")
		}
	}
	for j := i; j < int32(len(in.nodes)) && (j == i || in.nodes[j].depth > base); j++ {
		n := &in.nodes[j]
		closeTo(n.depth)
		b.WriteString(strings.Repeat("  ", int(n.depth-base)))
		b.WriteString("<" + n.name)
		if n.keyText != "" {
			b.WriteString(` key="` + n.keyText + `"`)
		}
		if n.label != "" && n.label != n.text {
			b.WriteString(` label="` + n.label + `"`)
		}
		switch {
		case n.kids:
			b.WriteString(">\n")
			open = append(open, j)
		case n.text != "":
			b.WriteString(">" + n.text + "</" + n.name + ">\n")
		default:
			b.WriteString("></" + n.name + ">\n")
		}
	}
	closeTo(base)
	return strings.TrimSuffix(b.String(), "\n")
}

// stylesText returns the Styles tab of the element chosen as CSS: its
// rule, with where the app built it, then those it inherits. Declarations
// turned off are comments, as Chrome copies them.
func (in *inspector) stylesText() string {
	var b strings.Builder
	for i, r := range in.sel.rules {
		if r.inherited {
			b.WriteString("/* Inherited from " + r.selector + " */\n")
		}
		b.WriteString(r.selector + " {")
		if i == 0 && in.sel.source != "" {
			b.WriteString(" /* " + shortSource(in.sel.source) + " */")
		}
		b.WriteByte('\n')
		for _, d := range r.decls {
			if d.off {
				b.WriteString("  /* " + d.name + ": " + d.value + "; */\n")
			} else {
				b.WriteString("  " + d.name + ": " + d.value + ";\n")
			}
		}
		b.WriteString("}\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// computedText returns the Computed tab of the element chosen as a CSS
// rule: its box model's sides (top, right, bottom, left) and its values as
// laid out, by name.
func (in *inspector) computedText() string {
	d := &in.sel
	sides := func(s [4]float32) string {
		parts := make([]string, 4)
		for i, v := range s {
			if isAuto(v) {
				parts[i] = "auto"
			} else {
				parts[i] = pxText(v)
			}
		}
		return strings.Join(parts, " ")
	}
	decls := append([]inspDecl{
		{name: "margin", value: sides(d.margin)},
		{name: "border-width", value: sides(d.border)},
		{name: "padding", value: sides(d.padding)},
	}, d.computed...)
	slices.SortStableFunc(decls, func(a, b inspDecl) int { return strings.Compare(a.name, b.name) })
	var b strings.Builder
	selector := d.name
	if len(d.rules) > 0 {
		selector = d.rules[0].selector
	}
	b.WriteString(selector + " {\n")
	for _, dc := range decls {
		b.WriteString("  " + dc.name + ": " + dc.value + ";\n")
	}
	b.WriteString("}")
	return b.String()
}

// propertiesText returns the Properties tab of the element chosen: each
// section's title, then its rows, indented.
func (in *inspector) propertiesText() string {
	var b strings.Builder
	for _, s := range in.sel.props {
		b.WriteString(s.title + "\n")
		for _, d := range s.rows {
			b.WriteString("  " + d.name + ": " + d.value + "\n")
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}
