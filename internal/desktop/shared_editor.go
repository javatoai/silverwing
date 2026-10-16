package desktop

import (
	"sort"
	"strings"
	"unicode"

	"silverwing-go/internal/tools"

	"github.com/egoist/mygo/ui"
)

// jsonEditorSnapshot is the application's undo checkpoint. MyGO's TextArea
// history covers ordinary typing, but setting its bound value for a format
// operation starts a new native history. This history keeps both kinds of
// changes in one sequence.
type jsonEditorSnapshot struct {
	text           string
	selectionStart int
	selectionEnd   int
}

type jsonEditorFold struct {
	pointer string
	start   int // original rune offset, inclusive
	end     int // original rune offset, exclusive
}

type jsonTextProjection struct {
	text       string
	viewToText []int // one original rune offset for every projected boundary
	folds      []jsonEditorFold
}

type jsonEditorMatch struct {
	start int
	end   int
}

func initializeJSONEditor(state *toolPageState) {
	state.jsonEditor = jsonToolState{
		viewSource:     state.jsonSource,
		sourceSnapshot: state.jsonSource,
		projection:     identityProjection(state.jsonSource),
		folded:         make(map[string]bool),
		history:        []jsonEditorSnapshot{{text: state.jsonSource}},
		historyReady:   true,
	}
}

func (state *toolPageState) syncJSONEditor() {
	e := &state.jsonEditor
	if !e.historyReady {
		initializeJSONEditor(state)
		return
	}
	if e.sourceSnapshot != state.jsonSource {
		e.sourceSnapshot = state.jsonSource
		e.folded = make(map[string]bool)
		e.projection = identityProjection(state.jsonSource)
		e.viewSource = state.jsonSource
		e.record(state.jsonSource, e.lastSelection.selectionStart, e.lastSelection.selectionEnd)
		e.setPendingSelection(e.lastSelection.selectionStart, e.lastSelection.selectionEnd)
	}
	nodes := []tools.JSONSourceNode(nil)
	if state.jsonValid {
		nodes = state.jsonDoc.Nodes
	}
	expected := makeJSONTextProjection(state.jsonSource, nodes, e.folded)
	if expected.text != e.projection.text || e.viewSource != expected.text {
		e.projection = expected
		e.viewSource = expected.text
	}
}

func (e *jsonToolState) record(text string, selectionStart, selectionEnd int) {
	if !e.historyReady {
		e.history = []jsonEditorSnapshot{{text: text, selectionStart: selectionStart, selectionEnd: selectionEnd}}
		e.historyIndex, e.historyReady = 0, true
		return
	}
	if len(e.history) == 0 {
		e.history = []jsonEditorSnapshot{{text: text, selectionStart: selectionStart, selectionEnd: selectionEnd}}
		e.historyIndex = 0
		return
	}
	if e.historyIndex < 0 || e.historyIndex >= len(e.history) {
		e.historyIndex = len(e.history) - 1
	}
	if e.history[e.historyIndex].text == text {
		e.history[e.historyIndex].selectionStart = selectionStart
		e.history[e.historyIndex].selectionEnd = selectionEnd
		return
	}
	e.history = e.history[:e.historyIndex+1]
	e.history = append(e.history, jsonEditorSnapshot{text: text, selectionStart: selectionStart, selectionEnd: selectionEnd})
	if len(e.history) > 81 {
		e.history = append([]jsonEditorSnapshot(nil), e.history[len(e.history)-81:]...)
	}
	e.historyIndex = len(e.history) - 1
}

func (e *jsonToolState) setPendingSelection(start, end int) {
	e.selectionStart = bounded(start, 0, utf8RuneCount(e.viewSource))
	e.selectionEnd = bounded(end, 0, utf8RuneCount(e.viewSource))
	e.pendingSelection = true
	e.pendingFocus = true
}

func (e *jsonToolState) setPendingSelectionWithoutFocus(start, end int) {
	e.selectionStart = bounded(start, 0, utf8RuneCount(e.viewSource))
	e.selectionEnd = bounded(end, 0, utf8RuneCount(e.viewSource))
	e.pendingSelection = true
	e.pendingFocus = false
}

func (e *jsonToolState) setPendingSourceSelection(start, end int) {
	e.selectionStart = projectedOffset(e.projection, start)
	e.selectionEnd = projectedOffset(e.projection, end)
	e.pendingSelection = true
	e.pendingFocus = true
}

func (s *app) applyJSONEditorChange(state *toolPageState, editor ui.Element) {
	selectionStart, selectionEnd := editor.TextSelection()
	s.applyJSONEditorChangeAt(state, selectionStart, selectionEnd)
}

func (s *app) applyJSONEditorChangeAt(state *toolPageState, selectionStart, selectionEnd int) {
	e := &state.jsonEditor
	if e.viewSource == e.projection.text {
		return
	}
	start, end, inserted, prefix, oldEnd, newEnd := projectionEdit(e.projection, e.viewSource)
	sourceSelectionStart, sourceSelectionEnd := projectionSelectionAfterEdit(e.projection, prefix, oldEnd, newEnd, selectionStart, selectionEnd)
	next := replaceRuneRange(state.jsonSource, start, end, inserted)
	s.commitJSONEditorSourceEdit(state, next, sourceSelectionStart, sourceSelectionEnd)
}

func (s *app) applyJSONEditorPaste(state *toolPageState, pasted string, selectionStart, selectionEnd int) bool {
	if pasted == "" {
		return false
	}
	return s.replaceJSONEditorSelection(state, pasted, selectionStart, selectionEnd)
}

func (s *app) replaceJSONEditorSelection(state *toolPageState, replacement string, selectionStart, selectionEnd int) bool {
	e := &state.jsonEditor
	start := originalOffset(e.projection, selectionStart)
	end := originalOffset(e.projection, selectionEnd)
	next := replaceRuneRange(state.jsonSource, start, end, replacement)
	if developerToolInputTooLarge(next) {
		s.rejectJSONToolInput(state)
		return false
	}
	caret := start + utf8RuneCount(replacement)
	s.commitJSONEditorSourceEdit(state, next, caret, caret)
	return true
}

func (s *app) commitJSONEditorSourceEdit(state *toolPageState, next string, sourceSelectionStart, sourceSelectionEnd int) {
	e := &state.jsonEditor
	state.jsonSource = next
	e.sourceSnapshot = next
	e.folded = make(map[string]bool) // Source expands folds when the text changes.
	e.projection = identityProjection(next)
	e.viewSource = next
	e.lastSelection = jsonEditorSnapshot{text: next, selectionStart: sourceSelectionStart, selectionEnd: sourceSelectionEnd}
	e.record(next, sourceSelectionStart, sourceSelectionEnd)
	e.setPendingSelection(sourceSelectionStart, sourceSelectionEnd)
	s.queueJSONValidation()
}

func (state *toolPageState) commitProgrammaticJSONSource(source string) {
	e := &state.jsonEditor
	selectionStart, selectionEnd := e.lastSelection.selectionStart, e.lastSelection.selectionEnd
	if source != state.jsonSource {
		e.record(source, selectionStart, selectionEnd)
	}
	state.jsonSource = source
	e.sourceSnapshot = source
	e.folded = make(map[string]bool)
	e.projection = identityProjection(source)
	e.viewSource = source
	selectionStart = bounded(selectionStart, 0, utf8RuneCount(source))
	selectionEnd = bounded(selectionEnd, 0, utf8RuneCount(source))
	e.lastSelection = jsonEditorSnapshot{text: source, selectionStart: selectionStart, selectionEnd: selectionEnd}
	e.setPendingSelection(selectionStart, selectionEnd)
}

func (s *app) moveJSONEditorHistory(state *toolPageState, redo bool) {
	e := &state.jsonEditor
	if !e.historyReady || len(e.history) == 0 {
		return
	}
	if redo {
		if e.historyIndex+1 >= len(e.history) {
			return
		}
		e.historyIndex++
	} else {
		if e.historyIndex <= 0 {
			return
		}
		e.historyIndex--
	}
	snapshot := e.history[e.historyIndex]
	state.jsonSource = snapshot.text
	e.sourceSnapshot = snapshot.text
	e.folded = make(map[string]bool)
	e.projection = identityProjection(snapshot.text)
	e.viewSource = snapshot.text
	e.lastSelection = snapshot
	e.setPendingSelection(snapshot.selectionStart, snapshot.selectionEnd)
	s.queueJSONValidation()
}

func (s *app) handleJSONEditorHistoryInput(state *toolPageState, event ui.InputEvent) bool {
	// TextArea consumes its editing keys before page shortcuts. Intercept these
	// keys on the focused source field so projected text never supplies undo.
	if event.Kind != ui.InputKeyDown {
		return false
	}
	switch {
	case event.Mods == ui.Cmd && event.Key == ui.KeyZ:
		s.moveJSONEditorHistory(state, false)
	case event.Mods == ui.Cmd|ui.Shift && event.Key == ui.KeyZ,
		event.Mods == ui.Cmd && event.Key == ui.KeyY:
		s.moveJSONEditorHistory(state, true)
	default:
		return false
	}
	return true
}

func eligibleJSONFold(node tools.JSONSourceNode) bool {
	return node.IsContainer && node.EndLine > node.StartLine
}

func jsonFoldNodesByVisibleLine(state *toolPageState) map[int]tools.JSONSourceNode {
	e := &state.jsonEditor
	if e.foldCache != nil && e.foldCacheSource == state.jsonSource && e.foldCacheView == e.projection.text &&
		e.foldCacheRevision == state.jsonRevision && e.foldCacheValid == state.jsonValid {
		return e.foldCache
	}
	result := make(map[int]tools.JSONSourceNode)
	if !state.jsonValid {
		e.foldCache, e.foldCacheSource, e.foldCacheView = result, state.jsonSource, e.projection.text
		e.foldCacheRevision, e.foldCacheValid = state.jsonRevision, false
		return result
	}
	viewRunes := []rune(state.jsonEditor.projection.text)
	lineAt := make([]int, len(viewRunes)+1)
	line := 0
	for i, r := range viewRunes {
		lineAt[i] = line
		if r == '\n' {
			line++
		}
	}
	lineAt[len(viewRunes)] = line
	for _, node := range state.jsonDoc.Nodes {
		if !eligibleJSONFold(node) {
			continue
		}
		start := utf16OffsetToRune(state.jsonSource, node.StartOffset)
		end := utf16OffsetToRune(state.jsonSource, node.EndOffset)
		hidden := false
		foldIndex := sort.Search(len(state.jsonEditor.projection.folds), func(i int) bool {
			return state.jsonEditor.projection.folds[i].start > start
		}) - 1
		if foldIndex >= 0 {
			collapsed := state.jsonEditor.projection.folds[foldIndex]
			hidden = collapsed.pointer != node.Pointer && start >= collapsed.start && end <= collapsed.end
		}
		if hidden {
			continue
		}
		viewStart := projectedOffset(state.jsonEditor.projection, start)
		viewStart = bounded(viewStart, 0, len(viewRunes))
		visibleLine := lineAt[viewStart]
		if existing, ok := result[visibleLine]; ok {
			existingStart := utf16OffsetToRune(state.jsonSource, existing.StartOffset)
			existingEnd := utf16OffsetToRune(state.jsonSource, existing.EndOffset)
			if existingEnd-existingStart <= end-start {
				continue
			}
		}
		result[visibleLine] = node
	}
	e.foldCache, e.foldCacheSource, e.foldCacheView = result, state.jsonSource, e.projection.text
	e.foldCacheRevision, e.foldCacheValid = state.jsonRevision, true
	return result
}

func (s *app) toggleJSONFold(state *toolPageState, pointer string) {
	node, ok := jsonNodeByPointer(state.jsonDoc.Nodes, pointer)
	if !ok || !eligibleJSONFold(node) {
		return
	}
	e := &state.jsonEditor
	if e.folded[pointer] {
		delete(e.folded, pointer)
	} else {
		e.folded[pointer] = true
	}
	e.projection = makeJSONTextProjection(state.jsonSource, state.jsonDoc.Nodes, e.folded)
	e.viewSource = e.projection.text
	e.setPendingSourceSelection(e.lastSelection.selectionStart, e.lastSelection.selectionEnd)
}

func (s *app) foldJSONAtCaret(state *toolPageState) {
	caret := state.jsonEditor.lastSelection.selectionStart
	var chosen tools.JSONSourceNode
	chosenSpan := int(^uint(0) >> 1)
	for _, node := range state.jsonDoc.Nodes {
		if !eligibleJSONFold(node) {
			continue
		}
		start := utf16OffsetToRune(state.jsonSource, node.StartOffset)
		end := utf16OffsetToRune(state.jsonSource, node.EndOffset)
		if caret < start || caret > end {
			continue
		}
		if span := end - start; span < chosenSpan {
			chosen, chosenSpan = node, span
		}
	}
	if chosenSpan == int(^uint(0)>>1) {
		root, ok := jsonNodeByPointer(state.jsonDoc.Nodes, "")
		if !ok || !eligibleJSONFold(root) {
			return
		}
		chosen = root
	}
	s.toggleJSONFold(state, chosen.Pointer)
}

func (s *app) expandAllJSONFolds(state *toolPageState) {
	if len(state.jsonEditor.folded) == 0 {
		return
	}
	state.jsonEditor.folded = make(map[string]bool)
	state.jsonEditor.projection = identityProjection(state.jsonSource)
	state.jsonEditor.viewSource = state.jsonSource
	state.jsonEditor.setPendingSourceSelection(state.jsonEditor.lastSelection.selectionStart, state.jsonEditor.lastSelection.selectionEnd)
}

func revealJSONProjectionRange(state *toolPageState, start, end int) {
	e := &state.jsonEditor
	changed := false
	for pointer := range e.folded {
		node, ok := jsonNodeByPointer(state.jsonDoc.Nodes, pointer)
		if !ok {
			delete(e.folded, pointer)
			changed = true
			continue
		}
		nodeStart := utf16OffsetToRune(state.jsonSource, node.StartOffset)
		nodeEnd := utf16OffsetToRune(state.jsonSource, node.EndOffset)
		if start < nodeEnd && end >= nodeStart {
			delete(e.folded, pointer)
			changed = true
		}
	}
	if changed {
		e.projection = makeJSONTextProjection(state.jsonSource, state.jsonDoc.Nodes, e.folded)
		e.viewSource = e.projection.text
	}
}

func identityProjection(source string) jsonTextProjection {
	runes := []rune(source)
	var out strings.Builder
	positions := make([]int, 1, len(runes)+1)
	for i, r := range runes {
		if r == '\r' && (i+1 == len(runes) || runes[i+1] != '\n') {
			out.WriteRune('\n')
		} else {
			out.WriteRune(r)
		}
		positions = append(positions, i+1)
	}
	return jsonTextProjection{text: out.String(), viewToText: positions}
}

func makeJSONTextProjection(source string, nodes []tools.JSONSourceNode, folded map[string]bool) jsonTextProjection {
	if len(folded) == 0 || source == "" || len(nodes) == 0 {
		return identityProjection(source)
	}
	runes := []rune(source)
	folds := make([]jsonEditorFold, 0, len(folded))
	for _, node := range nodes {
		if !folded[node.Pointer] || !node.IsContainer || node.EndLine <= node.StartLine {
			continue
		}
		start := utf16OffsetToRune(source, node.StartOffset)
		end := utf16OffsetToRune(source, node.EndOffset)
		if start < 0 || end > len(runes) || start >= end {
			continue
		}
		folds = append(folds, jsonEditorFold{pointer: node.Pointer, start: start, end: end})
	}
	sort.Slice(folds, func(i, j int) bool {
		if folds[i].start != folds[j].start {
			return folds[i].start < folds[j].start
		}
		return folds[i].end > folds[j].end
	})

	var out strings.Builder
	positions := make([]int, 1, len(runes)+1)
	positions[0] = 0
	kept := make([]jsonEditorFold, 0, len(folds))
	appendText := func(start, end int) {
		for i := start; i < end; i++ {
			if runes[i] == '\r' && (i+1 == len(runes) || runes[i+1] != '\n') {
				out.WriteRune('\n')
			} else {
				out.WriteRune(runes[i])
			}
			positions = append(positions, i+1)
		}
	}
	cursor := 0
	for _, fold := range folds {
		if fold.start < cursor {
			continue // an already folded ancestor hides this node
		}
		appendText(cursor, fold.start)
		positions[len(positions)-1] = fold.start
		// Keep a small visible placeholder while mapping its edges back to the
		// full original range. Editing across the placeholder replaces that node.
		out.WriteString(" … ")
		positions = append(positions, fold.start, fold.end, fold.end)
		kept = append(kept, fold)
		cursor = fold.end
	}
	appendText(cursor, len(runes))
	return jsonTextProjection{text: out.String(), viewToText: positions, folds: kept}
}

func jsonSourceLineCount(source string) int {
	lines := 1
	for i := 0; i < len(source); i++ {
		switch source[i] {
		case '\r':
			lines++
			if i+1 < len(source) && source[i+1] == '\n' {
				i++
			}
		case '\n':
			lines++
		}
	}
	return lines
}

func projectedOffset(projection jsonTextProjection, original int) int {
	if len(projection.viewToText) == 0 {
		return 0
	}
	original = max(0, original)
	return sort.Search(len(projection.viewToText), func(i int) bool {
		return projection.viewToText[i] >= original
	})
}

func originalOffset(projection jsonTextProjection, view int) int {
	if len(projection.viewToText) == 0 {
		return 0
	}
	return projection.viewToText[bounded(view, 0, len(projection.viewToText)-1)]
}

func projectionEdit(oldProjection jsonTextProjection, edited string) (start, end int, inserted string, prefix, oldEnd, newEnd int) {
	oldRunes, newRunes := []rune(oldProjection.text), []rune(edited)
	for prefix < len(oldRunes) && prefix < len(newRunes) && oldRunes[prefix] == newRunes[prefix] {
		prefix++
	}
	oldEnd, newEnd = len(oldRunes), len(newRunes)
	for oldEnd > prefix && newEnd > prefix && oldRunes[oldEnd-1] == newRunes[newEnd-1] {
		oldEnd--
		newEnd--
	}
	start = originalOffset(oldProjection, prefix)
	end = originalOffset(oldProjection, oldEnd)
	if end < start {
		end = start
	}
	inserted = string(newRunes[prefix:newEnd])
	return
}

func projectionSelectionAfterEdit(projection jsonTextProjection, prefix, oldEnd, newEnd, selectionStart, selectionEnd int) (int, int) {
	start, end := originalOffset(projection, prefix), originalOffset(projection, oldEnd)
	delta := (newEnd - prefix) - (end - start)
	mapCaret := func(offset int) int {
		switch {
		case offset <= prefix:
			return originalOffset(projection, offset)
		case offset >= newEnd:
			return originalOffset(projection, oldEnd+(offset-newEnd)) + delta
		default:
			return originalOffset(projection, prefix) + (offset - prefix)
		}
	}
	return mapCaret(selectionStart), mapCaret(selectionEnd)
}

func replaceRuneRange(source string, start, end int, replacement string) string {
	runes := []rune(source)
	start = bounded(start, 0, len(runes))
	end = bounded(end, start, len(runes))
	return string(runes[:start]) + replacement + string(runes[end:])
}

func jsonEditorMatches(source, query string) []jsonEditorMatch {
	queryRunes := []rune(strings.ToLower(query))
	if len(queryRunes) == 0 {
		return nil
	}
	runes := []rune(source)
	lower := make([]rune, len(runes))
	for i, r := range runes {
		lower[i] = unicode.ToLower(r)
	}
	matches := make([]jsonEditorMatch, 0)
	for i := 0; i+len(queryRunes) <= len(lower); {
		matched := true
		for j, r := range queryRunes {
			if lower[i+j] != unicode.ToLower(r) {
				matched = false
				break
			}
		}
		if matched {
			matches = append(matches, jsonEditorMatch{start: i, end: i + len(queryRunes)})
			i += len(queryRunes)
		} else {
			i++
		}
	}
	return matches
}

func jsonEditorIconButton(c *ui.Context, key, name, glyph string, p palette, enabled, active bool) bool {
	b := ui.ButtonBase(c.Key(key)).Width(28).Height(28).Center().Radius(6).Label(name).Tooltip(name).Disabled(!enabled)
	fg, bg := p.muted, p.soft
	if enabled {
		fg = p.text
		if active {
			fg, bg = p.blue, p.selected
		} else if b.Hovered() {
			bg = p.surface
		}
	}
	b.Background(bg).Children(func() {
		icon(c, glyph, 16, fg)
	})
	return b.Clicked()
}
