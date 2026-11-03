package markdown

import (
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown/parser"
)

type writingHistory struct {
	selection     any
	drafts        []int
	pairs         []autoPair
	formatPending bool
	formats       [4]bool
}

func (s *EditorState) writingHistory(selection any, drafts []int, pairs ...[]autoPair) any {
	if !s.opts.ConfirmCodeFenceOnEnter && !s.opts.SmartEditing && len(drafts) == 0 {
		return selection
	}
	saved := s.autoPairs
	if len(pairs) > 0 {
		saved = pairs[0]
	}
	return writingHistory{selection: selection, drafts: slices.Clone(drafts), pairs: slices.Clone(saved), formatPending: s.formatPending, formats: s.pendingFormats}
}

// ApplyEdits applies explicit source edits as one independently undoable action.
// Ranges and the resulting selection use normalized UTF-8 byte positions.
// Unlike native text input, structural edits do not escape table/code content.
func (s *EditorState) ApplyEdits(edits []editor.Edit, anchor, caret int, version uint64) error {
	return s.applySourceCommand(edits, anchor, caret, version, -1)
}

// InsertMarkdown inserts already-encoded Markdown at a captured prose/cell
// range. It materializes virtual writing rows and preserves table boundaries;
// raw source replacement remains available through ApplyEdits/ReplaceRange.
func (s *EditorState) InsertMarkdown(start, end int, text string, version uint64) error {
	s.sync()
	if s.opts.ReadOnly || s.composing || version != s.snapshot.Version() {
		return errors.New("Markdown insertion is no longer available")
	}
	if start < 0 || end < start || end > len(s.source) || byteBoundary(s.source, start) != start || byteBoundary(s.source, end) != end {
		return errors.New("invalid Markdown insertion range")
	}
	point := s.CaptureInsertionPoint()
	if start != min(s.anchor, s.caret) || end != max(s.anchor, s.caret) {
		point.selection = editorSelection{start, end}
		point.gap.valid = false
		point.omitted = nil
	}
	return s.InsertMarkdownAt(point, text)
}

func (s *EditorState) applySourceCommand(edits []editor.Edit, anchor, caret int, version uint64, confirmLine int, selectionIntent ...editorCodeGapHistory) error {
	s.sync()
	if s.opts.ReadOnly {
		return errors.New("Markdown is read-only")
	}
	if s.composing {
		return errors.New("Markdown input composition is active")
	}
	if version != s.snapshot.Version() {
		return errors.New("Markdown source changed before the edit could be applied")
	}
	for _, edit := range edits {
		if edit.Start < 0 || edit.End < edit.Start || edit.End > len(s.source) || byteBoundary(s.source, edit.Start) != edit.Start || byteBoundary(s.source, edit.End) != edit.End {
			return errors.New("invalid Markdown source range")
		}
	}
	ordered := slices.Clone(edits)
	slices.SortStableFunc(ordered, func(a, b editor.Edit) int { return a.Start - b.Start })
	previousStart, previousEnd := -1, -1
	for _, edit := range ordered {
		if edit.Start < previousEnd && (edit.End > edit.Start || edit.Start > previousStart) {
			return errors.New("overlapping Markdown source ranges")
		}
		if edit.End > previousEnd {
			previousStart, previousEnd = edit.Start, edit.End
		}
	}
	before := any(editorSelection{s.anchor, s.caret})
	if s.focusedCodeGap() != nil {
		before = s.captureCodeGapHistory(editorSelection{s.anchor, s.caret})
	}
	if hit := s.focusedOmittedTableCell(); hit != nil {
		before = editorOmittedCellHistory{selection: editorSelection{s.anchor, s.caret}, start: hit.table.start, row: hit.cell.row, column: hit.cell.column}
	}
	drafts := slices.Clone(s.fenceDrafts)
	if confirmLine >= 0 {
		drafts = slices.DeleteFunc(drafts, func(at int) bool { return at == confirmLine })
	}
	var nextDrafts []int
	var nextPairs []autoPair
	after := editorSelection{anchor, caret}
	afterGap := false
	s.buffer.EndUndoGroup()
	result := s.buffer.ApplyTransaction(editor.Transaction{Version: version, Edits: edits, Kind: editor.EditOther, Owner: s,
		SelectionBefore: s.writingHistory(before, s.fenceDrafts),
		SelectionAfterEdit: func(snapshot editor.Snapshot, changes []editor.Change) any {
			after.Anchor, after.Caret = byteBoundary(snapshot.String(), anchor), byteBoundary(snapshot.String(), caret)
			nextDrafts = s.draftsAfterEdit(snapshot.String(), changes, drafts, -1)
			nextPairs = s.pairsAfterEdit(snapshot.String(), changes, s.autoPairs)
			if len(selectionIntent) > 0 {
				intent := selectionIntent[0]
				after = editorSelection{byteBoundary(snapshot.String(), intent.selection.Anchor), byteBoundary(snapshot.String(), intent.selection.Caret)}
				intent.selection = after
				afterGap = true
				return s.writingHistory(intent, nextDrafts, nextPairs)
			}
			return s.writingHistory(after, nextDrafts, nextPairs)
		}})
	s.buffer.EndUndoGroup()
	if !result.Applied {
		if result.Rejected {
			return errors.New("Markdown edit exceeds the source size limit")
		}
		return errors.New("Markdown edit could not be applied")
	}
	s.snapshot = s.buffer.Snapshot()
	s.source = s.snapshot.String()
	s.fenceDrafts = nextDrafts
	s.autoPairs = nextPairs
	s.anchor, s.caret = after.Anchor, after.Caret
	s.codeGapFocus.valid = false
	s.omittedTableCell = nil
	if afterGap {
		s.restoreCodeGapHistory(selectionIntent[0])
	}
	s.affinity = ui.TextDownstream
	s.reveal, s.hasDesired = true, false
	s.blink = time.Now()
	s.notifyChanged()
	return nil
}

func sourceLineStart(source string, at int) int {
	return strings.LastIndexByte(source[:max(0, min(at, len(source)))], '\n') + 1
}

func (s *EditorState) documentPrefixEnd(start int) int {
	if start == 0 && strings.HasPrefix(s.source, "\ufeff") {
		return len("\ufeff")
	}
	return start
}

func draftMarker(source string, line int) (int, int, bool) {
	if line < 0 || line >= len(source) {
		return 0, 0, false
	}
	end := codeLineEnd(source, line)
	first := strings.IndexByte(source[line:end], '`')
	if first < 0 {
		return 0, 0, false
	}
	first += line
	last := first
	for last < end && source[last] == '`' {
		last++
	}
	return first, last, last-first >= 3 && !strings.ContainsRune(source[last:end], '`')
}

func (s *EditorState) draftsAfterEdit(source string, changes []editor.Change, drafts []int, candidate int) []int {
	var result []int
	for _, at := range drafts {
		at = sourceLineStart(source, mapEditorOffset(at, changes))
		if _, _, ok := draftMarker(source, at); ok {
			result = append(result, at)
		}
	}
	if candidate >= 0 && s.opts.ConfirmCodeFenceOnEnter && !s.SourceMode {
		line := sourceLineStart(source, candidate)
		if _, _, ok := draftMarker(source, line); ok && !slices.Contains(result, line) {
			d := parser.New(s.view.ext)
			d.SetWithPolicy(source, s.sourcePrefixFor(source), result)
			for i := 1; i < d.Len(); i++ {
				if syntax, ok := d.CodeSource(i); ok && sourceLineStart(source, syntax.OpeningStart) == line && source[syntax.OpeningStart] == '`' {
					result = append(result, line)
					break
				}
			}
		}
	}
	slices.Sort(result)
	return slices.Compact(result)
}

func (s *EditorState) literalFenceLines() []int {
	if !s.opts.ConfirmCodeFenceOnEnter || s.SourceMode || s.opts.ReadOnly {
		return nil
	}
	return s.fenceDrafts
}

func (s *EditorState) editingDocument() *parser.Document {
	if s.view.doc != nil && s.view.doc.Source() == s.source && slices.Equal(s.parsedFenceDrafts, s.literalFenceLines()) {
		return s.view.doc
	}
	d := parser.New(s.view.ext)
	d.SetWithPolicy(s.source, s.sourcePrefixEnd(), s.literalFenceLines())
	return d
}
