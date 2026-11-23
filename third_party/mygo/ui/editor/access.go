package editor

import (
	"sort"

	"github.com/egoist/mygo/internal/platform"
)

// What assistive technology reads of an editor besides its text
// (accessibility): where its lines in view are, by which it finds a range
// of the text (AccessNode.LineBoxes), and for a side of a diff, a node
// for each line in view (accessLines), added, deleted or unchanged, with
// its number. Both are of the lines the last frame painted, so their cost
// is that of the view, whatever the size of the text.

// accessView returns the part of the content the last frame showed, and
// false when it did not paint the editor as built, as one out of view.
func (ed *editor) accessView() (y0, y1 float64, ok bool) {
	if ed.r.W <= 0 || ed.r.H <= 0 || ed.paintedBuild != ed.builds {
		return 0, 0, false
	}
	if ed.o.fitContent {
		return ed.shownY[0], ed.shownY[1], true
	}
	return ed.scrollY, ed.scrollY + float64(ed.r.H), true
}

// accessBox returns where a part of the content from top, height high,
// shows in the window, across the text right of the gutter, and false
// when it is out of the view, y0 to y1.
func (ed *editor) accessBox(top, height, y0, y1 float64) (platform.RectF, bool) {
	if height <= 0 || top+height <= y0 || top >= y1 {
		return platform.RectF{}, false
	}
	return platform.RectF{
		X: float64(ed.r.X + ed.gutterW), Y: float64(ed.r.Y) + top - ed.scrollY,
		W: float64(max(ed.r.W-ed.gutterW, 0)), H: height,
	}, true
}

// lineBoxes returns the boxes of the lines in view from first to last,
// numbered from first, as the lines of a Value starting at first: a new
// slice, which the tree keeps. A run of lines collapsed has one box: the
// separator of a diff standing for it, or the line a fold hides it under,
// so that a range in it shows where it is folded rather than as the
// whole editor.
func (ed *editor) lineBoxes(first, last int) []platform.AccessLineBox {
	y0, y1, ok := ed.accessView()
	if !ok {
		return nil
	}
	h := &ed.view.heights
	var out []platform.AccessLineBox
	// add adds the box of lines from to to, those of Value alone.
	add := func(from, to int, b platform.RectF) {
		from, to = max(from, first), min(to, last)
		if from > to {
			return
		}
		if k := len(out) - 1; k >= 0 && out[k].Line+max(out[k].Count, 1) > from-first {
			return // already in a box
		}
		bx := platform.AccessLineBox{Line: from - first, Bounds: b}
		if to > from {
			bx.Count = to - from + 1
		}
		out = append(out, bx)
	}
	// separators adds the boxes of the separators among blks, whose top
	// is at top: the lines each collapses.
	separators := func(blks []block, top float64) {
		for i := range blks {
			if b := &blks[i]; b.kind == blockSeparator && b.to > b.from {
				if box, ok := ed.accessBox(top, float64(b.height), y0, y1); ok {
					add(b.from, b.to-1, box)
				}
			}
			top += float64(blks[i].height)
		}
	}
	var folded *lineSpan // lines hidden with no separator yet
	var prev platform.RectF
	for line := ed.shown[0]; line <= min(ed.shown[1], h.n-1); {
		g := h.geometry(line)
		if g.hidden {
			sp := h.hiddenSpan(line)
			folded = &sp
			line = sp.to + 1
			continue
		}
		above, below := h.blocksOf(line)
		separators(above, g.top)
		if folded != nil && prev.H > 0 {
			// Hidden under the line before them, as by a fold.
			add(folded.from, folded.to, prev)
		}
		folded = nil
		prev = platform.RectF{}
		if box, ok := ed.accessBox(g.rows, g.below-g.rows, y0, y1); ok {
			add(line, line, box)
			prev = box
		}
		separators(below, g.below)
		line++
	}
	return out
}

// accessLines adds a node inside the text field of a side of a diff for
// each line in view, in the order they show: an insertion for a line
// added, a deletion for one deleted, as the old side's changed lines and
// the deleted lines a unified view shows between the new text's, and a
// text for one unchanged, named by its text and described by its number,
// so that screen readers read every line of the diff as what it is.
func (ed *editor) accessLines(parent *platform.AccessNode, add func(platform.AccessNode)) {
	s, ok := ed.o.decor.(*diffSide)
	if !ok {
		return
	}
	y0, y1, ok := ed.accessView()
	if !ok {
		return
	}
	d := s.d
	h := &ed.view.heights
	tx := ed.texts
	blocks := func(blks []block, top float64) {
		for i := range blks {
			b := &blks[i]
			if b.kind == blockDeleted && b.hunk < len(d.deletedTops) {
				ed.accessDeleted(s, b, top, y0, y1, parent.ID, add)
			}
			top += float64(b.height)
		}
	}
	own := !s.empty()
	for line := ed.shown[0]; line <= min(ed.shown[1], h.n-1); line++ {
		g := h.geometry(line)
		if g.hidden {
			// Collapsed: the separator over them is an element of its own.
			line = h.hiddenSpan(line).to
			continue
		}
		above, below := h.blocksOf(line)
		blocks(above, g.top)
		if own && !g.hidden {
			if box, ok := ed.accessBox(g.rows, g.below-g.rows, y0, y1); ok {
				role := platform.RoleText
				if s.hunkOf(line) >= 0 {
					role = platform.RoleInsertion
					if s.old {
						role = platform.RoleDeletion
					}
				}
				add(platform.AccessNode{
					ID: lineNodeID(parent.ID, false, line), Role: role, Bounds: box,
					Label: ed.view.text.Line(line), Description: tx.Line(s.number(line)),
				})
			}
		}
		blocks(below, g.below)
	}
}

// accessDeleted adds the nodes of the deleted lines of block b, whose top
// is at top in the content, that show between y0 and y1, described by
// their old numbers ("Old line 12"), as the new line after one may have
// the same number: those from the
// first whose top is in view, found by halves, as a run as long as a file
// deleted is not walked from its first line.
func (ed *editor) accessDeleted(s *diffSide, b *block, top, y0, y1 float64, parent uint64, add func(platform.AccessNode)) {
	d := s.d
	tops := d.deletedTops[b.hunk]
	if len(tops) < b.to-b.from {
		return
	}
	lh := float64(ed.view.lineHeight)
	first := b.from + max(0, sort.Search(b.to-b.from, func(i int) bool { return top+tops[i] > y0 })-1)
	for line := first; line < b.to && top+tops[line-b.from] < y1; line++ {
		box, ok := ed.accessBox(top+tops[line-b.from], float64(d.oldView.heights.wraps.rows(line))*lh, y0, y1)
		if !ok {
			continue
		}
		n := line + 1
		if pt := d.partial; pt != nil {
			n = pt.fileLine(true, line) + 1
		}
		add(platform.AccessNode{
			ID: lineNodeID(parent, true, line), Role: platform.RoleDeletion, Bounds: box,
			Label: d.oldView.text.Line(line), Description: ed.texts.OldLine(n),
		})
	}
}

// lineNodeID returns the ID of the node of a line of the text field
// parent, or of a deleted line it shows of the other text: a mix of
// them, as no element's (splitmix64's finalizer), the same from frame to
// frame.
func lineNodeID(parent uint64, deleted bool, line int) uint64 {
	x := parent ^ uint64(line+1)*0x9e3779b97f4a7c15
	if deleted {
		x ^= 0xd6e8feb86659fd93
	}
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return max(x, 1)
}
