package markdowntext

import (
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// SemanticNodes parses an already classified rich source range without
// whole-document fallback classification. Source offsets remain unchanged.
func SemanticNodes(source string) []Node {
	normalized, offsets := NormalizeLines(source)
	normalizedBytes := []byte(normalized)
	root := markdownParser.Parse(text.NewReader(normalizedBytes))
	mapper := sourceMapper{original: source, offsets: offsets}

	children := make([]gast.Node, 0, root.ChildCount())
	for child := root.FirstChild(); child != nil; child = child.NextSibling() {
		start := child.Pos()
		if start >= 0 && start <= len(normalizedBytes) {
			children = append(children, child)
		}
	}

	nodes := make([]Node, 0, len(children))
	for index, child := range children {
		start := child.Pos()
		end := len(normalizedBytes)
		for next := index + 1; next < len(children); next++ {
			nextStart := children[next].Pos()
			if nextStart >= start && nextStart <= end {
				end = nextStart
				break
			}
		}
		startRaw, endRaw := mapper.rawRange(start, end)
		node := convertBlock(child, normalizedBytes, mapper, SourceRange{Start: startRaw, End: endRaw}, PartRich)
		nodes = append(nodes, node)
	}
	return nodes
}
