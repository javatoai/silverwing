package desktop

import (
	"sort"

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

func initializeJSONEditor(state *toolPageState) {
	state.jsonEditor = jsonToolState{
		sourceSnapshot: state.jsonSource,
		folded:         make(map[string]bool),
		history:        []jsonEditorSnapshot{{text: state.jsonSource}},
		historyReady:   true,
	}
	state.syncJSONProjection()
}

func (state *toolPageState) syncJSONEditor() {
	e := &state.jsonEditor
	if !e.historyReady {
		initializeJSONEditor(state)
		return
	}
	if e.sourceSnapshot != state.jsonSource {
		e.sourceSnapshot = state.jsonSource
		e.clearFolds()
		state.syncJSONProjection()
		e.record(state.jsonSource, e.lastSelection.selectionStart, e.lastSelection.selectionEnd)
		e.setPendingSelection(e.lastSelection.selectionStart, e.lastSelection.selectionEnd)
	}
	state.syncJSONProjection()
}

func (e *jsonToolState) clearFolds() {
	e.folded = make(map[string]bool)
	e.foldRevision++
}

func (state *toolPageState) syncJSONProjection() {
	e := &state.jsonEditor
	if e.sourceIndex == nil || e.sourceIndex.source != state.jsonSource {
		if e.nativeIndex != nil && e.nativeIndex.source == state.jsonSource {
			e.sourceIndex = e.nativeIndex
		} else {
			e.sourceIndex = newJSONTextIndex(state.jsonSource)
		}
	}
	key := jsonProjectionKey{source: state.jsonSource, parseRevision: state.jsonParseRevision, foldRevision: e.foldRevision, valid: state.jsonValid}
	if e.projectionReady && e.projectionKey == key {
		e.viewSource = e.projection.text
		return
	}
	nodes := []tools.JSONSourceNode(nil)
	if state.jsonValid {
		nodes = state.jsonDoc.Nodes
	}
	e.projection = makeIndexedJSONTextProjection(e.sourceIndex, nodes, e.folded)
	e.projectionKey, e.projectionReady = key, true
	e.projectionRevision++
	e.viewSource = e.projection.text
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
	e.selectionStart = bounded(start, 0, e.projection.index.runeCount())
	e.selectionEnd = bounded(end, 0, e.projection.index.runeCount())
	e.pendingSelection = true
	e.pendingFocus = true
}

func (e *jsonToolState) setPendingSelectionWithoutFocus(start, end int) {
	e.selectionStart = bounded(start, 0, e.projection.index.runeCount())
	e.selectionEnd = bounded(end, 0, e.projection.index.runeCount())
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
	e.clearFolds() // Source expands folds when the text changes.
	state.syncJSONProjection()
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
	e.clearFolds()
	state.syncJSONProjection()
	selectionStart = bounded(selectionStart, 0, e.sourceIndex.runeCount())
	selectionEnd = bounded(selectionEnd, 0, e.sourceIndex.runeCount())
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
	e.clearFolds()
	state.syncJSONProjection()
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

func jsonFoldNodesByVisibleLine(state *toolPageState) map[int]tools.JSONSourceNode {
	state.syncJSONProjection()
	e := &state.jsonEditor
	if e.foldCache != nil && e.foldCacheProjection == e.projectionRevision {
		return e.foldCache
	}
	result := make(map[int]tools.JSONSourceNode)
	if !state.jsonValid {
		e.foldCache, e.foldCacheProjection = result, e.projectionRevision
		return result
	}
	for _, node := range state.jsonDoc.Nodes {
		if !eligibleJSONFold(node) {
			continue
		}
		start := e.sourceIndex.runeAtUTF16(node.StartOffset)
		end := e.sourceIndex.runeAtUTF16(node.EndOffset)
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
		visibleLine := e.projection.index.lineAtRune(viewStart)
		if existing, ok := result[visibleLine]; ok {
			existingStart := e.sourceIndex.runeAtUTF16(existing.StartOffset)
			existingEnd := e.sourceIndex.runeAtUTF16(existing.EndOffset)
			if existingEnd-existingStart <= end-start {
				continue
			}
		}
		result[visibleLine] = node
	}
	e.foldCache, e.foldCacheProjection = result, e.projectionRevision
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
	e.foldRevision++
	state.syncJSONProjection()
	e.setPendingSourceSelection(e.lastSelection.selectionStart, e.lastSelection.selectionEnd)
}

func (s *app) foldJSONAtCaret(state *toolPageState) {
	state.syncJSONProjection()
	caret := state.jsonEditor.lastSelection.selectionStart
	var chosen tools.JSONSourceNode
	chosenSpan := int(^uint(0) >> 1)
	for _, node := range state.jsonDoc.Nodes {
		if !eligibleJSONFold(node) {
			continue
		}
		start := state.jsonEditor.sourceIndex.runeAtUTF16(node.StartOffset)
		end := state.jsonEditor.sourceIndex.runeAtUTF16(node.EndOffset)
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
	state.jsonEditor.clearFolds()
	state.syncJSONProjection()
	state.jsonEditor.setPendingSourceSelection(state.jsonEditor.lastSelection.selectionStart, state.jsonEditor.lastSelection.selectionEnd)
}

func revealJSONProjectionRange(state *toolPageState, start, end int) {
	state.syncJSONProjection()
	e := &state.jsonEditor
	changed := false
	for pointer := range e.folded {
		node, ok := jsonNodeByPointer(state.jsonDoc.Nodes, pointer)
		if !ok {
			delete(e.folded, pointer)
			changed = true
			continue
		}
		nodeStart := e.sourceIndex.runeAtUTF16(node.StartOffset)
		nodeEnd := e.sourceIndex.runeAtUTF16(node.EndOffset)
		if start < nodeEnd && end >= nodeStart {
			delete(e.folded, pointer)
			changed = true
		}
	}
	if changed {
		e.foldRevision++
		state.syncJSONProjection()
	}
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
