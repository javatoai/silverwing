package markdownui

import (
	"fmt"
	"strings"

	code "github.com/egoist/mygo/ui/editor"
	md "github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
)

type linkRemovalEdit struct {
	start, end int
	text       string
}

func unlinkInContext(snapshot code.Snapshot, start, end int, label string) (linkRemovalEdit, error) {
	source := snapshot.String()
	// Rebuild only the current link for analysis, allowing edited label text
	// without changing other references. Parser coordinates remain normalized;
	// label itself retains its original CR/LF bytes for the actual replacement.
	normalized := strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(label)
	before := parser.Parse(source[:start] + "[" + normalized + "](<https://silverwing.invalid/unlink>)" + source[end:])
	replacement := unlinkLabel(label)
	edit := linkRemovalEdit{start, end, replacement}
	if sameUnlinkedPresentation(before, parser.Parse(source[:start]+replacement+source[end:]), start) {
		return edit, nil
	}
	// Empty tags protect block syntax. Literal text delimiters additionally
	// need entities: emphasis can pair across tags, and code ignores escapes.
	replacement = unlinkLabel(unlinkLiteralDelimiters(before, start, label))
	edit.text = "<i></i>" + replacement + "<i></i>"
	if sameUnlinkedPresentation(before, parser.Parse(source[:start]+edit.text+source[end:]), start) {
		return edit, nil
	}
	// A valid inner link can disable surrounding literal link syntax. Once
	// unwrapped, guard tags alone cannot keep those outer brackets literal.
	// Escape only literal opening brackets before this link in its own block;
	// skip other actual links and retain original bytes between the escapes.
	brackets := outerLiteralBrackets(before, start)
	if len(brackets) > 0 {
		var prefix strings.Builder
		cursor := brackets[0]
		for _, at := range brackets {
			prefix.WriteString(snapshot.OriginalSlice(cursor, at))
			prefix.WriteString("&#91;")
			cursor = at + 1
		}
		prefix.WriteString(snapshot.OriginalSlice(cursor, start))
		edit.start = brackets[0]
		edit.text = prefix.String() + edit.text
		if sameUnlinkedPresentation(before, parser.Parse(source[:edit.start]+edit.text+source[end:]), start) {
			return edit, nil
		}
	}
	return linkRemovalEdit{}, fmt.Errorf("无法在保留原有格式的情况下移除该链接")
}

func outerLiteralBrackets(doc *parser.Document, start int) []int {
	for block := 1; block < doc.Len(); block++ {
		inlines := doc.Inlines(block)
		found := false
		for _, in := range inlines {
			if in.Kind == parser.Link && in.Start == start {
				found = true
				break
			}
		}
		if !found {
			continue
		}
		var brackets []int
		for index := 0; index < len(inlines); {
			node := inlines[index]
			if node.Start >= start {
				break
			}
			if node.Kind == parser.Link || node.Kind == parser.Image {
				index = node.Next
				continue
			}
			if node.Kind == parser.Text {
				for at := node.Start; at < min(node.End, start); at++ {
					if doc.Source()[at] == '[' {
						slashes := 0
						for i := at - 1; i >= node.Start && doc.Source()[i] == '\\'; i-- {
							slashes++
						}
						if slashes%2 == 0 {
							brackets = append(brackets, at)
						}
					}
				}
			}
			index++ // containers can hold literal text before the target link
		}
		return brackets
	}
	return nil
}

func unlinkLiteralDelimiters(doc *parser.Document, start int, label string) string {
	var ranges [][2]int
	for block := 1; block < doc.Len(); block++ {
		inlines := doc.Inlines(block)
		for index, link := range inlines {
			if link.Kind != parser.Link || link.Start != start {
				continue
			}
			for _, node := range inlines[index+1 : link.Next] {
				if node.Kind == parser.Text {
					ranges = append(ranges, [2]int{node.Start, node.End})
				}
			}
		}
	}
	var out strings.Builder
	position, current, copied := start+1, 0, 0
	for at := 0; at < len(label); at++ {
		for current < len(ranges) && ranges[current][1] <= position {
			current++
		}
		entity := ""
		if current < len(ranges) && position >= ranges[current][0] {
			switch label[at] {
			case '*':
				entity = "&#42;"
			case '_':
				entity = "&#95;"
			case '~':
				entity = "&#126;"
			case '`':
				entity = "&#96;"
			case '[':
				entity = "&#91;"
			case ']':
				entity = "&#93;"
			}
		}
		if entity != "" {
			first, slashes := at, 0
			for i := at - 1; i >= 0 && label[i] == '\\'; i-- {
				slashes++
			}
			if slashes%2 != 0 {
				first-- // replace the literal's escape as well as the delimiter
			}
			out.WriteString(label[copied:first])
			out.WriteString(entity)
			copied = at + 1
		}
		if label[at] == '\r' && at+1 < len(label) && label[at+1] == '\n' {
			at++
		}
		position++
	}
	if copied == 0 {
		return label
	}
	out.WriteString(label[copied:])
	return out.String()
}

func sameUnlinkedPresentation(a, b *parser.Document, removedStart int) bool {
	if a.Len() != b.Len() {
		return false
	}
	for i := 0; i < a.Len(); i++ {
		x, y := a.Block(i), b.Block(i)
		if x.Kind != y.Kind || x.Parent != y.Parent || x.Next != y.Next || x.Level != y.Level || x.Header != y.Header {
			return false
		}
		if inlinePresentation(a.Inlines(i), removedStart) != inlinePresentation(b.Inlines(i), -1) {
			return false
		}
	}
	// Definitions can consume the label and resolve other references while
	// leaving the block tree unchanged. Their displayed text must also match.
	x, y := md.TextBlocks(a), md.TextBlocks(b)
	if len(x) != len(y) {
		return false
	}
	for i := range x {
		if x[i].Text != y[i].Text {
			return false
		}
	}
	return true
}

// Compare semantic inline nesting, omitting only the link being removed and
// our zero-width guard tags. Adjacent text nodes are joined so entities and
// harmless parser segmentation do not count as formatting changes.
func inlinePresentation(inlines []parser.Inline, removedStart int) string {
	var out, text strings.Builder
	flush := func() {
		if text.Len() > 0 {
			fmt.Fprintf(&out, "text(%q)", text.String())
			text.Reset()
		}
	}
	var visit func(int, int)
	visit = func(first, end int) {
		for index := first; index < end; {
			node := inlines[index]
			if node.Kind == parser.HTML {
				if node.Text == "<i></i>" {
					index = node.Next
					continue
				}
				if node.Text == "<i>" && index+1 < end && inlines[index+1].Kind == parser.HTML && inlines[index+1].Text == "</i>" {
					index = inlines[index+1].Next
					continue
				}
			}
			if node.Kind == parser.Text {
				text.WriteString(node.Text)
			} else if node.Kind == parser.Link && node.Start == removedStart {
				visit(index+1, node.Next)
			} else {
				flush()
				fmt.Fprintf(&out, "(%d,%q,%q,%q,%t:", node.Kind, node.Text, node.Dest, node.Title, node.Display)
				visit(index+1, node.Next)
				flush()
				out.WriteByte(')')
			}
			index = node.Next
		}
	}
	visit(0, len(inlines))
	flush()
	return out.String()
}
