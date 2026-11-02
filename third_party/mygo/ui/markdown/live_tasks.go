package markdown

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown/parser"
)

type editorTaskPress struct {
	mark    int
	version uint64
	x, y    float32
}

// A checkbox is a reading action: its undo keeps the current viewport while
// still restoring the editor's source selection and any virtual-cell intent.
type editorTaskHistory struct{ selection any }

func (s *EditorState) taskAt(x, y float32) *editorTaskPress {
	if !s.opts.InteractiveTasks || s.SourceMode || s.opts.ReadOnly || s.composing || !s.projectionCurrent() {
		return nil
	}
	x, y = x+s.rect.X, y+s.rect.Y
	if !s.rect.Contains(x, y) {
		return nil
	}
	d := s.view.doc
	for i := 1; i < d.Len(); i++ {
		b := d.Block(i)
		if b.Kind != parser.Item || b.Task == parser.NoTask {
			continue
		}
		syntax, ok := d.ListItemSource(i)
		if !ok {
			continue
		}
		mark := syntax.MarkerEnd
		for mark < len(s.source) && (s.source[mark] == ' ' || s.source[mark] == '\t') {
			mark++
		}
		if mark+3 > len(s.source) || s.source[mark] != '[' || s.source[mark+2] != ']' || !strings.ContainsRune(" xX", rune(s.source[mark+1])) {
			continue
		}
		for _, run := range s.runs {
			if run.layout == nil || mark < run.caretStart || mark+3 > run.caretEnd {
				continue
			}
			for _, box := range run.layout.SelectionRects(ui.TextInputRange{Start: run.unitForSource(mark), End: run.unitForSource(mark + 3)}) {
				box.X += run.rect.X
				box.Y += run.rect.Y
				if box.Contains(x, y) {
					return &editorTaskPress{mark: mark, version: s.snapshot.Version()}
				}
			}
		}
	}
	return nil
}

func (s *EditorState) beginTaskDrag(press *editorTaskPress) {
	s.pressedTask = nil
	a, z, affinity := s.positionEdges(press.x, press.y)
	s.dragAnchorStart, s.dragAnchorEnd = a, z
	at := a
	if affinity == ui.TextUpstream {
		at = z
	}
	s.moveTo(at, affinity, false)
	s.dragging = true
	s.frozenStart, s.frozenEnd = s.activeStart, s.activeEnd
}

func taskDragged(press *editorTaskPress, ev ui.InputEvent) bool {
	dx, dy := ev.X-press.x, ev.Y-press.y
	return dx*dx+dy*dy > 16
}

func (s *EditorState) taskPointer(ev ui.InputEvent) bool {
	switch ev.Kind {
	case ui.InputKeyDown, ui.InputSelect:
		s.pressedTask = nil
	case ui.InputPointerDown:
		s.pressedTask = nil
		if ev.Button != 0 || ev.Mods != 0 || ev.Clicks > 1 {
			return false
		}
		if hit := s.taskAt(ev.X, ev.Y); hit != nil {
			hit.x, hit.y = ev.X, ev.Y
			s.pressedTask = hit
			s.contextTable, s.clickedLink, s.pressedLink = nil, nil, nil
			s.linkPress, s.dragging = false, false
			s.focusRequest = true
			return true
		}
	case ui.InputPointerMove:
		if press := s.pressedTask; press != nil {
			if !taskDragged(press, ev) {
				return true
			}
			s.beginTaskDrag(press) // the normal move path extends this selection
		}
	case ui.InputPointerUp:
		press := s.pressedTask
		if press == nil || ev.Button != 0 {
			return false
		}
		s.pressedTask = nil
		if taskDragged(press, ev) {
			s.beginTaskDrag(press)
			s.dragTo(ev.X, ev.Y)
			s.dragging = false
			return true
		}
		if ev.Mods == 0 && press.version == s.snapshot.Version() {
			if hit := s.taskAt(ev.X, ev.Y); hit != nil && hit.mark == press.mark {
				s.toggleTask(hit)
			}
		}
		return true
	}
	return false
}

func (s *EditorState) toggleTask(hit *editorTaskPress) {
	if !s.opts.InteractiveTasks || s.opts.ReadOnly || s.SourceMode || s.composing || hit.version != s.snapshot.Version() {
		return
	}
	at := hit.mark + 1
	if at >= len(s.source) || !strings.ContainsRune(" xX", rune(s.source[at])) {
		return
	}
	text := " "
	if s.source[at] == ' ' {
		text = "x"
	}
	before := editorSelection{s.anchor, s.caret}
	var history any = before
	gap := s.focusedCodeGap() != nil
	var omitted *editorOmittedCellHistory
	if gap {
		history = editorCodeGapHistory{selection: before}
	} else if cell := s.focusedOmittedTableCell(); cell != nil {
		saved := editorOmittedCellHistory{selection: before, start: cell.table.start, row: cell.cell.row, column: cell.cell.column}
		omitted, history = &saved, saved
	}
	s.buffer.EndUndoGroup()
	taskHistory := editorTaskHistory{selection: history}
	result := s.buffer.ApplyTransaction(editor.Transaction{Version: hit.version, Edits: []editor.Edit{{Start: at, End: at + 1, Text: text}}, Kind: editor.EditOther, Owner: s, SelectionBefore: taskHistory, SelectionAfter: taskHistory})
	s.buffer.EndUndoGroup()
	if !result.Applied {
		return
	}
	s.sync()
	s.anchor, s.caret = before.Anchor, before.Caret
	s.codeGapFocus.valid = false
	s.omittedTableCell = nil
	if gap {
		s.restoreCodeGap(s.caret)
	} else if omitted != nil {
		s.restoreOmittedTableCell(*omitted)
	}
	s.reveal, s.hasDesired = false, false // checking a task must not scroll to an old caret
	s.notifyChanged()
}
