package markdown

import (
	"github.com/egoist/mygo/ui/markdown/parser"
	"strings"
)

// A selection already contains Markdown escapes/entities. Keep that source
// rather than treating it as new literal text. Only unmatched label brackets
// need escaping; brackets in complete code spans and existing escapes are safe.
func (s *EditorState) selectedLinkLabel(a, z int) string {
	d := s.editingDocument()
	code := map[int]int{}
	for b := 1; b < d.Len(); b++ {
		for _, in := range d.Inlines(b) {
			if in.Kind == parser.Code && in.Start >= a && in.End <= z {
				code[in.Start] = in.End
			}
		}
	}
	var opened []int
	escape := map[int]bool{}
	for at := a; at < z; at++ {
		if end, ok := code[at]; ok {
			at = end - 1
			continue
		}
		if s.source[at] == '\\' && at+1 < z {
			at++
			continue
		}
		switch s.source[at] {
		case '[':
			opened = append(opened, at)
		case ']':
			if len(opened) > 0 {
				opened = opened[:len(opened)-1]
			} else {
				escape[at] = true
			}
		}
	}
	for _, at := range opened {
		escape[at] = true
	}
	var result strings.Builder
	for at := a; at < z; at++ {
		if escape[at] {
			result.WriteByte('\\')
		}
		if s.source[at] == '\n' {
			result.WriteByte(' ')
		} else {
			result.WriteByte(s.source[at])
		}
	}
	slashes := 0
	for at := z - 1; at >= a && s.source[at] == '\\'; at-- {
		slashes++
	}
	if slashes%2 != 0 {
		result.WriteByte('\\')
	} // a literal tail slash must not escape the new label's ]
	return result.String()
}
