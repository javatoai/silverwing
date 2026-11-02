package markdown

import (
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func (s *EditorState) input(ev ui.InputEvent) bool {
	s.sync()
	if ev.Kind == ui.InputPointerDown || ev.Kind == ui.InputKeyDown || ev.Kind == ui.InputText || ev.Kind == ui.InputCommand || ev.Kind == ui.InputScroll {
		s.pendingView = nil
		s.interactionEpoch++
	}
	if s.taskPointer(ev) {
		return true
	}
	switch ev.Kind {
	case ui.InputText:
		if s.opts.ReadOnly {
			return false
		}
		s.client.ReplaceText(nil, ev.Text)
		return true
	case ui.InputKeyDown:
		s.contextTable = nil
		return s.key(ev.Key, ev.Mods)
	case ui.InputCommand:
		return s.command(ev.Text)
	case ui.InputPointerDown:
		if ev.Button == 1 {
			if s.EndComposition() {
				s.contextTable = s.tableContextAt(ev.X, ev.Y)
			}
			return false // the root's native context menu opens after this hit
		}
		if ev.Button != 0 {
			return false
		}
		s.contextTable, s.clickedLink = nil, nil
		if !s.EndComposition() {
			return true
		}
		s.omittedTableCell = nil
		if s.opts.RenderedLinks && !s.SourceMode && ev.Mods&ui.Shift == 0 {
			if in, ok := s.linkAt(ev.X, ev.Y); ok {
				s.linkPress = true
				s.linkPressX, s.linkPressY = ev.X, ev.Y
				s.pressedLink = &editorLinkClick{start: in.Start, end: in.End, version: s.snapshot.Version()}
				s.dragging = false
				s.focusRequest = true
				return true // keep the move and release on the single input root
			}
		}
		s.linkPress = false
		s.codeGapFocus.valid = false
		if gap := s.codeGapAt(ev.X, ev.Y); gap != nil && !s.opts.ReadOnly {
			s.focusCodeGap(gap)
		}
		start, end, aff := s.positionEdges(ev.X, ev.Y)
		var omittedTarget *editorTableHit
		if hit := s.tableCellAt(ev.X, ev.Y); hit != nil && hit.cell.omitted && !s.opts.ReadOnly && ev.Mods&ui.Shift == 0 {
			if cell, ok := s.enterTableCell(hit.table, hit.cell); ok {
				start, end, aff = cell.start, cell.start, ui.TextDownstream
				omittedTarget = hit
			} else {
				return true
			}
		}
		at := start
		if aff == ui.TextUpstream {
			at = end
		}
		if ev.Mods&ui.Shift != 0 {
			s.dragAnchorStart, s.dragAnchorEnd = s.anchor, s.anchor
		} else {
			s.dragAnchorStart, s.dragAnchorEnd = start, end
		}
		s.moveTo(at, aff, ev.Mods&ui.Shift != 0)
		if omittedTarget != nil {
			s.omittedTableCell = omittedTarget
		}
		if ev.Clicks >= 3 {
			a, z := s.lineEdges(s.caret)
			s.anchor, s.caret = a, z
		} else if ev.Clicks == 2 {
			s.anchor, s.caret = s.wordEdges(s.caret)
		}
		s.dragging = true
		s.frozenStart, s.frozenEnd = s.activeStart, s.activeEnd
		s.focusRequest = true
		s.hasDesired = false
		return true
	case ui.InputPointerMove:
		if s.linkPress && !s.dragging && (ev.X-s.linkPressX)*(ev.X-s.linkPressX)+(ev.Y-s.linkPressY)*(ev.Y-s.linkPressY) > 16 {
			s.beginLinkDrag()
		}
		if !s.dragging {
			return false
		}
		s.dragTo(ev.X, ev.Y)
		if ev.Y < 0 {
			s.scroll.Y = max(0, s.scroll.Y+ev.Y)
		} else if ev.Y > s.rect.H {
			s.scroll.Y = min(s.scroll.MaxY, s.scroll.Y+ev.Y-s.rect.H)
		}
		return true
	case ui.InputPointerUp:
		if ev.Button != 0 {
			return false
		}
		if s.linkPress {
			if !s.dragging && (ev.X-s.linkPressX)*(ev.X-s.linkPressX)+(ev.Y-s.linkPressY)*(ev.Y-s.linkPressY) > 16 {
				s.beginLinkDrag()
			}
			if !s.dragging && s.pressedLink != nil && s.pressedLink.version == s.snapshot.Version() {
				if in, ok := s.linkAt(ev.X, ev.Y); ok && in.Start == s.pressedLink.start && in.End == s.pressedLink.end {
					s.clickedLink = s.pressedLink
					s.clickedLink.mods = ev.Mods
					s.invalidate()
				}
			}
			s.linkPress, s.pressedLink = false, nil
			if s.dragging {
				s.dragTo(ev.X, ev.Y)
			}
			s.dragging = false
			return true
		}
		if !s.dragging {
			return false
		}
		s.dragging = false
		return true
	case ui.InputSelect:
		s.SetSelection(editorRuneByte(s.source, ev.Start), editorRuneByte(s.source, ev.End))
		return true
	}
	return false
}

func (s *EditorState) dragTo(x, y float32) {
	start, end, affinity := s.positionEdges(x, y)
	switch {
	case start == s.dragAnchorStart && end == s.dragAnchorEnd:
		s.anchor = s.dragAnchorStart
		s.moveTo(s.dragAnchorStart, affinity, true)
	case start >= s.dragAnchorStart:
		s.anchor = s.dragAnchorStart
		s.moveTo(end, affinity, true)
	default:
		s.anchor = s.dragAnchorEnd
		s.moveTo(start, affinity, true)
	}
}

func editorRuneByte(text string, n int) int {
	for i := range text {
		if n <= 0 {
			return i
		}
		n--
	}
	return len(text)
}
func (s *EditorState) moveTo(at int, aff ui.TextAffinity, extend bool) {
	s.formatPending = false
	s.closePendingIME()
	s.omittedTableCell = nil
	if !extend {
		at = s.codeVisiblePosition(at)
		at = max(at, s.sourcePrefixEnd())
	}
	s.caret = byteBoundary(s.source, at)
	s.affinity = aff
	if !extend {
		s.anchor = s.caret
	}
	s.buffer.EndUndoGroup()
	s.reveal = true
	s.blink = time.Now()
}
func (s *EditorState) key(key ui.Key, mods ui.Modifiers) bool {
	if prefix := s.sourcePrefixEnd(); prefix > 0 && s.anchor == s.caret {
		if s.caret < prefix {
			s.anchor, s.caret = prefix, prefix
		}
		if key == ui.KeyBackspace && s.caret == prefix {
			return true
		}
	}
	if s.opts.SmartEditing && !s.SourceMode && (mods == ui.Cmd && (key == ui.KeyB || key == ui.KeyI) || mods == ui.Cmd|ui.Shift && key == ui.KeyX) {
		kind := CommandBold
		if key == ui.KeyI {
			kind = CommandItalic
		} else if key == ui.KeyX {
			kind = CommandStrike
		}
		s.report(s.Execute(EditorCommand{Kind: kind}))
		return true
	}
	shift := mods&ui.Shift != 0
	m := mods &^ ui.Shift
	if m == ui.Cmd {
		switch key {
		case ui.KeyA:
			return s.command("selectAll")
		case ui.KeyC:
			return s.command("copy")
		case ui.KeyX:
			return s.command("cut")
		case ui.KeyV:
			if shift {
				return s.command("pasteAndMatchStyle")
			}
			return s.command("paste")
		case ui.KeyZ:
			if shift {
				return s.command("redo")
			}
			return s.command("undo")
		case ui.KeyY:
			return s.command("redo")
		case ui.KeyS:
			if !s.EndComposition() {
				return true
			}
			if s.opts.OnSave != nil {
				s.opts.OnSave()
			}
			return true
		}
	}
	if s.composing {
		return false
	} // native input methods own composition keys
	if s.pairKey(key, mods) {
		return true
	}
	if s.blockWritingKey(key, mods) {
		return true
	}
	if s.codeWritingKey(key, mods) {
		return true
	}
	if s.codeGapKey(key, mods) {
		return true
	}
	if s.tableKey(key, mods) {
		return true
	}
	if s.writingKey(key, mods) {
		return true
	}
	a, z := min(s.anchor, s.caret), max(s.anchor, s.caret)
	switch key {
	case ui.KeyLeft, ui.KeyRight:
		dir := 1
		if key == ui.KeyLeft {
			dir = -1
		}
		at, aff := s.caret, s.affinity
		switch {
		case m == ui.Ctrl || m == ui.Alt:
			at = s.wordBoundary(at, dir)
		case m != 0:
			return false
		case a != z && !shift:
			if dir < 0 {
				at = a
			} else {
				at = z
			}
		default:
			at, aff = s.horizontal(dir, shift)
		}
		s.moveTo(at, aff, shift)
		s.hasDesired = false
		return true
	case ui.KeyUp, ui.KeyDown, ui.KeyPageUp, ui.KeyPageDown:
		if m != 0 {
			return false
		}
		dir := 1
		if key == ui.KeyUp || key == ui.KeyPageUp {
			dir = -1
		}
		box, ok := s.caretBounds(s.caret)
		if !ok {
			return true
		}
		if !s.hasDesired {
			s.desiredX = box.X
			s.hasDesired = true
		}
		distance := box.H
		if key == ui.KeyPageUp || key == ui.KeyPageDown {
			distance = max(box.H, s.rect.H-box.H)
		}
		y := box.Y + box.H/2 + float32(dir)*distance
		start, end, aff := s.positionEdges(s.desiredX, y)
		at := s.visualSelectionEdge(start, end, aff, shift)
		s.moveTo(at, aff, shift)
		return true
	case ui.KeyHome, ui.KeyEnd:
		a, z := s.lineEdges(s.caret)
		if m == ui.Ctrl || m == ui.Super {
			a, z = 0, len(s.source)
		} else if m != 0 {
			return false
		}
		if key == ui.KeyHome {
			s.moveTo(a, ui.TextDownstream, shift)
		} else {
			s.moveTo(z, ui.TextUpstream, shift)
		}
		s.hasDesired = false
		return true
	case ui.KeyBackspace, ui.KeyDelete:
		if s.opts.ReadOnly {
			return true
		}
		if m != 0 && m != ui.Ctrl && m != ui.Alt {
			return false
		}
		if a == z {
			dir := 1
			if key == ui.KeyBackspace {
				dir = -1
			}
			if start, end, ok := s.codeDeleteRange(dir, m != 0); ok {
				a, z = start, end
			} else if start, end, ok := s.linkDeleteRange(dir, m != 0); ok {
				a, z = start, end
			} else {
				at := s.grapheme(s.caret, dir)
				if m != 0 {
					at = s.wordBoundary(s.caret, dir)
				}
				a, z = min(at, s.caret), max(at, s.caret)
			}
		}
		s.replace(a, z, "", editor.EditDeleting)
		return true
	case ui.KeyEnter:
		if m != 0 || s.opts.ReadOnly {
			return false
		}
		s.finishInlineTyping()
		a, z = min(s.anchor, s.caret), max(s.anchor, s.caret)
		s.buffer.EndUndoGroup()
		s.replace(a, z, s.buffer.LineEnding(), editor.EditOther)
		s.buffer.EndUndoGroup()
		return true
	case ui.KeyTab:
		if m != 0 || shift || s.opts.ReadOnly {
			return false
		}
		if s.opts.ContinueBlocks && !s.SourceMode && !s.writingLiteralRange(s.writingDocument(), a, z) {
			return false // ordinary live text leaves Tab available for focus navigation
		}
		s.replace(a, z, "\t", editor.EditOther)
		return true
	}
	return false
}

func (s *EditorState) command(name string) bool {
	if name != "copy" && name != "selectAll" {
		if !s.EndComposition() {
			return true
		}
	}
	a, z := min(s.anchor, s.caret), max(s.anchor, s.caret)
	switch name {
	case "selectAll":
		s.SetSelection(0, len(s.source))
		return true
	case "copy":
		if a != z {
			if text, ok := s.tableCopy(a, z); ok {
				s.services.WriteClipboard(text)
				return true
			}
			s.services.WriteClipboard(s.snapshot.OriginalSlice(a, z))
		}
		return true
	case "cut":
		if !s.opts.ReadOnly && a != z {
			s.services.WriteClipboard(s.snapshot.OriginalSlice(a, z))
			s.replace(a, z, "", editor.EditOther)
		}
		return true
	case "paste", "pasteAndMatchStyle":
		if !s.opts.ReadOnly {
			if s.opts.OnPaste != nil && s.opts.OnPaste(name == "pasteAndMatchStyle") {
				return true
			}
			if s.tablePaste(s.services.ReadClipboard()) {
				return true
			}
			s.buffer.EndUndoGroup()
			s.replace(a, z, s.services.ReadClipboard(), editor.EditOther)
			s.buffer.EndUndoGroup()
		}
		return true
	case "delete":
		if !s.opts.ReadOnly {
			s.replace(a, z, "", editor.EditDeleting)
		}
		return true
	case "undo", "redo":
		if s.opts.ReadOnly {
			return true
		}
		s.buffer.EndUndoGroup()
		var result editor.HistoryResult
		if name == "undo" {
			result = s.buffer.UndoTransaction()
		} else {
			result = s.buffer.RedoTransaction()
		}
		if result.Applied {
			s.sync()
			preserveReading := false
			if result.Owner == s {
				selection, gap := result.Selection, false
				if saved, ok := selection.(writingHistory); ok {
					s.fenceDrafts = append(s.fenceDrafts[:0], saved.drafts...)
					s.autoPairs = append(s.autoPairs[:0], saved.pairs...)
					s.formatPending, s.pendingFormats = saved.formatPending, saved.formats
					selection = saved.selection
				}
				if saved, ok := selection.(editorTaskHistory); ok {
					selection, preserveReading = saved.selection, true
				}
				var omitted *editorOmittedCellHistory
				if saved, ok := selection.(editorCodeGapHistory); ok {
					selection, gap = saved.selection, true
				} else if saved, ok := selection.(editorOmittedCellHistory); ok {
					selection, omitted = saved.selection, &saved
				}
				if sel, ok := selection.(editorSelection); ok {
					s.anchor, s.caret = byteBoundary(s.source, sel.Anchor), byteBoundary(s.source, sel.Caret)
					s.codeGapFocus.valid = false
					s.omittedTableCell = nil
					if gap {
						s.restoreCodeGap(s.caret)
					} else if omitted != nil {
						s.restoreOmittedTableCell(*omitted)
					}
				}
			}
			s.reveal = !preserveReading
			s.notifyChanged()
		}
		return true
	}
	return false
}

func (s *EditorState) menu(menu *ui.Menu) {
	menu.EditItems()
}

func (s *EditorState) horizontal(dir int, extend bool) (int, ui.TextAffinity) {
	if at, affinity, ok := s.codeHorizontal(dir); ok {
		return at, affinity
	}
	if at, affinity, ok := s.linkHorizontal(dir, extend); ok {
		return at, affinity
	}
	r := s.caretRun(s.caret)
	if r != nil {
		u := r.unitForSource(s.caret)
		next := r.layout.MoveCaret(ui.TextCaretPosition{Index: u, Affinity: s.affinity}, dir)
		start, end := r.sourceEdgesForUnit(next.Index)
		at := s.visualSelectionEdge(start, end, next.Affinity, extend)
		if at != s.caret {
			return at, next.Affinity
		}
	}
	return s.grapheme(s.caret, dir), ui.TextDownstream
}

func (s *EditorState) visualSelectionEdge(start, end int, affinity ui.TextAffinity, extend bool) int {
	if !extend {
		if affinity == ui.TextUpstream {
			return end
		}
		return start
	}
	anchorStart, anchorEnd := s.anchor, s.anchor
	if r := s.caretRun(s.anchor); r != nil {
		anchorStart, anchorEnd = r.sourceEdgesForUnit(r.unitForSource(s.anchor))
	}
	switch {
	case start == anchorStart && end == anchorEnd:
		s.anchor = anchorStart
		return anchorStart
	case start < anchorStart:
		s.anchor = anchorEnd
		return start
	default:
		s.anchor = anchorStart
		return end
	}
}
func (s *EditorState) grapheme(at, dir int) int {
	// A paragraph-local layout gives Unicode extended grapheme boundaries,
	// including emoji ZWJ sequences, without reshaping the whole document.
	a := strings.LastIndexByte(s.source[:at], '\n') + 1
	z := len(s.source)
	if n := strings.IndexByte(s.source[at:], '\n'); n >= 0 {
		z = at + n
	}
	if dir < 0 && at == a {
		return max(0, at-1)
	}
	if dir > 0 && at == z {
		return min(len(s.source), at+1)
	}
	line := s.source[a:z]
	layout := ui.ShapeText(line, ui.Font{Size: 14}, 0)
	u := editorUnits(s.source[a:at])
	if dir < 0 {
		u = layout.PreviousBoundary(u)
	} else {
		u = layout.NextBoundary(u)
	}
	return a + ui.UTF16ByteOffset(line, u)
}
func (s *EditorState) lineEdges(at int) (int, int) {
	if r := s.caretRun(at); r != nil {
		caret := r.layout.CaretAt(ui.TextCaretPosition{Index: r.unitForSource(at), Affinity: s.affinity})
		y := caret.Y + caret.H/2
		a := r.sourceForUnit(r.layout.PositionAt(ui.Point{X: -1e6, Y: y}).Index)
		_, z := r.sourceEdgesForUnit(r.layout.PositionAt(ui.Point{X: 1e6, Y: y}).Index)
		return min(a, z), max(a, z)
	}
	a := strings.LastIndexByte(s.source[:at], '\n') + 1
	z := len(s.source)
	if n := strings.IndexByte(s.source[at:], '\n'); n >= 0 {
		z = at + n
	}
	return a, z
}
func editorWord(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }
func (s *EditorState) wordBoundary(at, dir int) int {
	return editorWordBoundary(s.source, at, dir)
}

func editorWordBoundary(text string, at, dir int) int {
	seen := false
	for at > 0 && dir < 0 || at < len(text) && dir > 0 {
		var r rune
		var n int
		if dir < 0 {
			r, n = utf8.DecodeLastRuneInString(text[:at])
		} else {
			r, n = utf8.DecodeRuneInString(text[at:])
		}
		if !editorWord(r) && seen {
			break
		}
		seen = seen || editorWord(r)
		at += dir * n
	}
	return at
}
func (s *EditorState) wordEdges(at int) (int, int) {
	if at == len(s.source) && at > 0 {
		_, n := utf8.DecodeLastRuneInString(s.source)
		at -= n
	}
	if at == len(s.source) {
		return at, at
	}
	r, _ := utf8.DecodeRuneInString(s.source[at:])
	word := editorWord(r)
	a, z := at, at
	for a > 0 {
		r, n := utf8.DecodeLastRuneInString(s.source[:a])
		if r == '\n' || editorWord(r) != word {
			break
		}
		a -= n
	}
	for z < len(s.source) {
		r, n := utf8.DecodeRuneInString(s.source[z:])
		if r == '\n' || editorWord(r) != word {
			break
		}
		z += n
	}
	return a, z
}
