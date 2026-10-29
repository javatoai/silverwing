package desktop

import "sort"

func jsonEditorGutterLines(state *toolPageState) []int {
	e := &state.jsonEditor
	if e.gutterLines == nil || e.gutterLinesProjection != e.projectionRevision {
		e.gutterLines = indexedJSONSourceLineNumbers(e.sourceIndex, e.projection)
		e.gutterLinesProjection = e.projectionRevision
	}
	return e.gutterLines
}

func jsonEditorSourceLineNumbers(source string, projection jsonTextProjection) []int {
	return indexedJSONSourceLineNumbers(newJSONTextIndex(source), projection)
}

func indexedJSONSourceLineNumbers(source *jsonTextIndex, projection jsonTextProjection) []int {
	lines := make([]int, len(projection.index.lineStarts))
	for i, start := range projection.index.lineStarts {
		lines[i] = source.lineAtRune(originalOffset(projection, int(start))) + 1
	}
	return lines
}

// Find ordinals belong to the full source. Only visible matches are painted,
// so a folded earlier hit must not shift the current hit's highlight.
func jsonEditorProjectedFindIndex(state *toolPageState, matches []jsonEditorMatch) int {
	if !state.jsonEditor.finding || len(matches) == 0 {
		return -1
	}
	match := matches[bounded(state.jsonEditor.findIndex, 0, len(matches)-1)]
	start := projectedOffset(state.jsonEditor.projection, match.start)
	e := &state.jsonEditor
	e.projectedMatches.update(e.projection, e.projectionRevision, e.findQuery)
	i := sort.Search(len(e.projectedMatches.matches), func(i int) bool { return e.projectedMatches.matches[i].start >= start })
	if i < len(e.projectedMatches.matches) && e.projectedMatches.matches[i].start == start {
		return i
	}
	return -1
}
