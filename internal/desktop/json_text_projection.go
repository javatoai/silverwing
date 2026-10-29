package desktop

import (
	"sort"
	"strings"
	"unicode/utf8"

	"silverwing-go/internal/tools"
)

type jsonEditorFold struct {
	pointer string
	start   int // original rune offset, inclusive
	end     int // original rune offset, exclusive
}

type jsonTextProjection struct {
	text       string
	viewToText []int32 // nil for identity; otherwise one original rune offset per view boundary
	folds      []jsonEditorFold
	index      *jsonTextIndex
}

// The key describes source coordinates, not just displayed text. In particular,
// a parse or fold change can produce the same text with different source edges.
type jsonProjectionKey struct {
	source        string
	parseRevision uint64
	foldRevision  uint64
	valid         bool
}

func eligibleJSONFold(node tools.JSONSourceNode) bool {
	return node.IsContainer && node.EndLine > node.StartLine
}

func identityProjection(source string) jsonTextProjection {
	return identityJSONProjection(newJSONTextIndex(source))
}

func identityJSONProjection(index *jsonTextIndex) jsonTextProjection {
	text := index.source
	if index.hasBareCR || index.hasInvalidUTF8 {
		var out strings.Builder
		out.Grow(len(text))
		for at, point := range text {
			if point == '\r' && (at+1 == len(text) || text[at+1] != '\n') {
				point = '\n'
			}
			out.WriteRune(point)
		}
		text = out.String()
	}
	// Replacing bare CR with LF does not change any byte/rune/UTF-16 boundary.
	if index.hasInvalidUTF8 {
		index = newJSONTextIndex(text)
	}
	return jsonTextProjection{text: text, index: index}
}

func makeJSONTextProjection(source string, nodes []tools.JSONSourceNode, folded map[string]bool) jsonTextProjection {
	return makeIndexedJSONTextProjection(newJSONTextIndex(source), nodes, folded)
}

func makeIndexedJSONTextProjection(index *jsonTextIndex, nodes []tools.JSONSourceNode, folded map[string]bool) jsonTextProjection {
	if len(folded) == 0 || index.source == "" || len(nodes) == 0 {
		return identityJSONProjection(index)
	}
	folds := make([]jsonEditorFold, 0, len(folded))
	for _, node := range nodes {
		if !folded[node.Pointer] || !eligibleJSONFold(node) {
			continue
		}
		start, end := index.runeAtUTF16(node.StartOffset), index.runeAtUTF16(node.EndOffset)
		if start >= end {
			continue
		}
		folds = append(folds, jsonEditorFold{pointer: node.Pointer, start: start, end: end})
	}
	if len(folds) == 0 {
		return identityJSONProjection(index)
	}
	sort.Slice(folds, func(i, j int) bool {
		if folds[i].start != folds[j].start {
			return folds[i].start < folds[j].start
		}
		return folds[i].end > folds[j].end
	})
	var out strings.Builder
	positions := make([]int32, 1, index.runeCount()+1)
	kept := make([]jsonEditorFold, 0, len(folds))
	appendText := func(start, end int) {
		for i := start; i < end; i++ {
			at, next := index.byteAtRune(i), index.byteAtRune(i+1)
			if index.source[at] == '\r' && (next == len(index.source) || index.source[next] != '\n') {
				out.WriteByte('\n')
			} else if next-at == 1 && index.source[at] >= utf8.RuneSelf {
				out.WriteRune(utf8.RuneError)
			} else {
				out.WriteString(index.source[at:next])
			}
			positions = append(positions, int32(i+1))
		}
	}
	cursor := 0
	for _, fold := range folds {
		if fold.start < cursor {
			continue // an already folded ancestor hides this node
		}
		appendText(cursor, fold.start)
		positions[len(positions)-1] = int32(fold.start)
		// Editing across the placeholder replaces its full original range.
		out.WriteString(" … ")
		positions = append(positions, int32(fold.start), int32(fold.end), int32(fold.end))
		kept = append(kept, fold)
		cursor = fold.end
	}
	appendText(cursor, index.runeCount())
	text := out.String()
	return jsonTextProjection{text: text, viewToText: positions, folds: kept, index: newJSONTextIndex(text)}
}

func projectedOffset(projection jsonTextProjection, original int) int {
	if projection.index == nil {
		return 0
	}
	original = max(0, original)
	if projection.viewToText == nil {
		return min(original, projection.index.runeCount()+1)
	}
	return sort.Search(len(projection.viewToText), func(i int) bool { return int(projection.viewToText[i]) >= original })
}

func originalOffset(projection jsonTextProjection, view int) int {
	if projection.index == nil {
		return 0
	}
	if projection.viewToText == nil {
		return bounded(view, 0, projection.index.runeCount())
	}
	return int(projection.viewToText[bounded(view, 0, len(projection.viewToText)-1)])
}
