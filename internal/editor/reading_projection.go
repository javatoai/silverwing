package editor

import (
	"sort"
	"strings"

	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
)

// ReadingProjection omits only AST-validated presentation annotations. Source
// remains owned by Document; neither cache bytes nor code are rewritten.
type ReadingProjection struct {
	Source       string
	Removed      []SourceRange
	LinkPreviews []Node
}

func ProjectReading(source string) ReadingProjection {
	normalized, offsets := normalizeLines(source)
	bytes := []byte(normalized)
	root := markdownParser.Parse(text.NewReader(bytes))
	mapper := sourceMapper{original: source, offsets: offsets}
	ranges := map[SourceRange]bool{}
	var links []Node
	previous := parsedBlock{}
	for child := root.FirstChild(); child != nil; child = child.NextSibling() {
		start := child.Pos()
		end := len(bytes)
		if next := child.NextSibling(); next != nil {
			end = next.Pos()
		}
		if start < 0 || end < start || end > len(bytes) {
			continue
		}
		current := parsedBlock{node: child, start: start, end: end, kind: classifyBlock(child, bytes, start, end)}
		if safeAdjacentBlockImageMetadata(previous, current, bytes) {
			a, b := mapper.rawRange(start, end)
			ranges[SourceRange{a, b}] = true
		}
		ignored := safeAdjacentPresentationMetadata(child, bytes, start, end)
		for _, r := range ignored {
			a, b := mapper.rawRange(r.Start, r.End)
			ranges[SourceRange{a, b}] = true
		}
		_ = gast.Walk(child, func(n gast.Node, entering bool) (gast.WalkStatus, error) {
			if entering {
				for link := range safeAdjacentLinkPreviewLinks(n, bytes, start, end, ignored) {
					a := inlineNodeSourceStart(link, bytes, start, end)
					b := inlineNodeEnd(link, bytes, a, end)
					x, y := mapper.rawRange(a, b)
					links = append(links, Node{Kind: "link-preview", Range: SourceRange{x, y}, Destination: string(link.Destination), Text: string(link.Text(bytes))})
				}
			}
			return gast.WalkContinue, nil
		})
		previous = current
	}
	removed := make([]SourceRange, 0, len(ranges))
	for r := range ranges {
		removed = append(removed, r)
	}
	sort.Slice(removed, func(i, j int) bool { return removed[i].Start < removed[j].Start })
	var output strings.Builder
	cursor := 0
	for _, r := range removed {
		if r.Start < cursor {
			continue
		}
		output.WriteString(source[cursor:r.Start])
		cursor = r.End
	}
	output.WriteString(source[cursor:])
	return ReadingProjection{Source: output.String(), Removed: removed, LinkPreviews: links}
}

func (p ReadingProjection) OriginalOffset(offset int) int {
	original := offset
	for _, r := range p.Removed {
		if original < r.Start {
			break
		}
		original += r.End - r.Start
	}
	return original
}
