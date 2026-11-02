package markdown

import (
	"github.com/egoist/mygo/ui"
)

type editorLinkRange struct {
	start, end, labelStart, labelEnd int
}

func (s *EditorState) editableLinks() []editorLinkRange {
	if !s.opts.RenderedLinks || s.SourceMode {
		return nil
	}
	d := s.editingDocument()
	var links []editorLinkRange
	for block := 1; block < d.Len(); block++ {
		inlines := d.Inlines(block)
		for index, in := range inlines {
			if start, end, ok := d.LinkLabelRange(block, index); ok {
				links = append(links, editorLinkRange{start: in.Start, end: in.End, labelStart: start, labelEnd: end})
			}
		}
	}
	return links
}

func (s *EditorState) linkRun(link editorLinkRange) *editorRun {
	for _, run := range s.runs {
		if run.layout == nil {
			continue
		}
		for i, at := range run.boundaries {
			if i < len(run.boundaries)-1 && at >= link.labelStart && at < link.labelEnd {
				return run
			}
		}
	}
	return nil
}

// Rendered link labels are editable text; their brackets, destination and
// reference suffix are hidden structure. Delete visible graphemes within a
// label, or the complete link when deleting across one of its outer edges.
func (s *EditorState) linkDeleteRange(dir int, word bool) (int, int, bool) {
	for _, link := range s.editableLinks() {
		at := s.caret
		if at < link.start || at > link.end || at == link.start && dir < 0 || at == link.end && dir > 0 {
			continue
		}
		if at < link.labelStart || at > link.labelEnd {
			return link.start, link.end, true
		}
		run := s.linkRun(link)
		if run == nil {
			return link.start, link.end, true
		}
		current := run.unitForSource(at)
		next := current
		if word {
			byteAt := ui.UTF16ByteOffset(run.text, current)
			next = editorUnits(run.text[:editorWordBoundary(run.text, byteAt, dir)])
		} else if dir < 0 {
			next = run.layout.PreviousBoundary(current)
		} else {
			next = run.layout.NextBoundary(current)
		}
		if next == current {
			return link.start, link.end, true
		}
		a, _ := run.sourceEdgesForUnit(min(current, next))
		_, z := run.sourceEdgesForUnit(max(current, next))
		if a >= link.labelStart && z <= link.labelEnd && a < z {
			return a, z, true
		}
		return link.start, link.end, true
	}
	return 0, 0, false
}

func (s *EditorState) linkHorizontal(dir int, extend bool) (int, ui.TextAffinity, bool) {
	for _, link := range s.editableLinks() {
		if s.caret < link.start || s.caret > link.end || s.caret == link.start && dir < 0 || s.caret == link.end && dir > 0 {
			continue
		}
		if run := s.linkRun(link); run != nil {
			current := run.unitForSource(s.caret)
			next := run.layout.MoveCaret(ui.TextCaretPosition{Index: current, Affinity: s.affinity}, dir)
			if next.Index != current {
				start, end := run.sourceEdgesForUnit(next.Index)
				return s.visualSelectionEdge(start, end, next.Affinity, extend), next.Affinity, true
			}
		}
		if dir < 0 {
			return link.start, ui.TextUpstream, true
		}
		return link.end, ui.TextDownstream, true
	}
	return 0, ui.TextDownstream, false
}

func (s *EditorState) protectLinkEdit(a, z int) (int, int) {
	for _, link := range s.editableLinks() {
		if a == z {
			switch {
			case a > link.start && a < link.labelStart:
				a, z = link.labelStart, link.labelStart
			case a > link.labelEnd && a < link.end:
				a, z = link.labelEnd, link.labelEnd
			}
		} else if a < link.end && z > link.start && !(a >= link.labelStart && z <= link.labelEnd) {
			a, z = min(a, link.start), max(z, link.end)
		}
	}
	return a, z
}
