package markdown

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

type editorLinkClick struct {
	start, end int
	version    uint64
	mods       ui.Modifiers
}

// LinkLabel builds a LinkView's formatted label and records the outer text
// element's geometry. Inline text children are painted by that outer text,
// so their own Draw callbacks never run.
func (s *EditorState) LinkLabel(element ui.Element, children func(), links ...*parser.Inline) {
	start := len(s.runs)
	element.Children(children)
	if !s.projectionCurrent() || start == len(s.runs) {
		return
	}
	runs := append([]*editorRun(nil), s.runs[start:]...)
	s.runs = s.runs[:start]
	var spans []ui.Span
	var boundaries, ends []int
	for _, run := range runs {
		spans = append(spans, run.spans...)
		if len(boundaries) > 0 {
			boundaries, ends = boundaries[:len(boundaries)-1], ends[:len(ends)-1]
		}
		boundaries = append(boundaries, run.boundaries...)
		ends = append(ends, run.ends...)
	}
	first := runs[0]
	s.observe(element, spans, boundaries, ends, first.font, first.lineHeight, first.noWrap)
	run := s.runs[len(s.runs)-1]
	for _, link := range links {
		if link != nil {
			// Hidden wrappers share the label's visible edge without changing
			// its pointer/selection source mapping.
			run.caretStart = min(run.caretStart, s.virtualOffset(link.Start, false))
			run.caretEnd = max(run.caretEnd, s.virtualOffset(link.End, true))
		}
	}
}

// LinkClicked consumes a rendered link's pointer activation. LinkView hosts
// use this alongside their element's Clicked (for keyboard activation). The
// root owns the whole pointer gesture, so a drag never clicks a child link.
func (s *EditorState) LinkClicked(in *parser.Inline) (ui.Modifiers, bool) {
	click := s.clickedLink
	if in == nil || click == nil || click.version != s.buffer.Version() || click.start != in.Start || click.end != in.End {
		return 0, false
	}
	s.clickedLink = nil
	return click.mods, true
}

func (s *EditorState) linkAt(x, y float32) (*parser.Inline, bool) {
	if s.view.doc == nil || s.view.doc.Source() != s.source {
		return nil, false
	}
	px, py := x+s.rect.X, y+s.rect.Y
	// A nearest-run hit can be in padding beside a link. Only the actual
	// label geometry may begin a link gesture.
	for _, run := range s.runs {
		if run.layout == nil || !run.rect.Contains(px, py) {
			continue
		}
		position := run.layout.PositionAt(ui.Point{X: px - run.rect.X, Y: py - run.rect.Y})
		a, z := run.sourceEdgesForUnit(position.Index)
		for i := 1; i < s.view.doc.Len(); i++ {
			for _, in := range s.view.doc.Inlines(i) {
				if in.Kind == parser.Link && a >= in.Start && a < in.End && z <= in.End {
					return &in, true
				}
			}
		}
	}
	return nil, false
}

func (s *EditorState) beginLinkDrag() {
	start, end, affinity := s.positionEdges(s.linkPressX, s.linkPressY)
	s.dragAnchorStart, s.dragAnchorEnd = start, end
	at := start
	if affinity == ui.TextUpstream {
		at = end
	}
	s.moveTo(at, affinity, false)
	s.dragging = true
	s.frozenStart, s.frozenEnd = s.activeStart, s.activeEnd
	s.focusRequest = true
	s.hasDesired = false
}
