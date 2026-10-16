package desktop

import "github.com/egoist/mygo/ui"

type jsonEditorGutterCache struct {
	source, view string
	lines        []int
}

func jsonEditorGutterLines(field ui.Element, state *toolPageState) []int {
	cache := ui.Local(field, "json-original-line-numbers", func() jsonEditorGutterCache { return jsonEditorGutterCache{} })
	if cache.lines == nil || cache.source != state.jsonSource || cache.view != state.jsonEditor.projection.text {
		cache.source, cache.view = state.jsonSource, state.jsonEditor.projection.text
		cache.lines = jsonEditorSourceLineNumbers(state.jsonSource, state.jsonEditor.projection)
	}
	return cache.lines
}

func jsonEditorSourceLineNumbers(source string, projection jsonTextProjection) []int {
	sourceRunes, viewRunes := []rune(source), []rune(projection.text)
	cursor, line := 0, 1
	lineAt := func(offset int) int {
		for cursor < offset && cursor < len(sourceRunes) {
			if sourceRunes[cursor] == '\r' || sourceRunes[cursor] == '\n' && (cursor == 0 || sourceRunes[cursor-1] != '\r') {
				line++
			}
			cursor++
		}
		return line
	}
	lines := []int{lineAt(originalOffset(projection, 0))}
	for i := 0; i < len(viewRunes); i++ {
		if viewRunes[i] != '\r' && viewRunes[i] != '\n' {
			continue
		}
		if viewRunes[i] == '\r' && i+1 < len(viewRunes) && viewRunes[i+1] == '\n' {
			i++
		}
		lines = append(lines, lineAt(originalOffset(projection, i+1)))
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
	for i, visible := range jsonEditorMatches(state.jsonEditor.projection.text, state.jsonEditor.findQuery) {
		if visible.start == start {
			return i
		}
	}
	return -1
}
