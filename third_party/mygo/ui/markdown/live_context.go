package markdown

import "github.com/egoist/mygo/ui/markdown/parser"

type SelectionContext string

const (
	ContextProse  SelectionContext = "prose"
	ContextCode   SelectionContext = "code"
	ContextTable  SelectionContext = "table"
	ContextSource SelectionContext = "source"
)

func (s *EditorState) SelectionContext() SelectionContext {
	s.sync()
	if s.SourceMode {
		return ContextSource
	}
	a, z := min(s.anchor, s.caret), max(s.anchor, s.caret)
	if s.writingFrontmatter(a) || s.writingFrontmatter(z) {
		return ContextSource
	}
	if _, ok := s.editingCode(a, z); ok {
		return ContextCode
	}
	if _, _, ok := s.editingTable(); ok {
		return ContextTable
	}
	d := s.editingDocument()
	for i := 1; i < d.Len(); i++ {
		if d.Block(i).Kind == parser.Table {
			start, end := s.blockRange(d.Block(i))
			if a >= start && z <= end {
				return ContextTable
			}
		}
	}
	return ContextProse
}
