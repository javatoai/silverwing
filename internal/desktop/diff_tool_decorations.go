package desktop

import (
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"silverwing-go/internal/tools"
)

// Only CompareText decides what differs. This adapter projects that completed
// result onto the public native editor highlighter, gutter and folding APIs.
type diffEditableDecorations struct {
	left, right string
	options     tools.DiffOptions
	revision    uint64
	firstRow    *tools.DiffRow
	rowCount    int
	buffer      *editor.Buffer
	version     uint64
	language    *editor.Language
	marks       []editor.GutterMark
	folds       map[int]int
	collapse    bool
}

type diffEditableHighlighter struct {
	valid bool
	lines map[int][]editor.Token
	text  map[int]string
	folds map[int]int
}

func (h *diffEditableHighlighter) Edit(editor.Snapshot, editor.Snapshot, []editor.Change) {
	h.valid = false
}
func (h *diffEditableHighlighter) Highlight(int, time.Duration) bool { return true }
func (h *diffEditableHighlighter) Tokens(line int, text string, dst []editor.Token) ([]editor.Token, bool) {
	if h.valid && h.text[line] == text {
		dst = append(dst, h.lines[line]...)
	}
	return dst, true
}
func (h *diffEditableHighlighter) FoldEnd(line int) (int, bool) {
	end, ok := h.folds[line]
	return end, h.valid && ok
}

// CompareText and source-preserving MyGO buffers share CR, LF and CRLF logical
// lines. Decorations use the display's UTF-8 bytes without changing source.
type diffEditableLogicalLine struct {
	text                                   string
	nativeLine, nativeColumn, nativeOffset int
}

func diffEditableLogicalLines(source string) ([]diffEditableLogicalLine, string) {
	native := diffInputLF(source)
	var lines []diffEditableLogicalLine
	start, nativeLine, lineStart := 0, 0, 0
	for index := 0; index < len(native); index++ {
		if native[index] != '\n' && native[index] != '\r' {
			continue
		}
		lines = append(lines, diffEditableLogicalLine{native[start:index], nativeLine, start - lineStart, start})
		if native[index] == '\n' {
			nativeLine++
			lineStart = index + 1
		}
		start = index + 1
	}
	lines = append(lines, diffEditableLogicalLine{native[start:], nativeLine, start - lineStart, start})
	return lines, native
}

func (state *toolPageState) syncDiffEditableDecorations(p palette) {
	ready := state.diffHasData && state.diffOutputRevision == state.diffRevision
	var first *tools.DiffRow
	if len(state.diffResult.Rows) != 0 {
		first = &state.diffResult.Rows[0]
	}
	for side := range state.diffInputHistory {
		h := &state.diffInputHistory[side]
		if !ready {
			h.clearDiffDecorations()
			continue
		}
		d := h.decorations
		if d == nil || d.left != state.diffLeft || d.right != state.diffRight || d.options != state.diffOptions || d.revision != state.diffOutputRevision || d.firstRow != first || d.rowCount != len(state.diffResult.Rows) || d.buffer != h.view.Buffer || d.version != h.view.Buffer.Version() {
			h.clearDiffDecorations()
			d = newDiffEditableDecorations(state, side, h.view.Buffer)
			h.decorations = d
			h.view.Buffer.SetLanguage(d.language)
			// A fresh result applies the requested fold state once. Native
			// gutter expansion then remains available between comparisons.
			d.collapse = !state.diffCollapseEqual
		}
		color := p.danger
		if side == 1 {
			color = p.green
		}
		h.view.GutterMarks = make([]editor.GutterMark, len(d.marks))
		for index, mark := range d.marks {
			mark.Color = color
			h.view.GutterMarks[index] = mark
		}
		if d.collapse != state.diffCollapseEqual {
			for _, line := range h.view.Folded() {
				h.view.Unfold(line)
			}
			if state.diffCollapseEqual {
				starts := make([]int, 0, len(d.folds))
				for start := range d.folds {
					starts = append(starts, start)
				}
				sort.Ints(starts)
				for _, start := range starts {
					h.view.Fold(start)
				}
			}
			d.collapse = state.diffCollapseEqual
		}
	}
}

func (h *diffInputHistory) clearDiffDecorations() {
	if h.decorations == nil {
		return
	}
	for _, line := range h.view.Folded() {
		h.view.Unfold(line)
	}
	h.view.GutterMarks = nil
	if h.view.Buffer != nil {
		h.view.Buffer.SetLanguage(nil)
	}
	h.decorations = nil
}

func newDiffEditableDecorations(state *toolPageState, side int, buffer *editor.Buffer) *diffEditableDecorations {
	source := state.diffLeft
	if side == 1 {
		source = state.diffRight
	}
	logical, native := diffEditableLogicalLines(source)
	nativeLines := strings.Split(native, "\n")
	ranges := make(map[int][][2]int)
	marked := make(map[int]bool)
	lineFor := func(row tools.DiffRow) *tools.DiffLine {
		if side == 0 {
			return row.Left
		}
		return row.Right
	}
	for _, row := range state.diffResult.Rows {
		line := lineFor(row)
		if line == nil || row.Kind == "EQUAL" || line.Number <= 0 || line.Number > len(logical) {
			continue
		}
		origin := logical[line.Number-1]
		if origin.text != line.Text {
			continue
		}
		marked[origin.nativeLine] = true
		for _, span := range line.ChangedSpans {
			start := origin.nativeColumn + ui.UTF16ByteOffset(origin.text, span.Start)
			end := origin.nativeColumn + ui.UTF16ByteOffset(origin.text, span.EndExclusive)
			if end > start {
				ranges[origin.nativeLine] = append(ranges[origin.nativeLine], [2]int{start, end})
			}
		}
	}
	lines := make(map[int][]editor.Token, len(ranges))
	texts := make(map[int]string, len(ranges))
	for line, spans := range ranges {
		sort.Slice(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })
		cursor := 0
		var tokens []editor.Token
		for _, span := range spans {
			start, end := max(cursor, span[0]), min(len(nativeLines[line]), span[1])
			if end <= start {
				continue
			}
			if start > cursor {
				tokens = append(tokens, editor.Token{End: start, Kind: editor.TokenText})
			}
			tokens = append(tokens, editor.Token{End: end, Kind: editor.TokenStrong})
			cursor = end
		}
		if cursor < len(nativeLines[line]) {
			tokens = append(tokens, editor.Token{End: len(nativeLines[line]), Kind: editor.TokenText})
		}
		lines[line], texts[line] = tokens, nativeLines[line]
	}
	folds := make(map[int]int)
	context := max(0, state.diffOptions.ContextLines)
	for start := 0; start < len(state.diffResult.Rows); {
		if state.diffResult.Rows[start].Kind != "EQUAL" {
			start++
			continue
		}
		end := start + 1
		for end < len(state.diffResult.Rows) && state.diffResult.Rows[end].Kind == "EQUAL" {
			end++
		}
		if end-start > context*2+1 {
			first, last := lineFor(state.diffResult.Rows[start]), lineFor(state.diffResult.Rows[end-1])
			if first != nil && last != nil && first.Number > 0 && last.Number <= len(logical) {
				a, b := logical[first.Number-1], logical[last.Number-1]
				foldStart, foldEnd := a.nativeLine+max(0, context-1), b.nativeLine-context
				if a.nativeColumn == 0 && a.text == nativeLines[a.nativeLine] && b.text == nativeLines[b.nativeLine] && foldEnd > foldStart {
					folds[foldStart] = foldEnd
				}
			}
		}
		start = end
	}
	d := &diffEditableDecorations{left: state.diffLeft, right: state.diffRight, options: state.diffOptions, revision: state.diffOutputRevision, rowCount: len(state.diffResult.Rows), buffer: buffer, version: buffer.Version(), folds: folds}
	if len(state.diffResult.Rows) != 0 {
		d.firstRow = &state.diffResult.Rows[0]
	}
	for line := range marked {
		d.marks = append(d.marks, editor.GutterMark{Line: line})
	}
	sort.Slice(d.marks, func(i, j int) bool { return d.marks[i].Line < d.marks[j].Line })
	d.language = &editor.Language{Name: "Compared text", Highlighter: func(snapshot editor.Snapshot, _ func()) editor.Highlighter {
		return &diffEditableHighlighter{valid: snapshot.Version() == d.version && snapshot.String() == native, lines: lines, text: texts, folds: folds}
	}}
	return d
}

func (state *toolPageState) revealDiffEditableHunk(hunk tools.DiffHunk) {
	for side := range state.diffInputHistory {
		source := state.diffLeft
		if side == 1 {
			source = state.diffRight
		}
		logical, _ := diffEditableLogicalLines(source)
		for index := hunk.StartRow; index < hunk.EndRowExclusive && index < len(state.diffResult.Rows); index++ {
			row := state.diffResult.Rows[index]
			line := row.Left
			if side == 1 {
				line = row.Right
			}
			if line == nil || line.Number <= 0 || line.Number > len(logical) {
				continue
			}
			origin := logical[line.Number-1]
			state.diffInputHistory[side].view.GoTo(editor.Pos{Line: origin.nativeLine, Col: origin.nativeColumn})
			break
		}
	}
	state.diffScrollPending = false
}
