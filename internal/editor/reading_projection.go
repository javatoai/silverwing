package editor

import (
	"bytes"
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
	return projectReading(source, false)
}

// ProjectRichTextReading adapts imported body/comment formatting to the native
// Markdown theme. Only AST-recognized span wrappers are omitted from the view.
func ProjectRichTextReading(source string) ReadingProjection {
	return projectReading(source, true)
}

func projectReading(source string, richTextHTML bool) ReadingProjection {
	normalized, offsets := normalizeLines(source)
	normalizedBytes := []byte(normalized)
	root := markdownParser.Parse(text.NewReader(normalizedBytes))
	mapper := sourceMapper{original: source, offsets: offsets}
	ranges := map[SourceRange]bool{}
	if richTextHTML {
		for _, span := range htmlSpanRanges(normalizedBytes) {
			a, b := mapper.rawRange(span.Start, span.End)
			ranges[SourceRange{a, b}] = true
		}
	}
	var links []Node
	previous := parsedBlock{}
	for child := root.FirstChild(); child != nil; child = child.NextSibling() {
		start := child.Pos()
		end := len(normalizedBytes)
		if next := child.NextSibling(); next != nil {
			end = next.Pos()
		}
		if start < 0 || end < start || end > len(normalizedBytes) {
			continue
		}
		current := parsedBlock{node: child, start: start, end: end, kind: classifyBlock(child, normalizedBytes, start, end)}
		if safeAdjacentBlockImageMetadata(previous, current, normalizedBytes) {
			a, b := mapper.rawRange(start, end)
			ranges[SourceRange{a, b}] = true
		}
		ignored := safeAdjacentPresentationMetadata(child, normalizedBytes, start, end)
		for _, r := range ignored {
			a, b := mapper.rawRange(r.Start, r.End)
			ranges[SourceRange{a, b}] = true
		}
		_ = gast.Walk(child, func(n gast.Node, entering bool) (gast.WalkStatus, error) {
			if entering {
				for link := range safeAdjacentLinkPreviewLinks(n, normalizedBytes, start, end, ignored) {
					a := inlineNodeSourceStart(link, normalizedBytes, start, end)
					b := inlineNodeEnd(link, normalizedBytes, a, end)
					x, y := mapper.rawRange(a, b)
					links = append(links, Node{Kind: "link-preview", Range: SourceRange{x, y}, Destination: string(link.Destination), Text: string(link.Text(normalizedBytes))})
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

func htmlSpanRanges(source []byte) []SourceRange {
	var ranges []SourceRange
	collectHTMLSpanRanges(source, 0, &ranges)
	return ranges
}

func collectHTMLSpanRanges(source []byte, base int, ranges *[]SourceRange) {
	root := markdownParser.Parse(text.NewReader(source))
	_ = gast.Walk(root, func(node gast.Node, entering bool) (gast.WalkStatus, error) {
		if !entering {
			return gast.WalkContinue, nil
		}
		switch value := node.(type) {
		case *gast.RawHTML:
			if value.Segments != nil && value.Segments.Len() > 0 {
				first := value.Segments.At(0)
				last := value.Segments.At(value.Segments.Len() - 1)
				if first.Start >= 0 && last.Stop >= first.Start && last.Stop <= len(source) && isHTMLSpanToken(source[first.Start:last.Stop]) {
					*ranges = append(*ranges, SourceRange{Start: base + first.Start, End: base + last.Stop})
				}
			}
		case *gast.HTMLBlock:
			collectHTMLBlockSpanRanges(value, source, base, ranges)
		}
		return gast.WalkContinue, nil
	})
}

func collectHTMLBlockSpanRanges(block *gast.HTMLBlock, source []byte, base int, ranges *[]SourceRange) {
	lines := block.Lines()
	if lines == nil || lines.Len() == 0 {
		return
	}
	firstLine := lines.At(0)
	lastLine := lines.At(lines.Len() - 1)
	if firstLine.Start < 0 || lastLine.Stop < firstLine.Start || lastLine.Stop > len(source) {
		return
	}

	allSpanLines := true
	for index := 0; index < lines.Len(); index++ {
		if _, _, ok := standaloneHTMLSpanToken(source, lines.At(index)); !ok {
			allSpanLines = false
			break
		}
	}
	if allSpanLines {
		for index := 0; index < lines.Len(); index++ {
			start, end, _ := standaloneHTMLSpanToken(source, lines.At(index))
			*ranges = append(*ranges, SourceRange{Start: base + start, End: base + end})
		}
		return
	}

	cutEnd := firstLine.Start
	removedLeadingSpans := false
	for index := 0; index < lines.Len(); index++ {
		line := lines.At(index)
		start, end, ok := standaloneHTMLSpanToken(source, line)
		if !ok {
			break
		}
		*ranges = append(*ranges, SourceRange{Start: base + start, End: base + end})
		removedLeadingSpans = true
		cutEnd = line.Stop
		if cutEnd < len(source) && source[cutEnd] == '\n' {
			cutEnd++
		}
	}
	if removedLeadingSpans && cutEnd <= lastLine.Stop {
		collectHTMLSpanRanges(source[cutEnd:lastLine.Stop], base+cutEnd, ranges)
	}
}

func standaloneHTMLSpanToken(source []byte, segment text.Segment) (int, int, bool) {
	if segment.Start < 0 || segment.Stop < segment.Start || segment.Stop > len(source) {
		return 0, 0, false
	}
	start, end := segment.Start, segment.Stop
	for start < end && isHTMLSpace(source[start]) {
		start++
	}
	for end > start && isHTMLSpace(source[end-1]) {
		end--
	}
	token := source[start:end]
	if !isHTMLSpanToken(token) {
		return 0, 0, false
	}
	return start, end, true
}

func isHTMLSpanToken(token []byte) bool {
	if len(token) < 6 || token[0] != '<' {
		return false
	}

	index := 1
	closing := token[index] == '/'
	if closing {
		index++
	}
	if len(token)-index < len("span") || !bytes.EqualFold(token[index:index+len("span")], []byte("span")) {
		return false
	}
	index += len("span")
	if index >= len(token) {
		return false
	}

	if closing {
		for index < len(token)-1 && isHTMLSpace(token[index]) {
			index++
		}
		return index == len(token)-1 && token[index] == '>'
	}

	if token[index] != '>' && !isHTMLSpace(token[index]) && token[index] != '/' {
		return false
	}
	var quote byte
	for ; index < len(token); index++ {
		value := token[index]
		if quote != 0 {
			if value == quote {
				quote = 0
			}
			continue
		}
		switch value {
		case '\'', '"':
			quote = value
		case '<':
			return false
		case '>':
			return index == len(token)-1
		}
	}
	return false
}

func isHTMLSpace(value byte) bool {
	switch value {
	case ' ', '\t', '\n', '\r', '\f':
		return true
	default:
		return false
	}
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
