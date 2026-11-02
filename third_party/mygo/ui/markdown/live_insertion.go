package markdown

import (
	"errors"
	"strings"

	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown/parser"
)

// InsertionPoint captures a version-bound selection, including the visible
// column or writing row when multiple positions share the same source offset.
// It is transient UI state and contains no document text.
type InsertionPoint struct {
	buffer     *editor.Buffer
	version    uint64
	selection  editorSelection
	gap        editorCodeGapFocus
	omitted    *editorOmittedCellHistory
	sourceMode bool
}

func (s *EditorState) CaptureInsertionPoint() InsertionPoint {
	s.sync()
	p := InsertionPoint{buffer: s.buffer, version: s.snapshot.Version(), selection: editorSelection{s.anchor, s.caret}, gap: s.codeGapFocus, sourceMode: s.SourceMode}
	if hit := s.focusedOmittedTableCell(); hit != nil {
		p.omitted = &editorOmittedCellHistory{selection: p.selection, start: hit.table.start, row: hit.cell.row, column: hit.cell.column}
	}
	return p
}

// RestoreInsertionPoint restores selection intent without focusing or scrolling.
// A point from another Buffer, source version or mode is never accepted.
func (s *EditorState) RestoreInsertionPoint(p InsertionPoint) error {
	s.sync()
	if p.buffer != s.buffer || p.version != s.snapshot.Version() || p.sourceMode != s.SourceMode || s.composing {
		return errors.New("Markdown insertion position is no longer available")
	}
	s.restoreInsertionPoint(p)
	s.pendingView = nil
	s.invalidate()
	return nil
}

func (s *EditorState) restoreInsertionPoint(p InsertionPoint) {
	s.anchor, s.caret = p.selection.Anchor, p.selection.Caret
	s.codeGapFocus = p.gap
	s.omittedTableCell = nil
	if p.omitted != nil {
		s.restoreOmittedTableCell(*p.omitted)
	}
}

// InsertMarkdownAt inserts an encoded fragment at an earlier captured position,
// retaining its selection direction and virtual cell/row intent in Undo.
func (s *EditorState) InsertMarkdownAt(p InsertionPoint, text string) error {
	return s.insertMarkdownAt(p, text, nil)
}

func (s *EditorState) insertMarkdownAt(p InsertionPoint, text string, validate func(editor.Edit) error) error {
	if s.opts.ReadOnly {
		return errors.New("Markdown is read-only")
	}
	before := s.CaptureInsertionPoint()
	if err := s.RestoreInsertionPoint(p); err != nil {
		return err
	}
	a, z := min(s.anchor, s.caret), max(s.anchor, s.caret)
	text, compatible := s.mergeInlineFragment(a, z, text)
	endpoint := len(text)
	text, endpoint, gap := s.codeGapInput(a, z, text, endpoint)
	if !gap && !s.SourceMode {
		if _, cell, ok := s.editingTable(); ok && a >= cell.sourceStart && z <= cell.sourceEnd {
			text, endpoint = s.tableInputText(a, z, text, endpoint)
		} else if s.blockFragment(text) {
			prefix, suffix := s.fragmentSeparators(a, z, text)
			text, endpoint = prefix+text+suffix, len(prefix)+endpoint
		}
	}
	if s.opts.SmartEditing && !s.SourceMode && (!compatible || !s.richReplacementSafe(a, z, text)) {
		s.restoreInsertionPoint(before)
		return errors.New("该选区的 Markdown 结构无法安全替换")
	}
	at := a + len(normalizedText(text[:endpoint]))
	edit := editor.Edit{Start: a, End: z, Text: text}
	if validate != nil {
		if err := validate(edit); err != nil {
			s.restoreInsertionPoint(before)
			return err
		}
	}
	err := s.ApplyEdits([]editor.Edit{edit}, at, at, p.version)
	if err != nil {
		s.restoreInsertionPoint(before)
	}
	return err
}

// A fully styled incoming fragment inherits an identical surrounding style.
// Remove only redundant whole-fragment wrappers; mixed ambiguous nesting is
// rejected rather than toggling the surrounding delimiter runs accidentally.
func (s *EditorState) mergeInlineFragment(a, z int, text string) (string, bool) {
	if !s.opts.SmartEditing || s.SourceMode {
		return text, true
	}
	var inherited [3]bool
	for i := range inherited {
		_, _, left, right, ok := s.inlineContainer(i, a, z)
		inherited[i] = ok && a >= left && z <= right
	}
	kinds := []parser.InlineKind{parser.Strong, parser.Emphasis, parser.Strikethrough}
	for pass := 0; pass < 3; pass++ {
		d := parser.New(s.view.ext)
		d.Set(text)
		stripped := false
		for b := 1; b < d.Len() && !stripped; b++ {
			for _, in := range d.Inlines(b) {
				for i, kind := range kinds {
					if inherited[i] && in.Kind == kind && in.Start == 0 && in.End == len(normalizedText(text)) {
						width := []int{2, 1, 2}[i]
						snapshot := editor.NewBufferPreservingLineEndings(text).Snapshot()
						text = snapshot.OriginalSlice(width, snapshot.Len()-width)
						stripped = true
						break
					}
				}
				if stripped {
					break
				}
			}
		}
		if !stripped {
			break
		}
	}
	d := parser.New(s.view.ext)
	d.Set(text)
	for b := 1; b < d.Len(); b++ {
		for _, in := range d.Inlines(b) {
			for i, kind := range kinds {
				if inherited[i] && in.Kind == kind {
					return text, false
				}
			}
		}
	}
	return text, true
}

func (s *EditorState) richReplacementSafe(a, z int, text string) bool {
	d := s.editingDocument()
	block := s.blockFragment(text)
	for b := 1; b < d.Len(); b++ {
		for _, in := range d.Inlines(b) {
			switch in.Kind {
			case parser.Strong, parser.Emphasis, parser.Strikethrough, parser.Code, parser.Link, parser.Image:
			default:
				continue
			}
			if in.Start < a && in.End > a && in.End <= z || in.Start >= a && in.Start < z && in.End > z {
				return false
			}
			if in.Start < a && in.End > z && (block || in.Kind == parser.Code) {
				return false
			}
		}
	}
	return true
}

func (s *EditorState) blockFragment(text string) bool {
	d := parser.New(s.view.ext)
	d.Set(text)
	count := 0
	for i := 1; i < d.Len(); i = d.Block(i).Next {
		count++
		if d.Block(i).Kind != parser.Paragraph {
			return true
		}
	}
	return count > 1
}

func (s *EditorState) fragmentSeparators(a, z int, text string) (prefix, suffix string) {
	if a > s.documentPrefixEnd(0) {
		n := 0
		for at := a - 1; at >= 0 && s.source[at] == '\n' && n < 2; at-- {
			n++
		}
		n += len(normalizedText(text)) - len(strings.TrimLeft(normalizedText(text), "\n"))
		prefix = strings.Repeat(s.buffer.LineEnding(), max(0, 2-n))
	}
	if z < len(s.source) {
		n := 0
		for at := z; at < len(s.source) && s.source[at] == '\n' && n < 2; at++ {
			n++
		}
		normalized := normalizedText(text)
		n += len(normalized) - len(strings.TrimRight(normalized, "\n"))
		suffix = strings.Repeat(s.buffer.LineEnding(), max(0, 2-n))
	}
	return
}
