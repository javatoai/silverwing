package markdown

import (
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/egoist/mygo/internal/platform"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown/parser"
)

// EditorOptions configure the native, source-preserving Markdown editor.
type EditorOptions struct {
	Options              *Options
	Label                string
	ReadOnly             bool
	OnChange             func()
	OnCompositionChanged func(bool)
	OnSave               func()
	OnError              func(error)
	OnPaste              func(plain bool) bool
	// InPlaceTables and RenderedLinks keep structured content visible while
	// editing. Both use the root's existing input client and source Buffer.
	InPlaceTables bool
	RenderedLinks bool
	// InPlaceCode keeps ordinary code blocks highlighted while editing their
	// contents through this Editor's single input client and Buffer. Language
	// renderers in Options.Code retain their existing activation behavior.
	InPlaceCode bool
	// ContinueBlocks enables source-preserving list and quote writing aids in
	// live mode. SourceMode and the zero-value SDK options retain raw keys.
	ContinueBlocks bool
	// InteractiveTasks toggles parser-confirmed task markers on an ordinary
	// click through the same root client and source Buffer. Default is off.
	InteractiveTasks bool
	// SmartEditing enables optional source-preserving writing commands.
	SmartEditing bool
	// ConfirmCodeFenceOnEnter keeps newly typed opening fences literal until
	// Enter creates a complete code block. Existing documents parse normally.
	ConfirmCodeFenceOnEnter bool
	// SourceHighlights decorate normalized source byte ranges in SourceMode.
	// They are display-only and never own text, selection or history.
	SourceHighlights []SourceHighlight
}

type SourceHighlight struct {
	Start, End int
	Current    bool
}

// EditorState owns one selection and input-method session for an entire
// Markdown document. Its Buffer is the only owner of text and undo history.
// Public selection offsets are UTF-8 bytes of Buffer's LF-normalized text.
// Keep the state on the UI thread and reuse it across frames and display modes.
type EditorState struct {
	// SourceMode reveals the whole source through the same input client.
	SourceMode                                      bool
	buffer                                          *editor.Buffer
	snapshot                                        editor.Snapshot
	source                                          string
	anchor, caret                                   int
	affinity                                        ui.TextAffinity
	opts                                            EditorOptions
	view                                            *viewState
	client                                          editorInputClient
	services                                        ui.Services
	root                                            ui.Element
	rect                                            ui.Rect
	scroll                                          ui.ScrollState
	runs                                            []*editorRun
	lineMaps                                        map[int][]int
	paragraphMaps                                   map[int]*editorParagraphMap
	blocks                                          []*editorBlock
	viewport                                        editorViewportAnchor
	pendingView                                     *pendingViewRestore
	metadataViewportVersion                         uint64
	interactionEpoch                                uint64
	activeStart, activeEnd                          int
	frozenStart, frozenEnd                          int
	compose                                         string
	virtualCache                                    editorVirtualText
	composeStart, composeEnd                        int
	composeSelected                                 ui.TextInputRange
	composing                                       bool
	pendingIME                                      bool
	pendingIMERange                                 ui.TextInputRange
	pendingIMEActualRange                           ui.TextInputRange
	pendingIMEVersion                               uint64
	dragging                                        bool
	linkPress                                       bool
	linkPressX, linkPressY                          float32
	pressedLink, clickedLink                        *editorLinkClick
	pressedTask                                     *editorTaskPress
	tableCells                                      []*editorTableHit
	omittedTableCell                                *editorTableHit
	codeGaps                                        []*editorCodeGap
	codeGapFocus                                    editorCodeGapFocus
	fenceDrafts, parsedFenceDrafts                  []int
	formatPending                                   bool
	pendingFormats                                  [4]bool
	autoPairs                                       []autoPair
	selectionGeometry                               ui.Rect
	selectionGeometryValid                          bool
	selectionGeometryVersion                        uint64
	selectionGeometryAnchor, selectionGeometryCaret int
	contextTable                                    *editorTableContext
	dragAnchorStart, dragAnchorEnd                  int
	focused, focusRequest, reveal, changed          bool
	revealBootstrap                                 bool
	blink                                           time.Time
	desiredX                                        float32
	hasDesired                                      bool
}

type editorSelection struct{ Anchor, Caret int }

// NewEditorState attaches a live editor to an existing source buffer.
func NewEditorState(buffer *editor.Buffer) *EditorState {
	if buffer == nil {
		panic("markdown: NewEditorState requires a Buffer")
	}
	s := &EditorState{buffer: buffer, view: newState(), activeStart: -1, activeEnd: -1}
	s.client.state = s
	s.snapshot = buffer.Snapshot()
	s.source = s.snapshot.String()
	return s
}

func (s *EditorState) Buffer() *editor.Buffer         { return s.buffer }
func (s *EditorState) Selection() (anchor, caret int) { s.sync(); return s.anchor, s.caret }
func (s *EditorState) Composing() bool                { return s.composing }

// SetSelection also reveals the caret on the next frame.
func (s *EditorState) SetSelection(anchor, caret int) {
	s.interactionEpoch++
	if !s.EndComposition() {
		return
	}
	s.sync()
	s.pendingView = nil
	s.formatPending = false
	s.codeGapFocus.valid = false
	s.omittedTableCell = nil
	s.anchor, s.caret = byteBoundary(s.source, anchor), byteBoundary(s.source, caret)
	if s.anchor == s.caret {
		s.ensureCodeTail(s.caret)
	}
	s.contextTable = nil
	s.affinity = ui.TextDownstream
	s.buffer.EndUndoGroup()
	s.reveal, s.hasDesired = true, false
	s.invalidate()
}

func (s *EditorState) Focus() { s.focusRequest = true; s.invalidate() }

// ReplaceRange applies a version-checked, independently undoable source edit.
// Offsets are UTF-8 bytes in the LF-normalized Buffer snapshot.
func (s *EditorState) ReplaceRange(start, end int, text string, version uint64) error {
	if s.opts.ReadOnly {
		return errors.New("Markdown is read-only")
	}
	if s.composing {
		return errors.New("Markdown input composition is active")
	}
	s.sync()
	if s.snapshot.Version() != version {
		return errors.New("Markdown source changed before the edit could be applied")
	}
	if start < 0 || end < start || end > len(s.source) || byteBoundary(s.source, start) != start || byteBoundary(s.source, end) != end {
		return errors.New("invalid Markdown source range")
	}
	s.buffer.EndUndoGroup()
	ok := s.replaceSource(start, end, text, editor.EditOther, 0, false)
	s.buffer.EndUndoGroup()
	if !ok {
		return errors.New("Markdown edit could not be applied")
	}
	return nil
}

// EndComposition commits pending input as one source transaction. A rejected
// commit leaves the Buffer unchanged and reports the error through OnError.
func (s *EditorState) EndComposition() bool {
	return s.endComposition(0, true)
}

func (s *EditorState) endComposition(trailingBytes int, closeGroup bool) bool {
	if !s.composing {
		if closeGroup {
			s.closePendingIME()
		}
		return true
	}
	marked, _ := s.client.MarkedRange()
	a, z, text := s.composeStart, s.composeEnd, s.compose
	prefixOnly := a == z && a == len(s.source) && a == s.sourcePrefixEnd() && a > 0
	ok := s.replaceWithTrailing(a, z, text, editor.EditComposition, trailingBytes)
	if !ok {
		return false
	}
	s.clearComposition()
	if closeGroup {
		s.buffer.EndUndoGroup()
		s.pendingIME = false
	} else {
		// Some native IMEs unmark first, then commit through the previously
		// marked UTF-16 range. Keep that replacement in this undo step.
		s.pendingIME = true
		s.pendingIMERange = marked
		s.pendingIMEActualRange = marked
		if prefixOnly {
			// Materializing a first body line can move the committed text.
			// Match the platform's old range, but replace its actual new range.
			shift := editorUnits(s.source[:s.sourcePrefixEnd()]) - marked.Start
			s.pendingIMEActualRange.Start += shift
			s.pendingIMEActualRange.End += shift
		}
		s.pendingIMEVersion = s.snapshot.Version()
	}
	return ok
}

func (s *EditorState) closePendingIME() {
	if s.pendingIME {
		s.buffer.EndUndoGroup()
		s.pendingIME = false
	}
}

// Editor builds one continuous native edit surface. Rendered blocks are
// display elements, never independent input fields or selection containers.
func Editor(c *ui.Context, s *EditorState, options ...EditorOptions) ui.Element {
	if s == nil {
		panic("markdown: Editor requires an EditorState")
	}
	s.opts = EditorOptions{}
	if len(options) > 0 {
		s.opts = options[0]
	}
	s.services = c.Services()
	s.sync()
	if s.SourceMode {
		s.formatPending = false
	}
	o := s.opts.Options
	if o == nil {
		o = &defaultOptions
	}
	v := s.view
	v.o, v.editor = o, s
	ext := o.Extensions
	if ext == 0 {
		ext = parser.Default
	}
	if v.doc == nil || v.ext != ext {
		v.doc, v.ext = parser.New(ext), ext
		v.forget()
	}
	if v.doc.SetWithPolicy(s.source, s.sourcePrefixEnd(), s.literalFenceLines()) {
		v.forget()
	}
	s.parsedFenceDrafts = append(s.parsedFenceDrafts[:0], s.literalFenceLines()...)
	v.theme(c)
	v.numberNotes()
	th := &v.th
	// Input/programmatic scrolling is visible before layout. Layout may then
	// clamp Y when an asynchronously resized resource shortens the document.
	scrollBeforeLayout := s.scroll.Y
	root := ui.Scroll(c).Focusable().FocusRing(false).Clip().TrackScroll(&s.scroll)
	s.root = root
	label := s.opts.Label
	if label == "" {
		label = "Markdown"
	}
	root.Role(ui.RoleTextField).Label(label).Access(s.access).Cursor(ui.CursorText)
	root.Font(th.Font).FontSize(th.FontSize).LineHeight(th.LineHeight).TextColor(th.Text)
	root.HandleInput(s.input).ContextMenu(func(menu *ui.Menu) {
		s.menu(menu)
		s.tableMenu(menu)
		if o.ContextMenu != nil {
			o.ContextMenu(c, menu)
		}
	})
	if !s.opts.ReadOnly {
		root.HandleTextInput(&s.client)
	}
	root.Draw(func(_ *ui.Painter, r ui.Rect) { s.rect = r })
	root.DrawOver(func(_ *ui.Painter, _ ui.Rect) {
		if s.applyViewRestore(scrollBeforeLayout) {
			s.cacheSelectionBounds()
			return
		}
		correction := s.stabilizeViewport(scrollBeforeLayout)
		before := s.scroll.Y
		s.revealCaret()
		s.rememberViewport(correction + s.scroll.Y - before)
		s.cacheSelectionBounds()
	})
	if s.focusRequest {
		root.Focus()
		s.focusRequest = false
	}
	focused := root.Focused()
	if focused != s.focused {
		s.focused = focused
		s.blink = c.Now()
		if !focused {
			s.dragging = false
		}
	}
	if s.changed {
		root.SetChanged()
		s.changed = false
	}
	s.runs = nil
	s.lineMaps = nil
	s.paragraphMaps = nil
	s.blocks = nil
	s.tableCells = nil
	s.codeGaps = nil
	// A chosen context-menu action runs while this root is built. Let the
	// host prepare its source projections again before traversing new blocks.
	s.sync()
	if v.doc.Source() != s.source {
		s.clickedLink = nil
		s.revealAfterProjectionChange()
		s.invalidate()
		return root
	}
	s.chooseActive()
	root.Children(func() {
		ui.Column(c).AlignSelf(ui.Stretch).Padding(12).Children(func() { s.buildBlocks(c) })
	})
	if !s.projectionCurrent() {
		// A LinkView action can change source during the traversal. Discard
		// the partial geometry and rebuild from the new snapshot next frame.
		s.runs, s.blocks, s.tableCells = nil, nil, nil
		s.codeGaps = nil
		s.lineMaps, s.paragraphMaps = nil, nil
		s.revealAfterProjectionChange()
		s.invalidate()
	} else {
		s.shapePendingReveal(scrollBeforeLayout)
	}
	// A host-owned LinkView must consume a click in the frame in which its
	// label is built. Never replay it against a later document or mode.
	s.clickedLink = nil
	if focused && !s.opts.ReadOnly {
		const blink = 530 * time.Millisecond
		if phase := c.Now().Sub(s.blink); phase < 30*time.Second {
			c.After(blink - phase%blink)
		}
	}
	return root
}

// A metadata edit rebuilds the projection without moving the reading position
// to a caret that the user may have deliberately scrolled away from.
func (s *EditorState) revealAfterProjectionChange() {
	s.reveal = s.metadataViewportVersion == 0 || s.metadataViewportVersion != s.buffer.Version()
}

func (s *EditorState) projectionCurrent() bool {
	return s.view.doc != nil && s.view.doc.Source() == s.source && s.snapshot.Version() == s.buffer.Version()
}

func (s *EditorState) sync() {
	if s.snapshot.Version() == s.buffer.Version() {
		return
	}
	changes, snap, ok := s.buffer.ChangesSince(s.snapshot.Version())
	s.closePendingIME()
	if s.composing {
		s.clearComposition()
		s.report(errors.New("Markdown source changed during composition"))
	}
	if ok {
		for _, batch := range changes {
			s.anchor = mapEditorOffset(s.anchor, batch)
			s.caret = mapEditorOffset(s.caret, batch)
			for i := range s.fenceDrafts {
				s.fenceDrafts[i] = mapEditorOffset(s.fenceDrafts[i], batch)
			}
			for i := range s.autoPairs {
				s.autoPairs[i].Open = mapEditorOffset(s.autoPairs[i].Open, batch)
				s.autoPairs[i].Close = mapEditorOffset(s.autoPairs[i].Close, batch)
			}
		}
	}
	s.snapshot, s.source = snap, snap.String()
	for i := range s.fenceDrafts {
		s.fenceDrafts[i] = sourceLineStart(s.source, s.fenceDrafts[i])
	}
	s.fenceDrafts = s.draftsAfterEdit(s.source, nil, s.fenceDrafts, -1)
	s.autoPairs = s.pairsAfterEdit(s.source, nil, s.autoPairs)
	s.anchor, s.caret = byteBoundary(s.source, s.anchor), byteBoundary(s.source, s.caret)
}

func mapEditorOffset(at int, changes []editor.Change) int {
	delta := 0
	for _, c := range changes {
		if at < c.OldStart {
			break
		}
		if at <= c.OldEnd {
			return c.NewEnd
		}
		delta = c.NewEnd - c.OldEnd
	}
	return at + delta
}

func byteBoundary(src string, at int) int {
	at = max(0, min(at, len(src)))
	for at > 0 && at < len(src) && !utf8.RuneStart(src[at]) {
		at--
	}
	return at
}

func normalizedText(text string) string {
	return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
}

func (s *EditorState) replace(a, z int, text string, kind editor.EditKind) bool {
	return s.replaceWithTrailing(a, z, text, kind, 0)
}

func (s *EditorState) replaceWithTrailing(a, z int, text string, kind editor.EditKind, trailingBytes int) bool {
	return s.replaceSource(a, z, text, kind, trailingBytes, true)
}

func (s *EditorState) replaceSource(a, z int, text string, kind editor.EditKind, trailingBytes int, tableInput bool) bool {
	s.sync()
	if s.opts.ReadOnly {
		return false
	}
	a, z = byteBoundary(s.source, min(a, z)), byteBoundary(s.source, max(a, z))
	leadingLine := ""
	if prefix := s.sourcePrefixEnd(); tableInput && prefix > 0 && s.anchor == s.caret {
		a, z = max(a, prefix), max(z, prefix)
		if a == z && a == prefix && a == len(s.source) && text != "" && s.source[a-1] != '\n' && s.source[a-1] != '\r' {
			leadingLine = s.buffer.LineEnding()
		}
	}
	endFormat := tableInput && s.opts.SmartEditing && !s.SourceMode && s.formatPending && strings.ContainsAny(text, "\r\n")
	if _, code := s.editingCode(a, z); code {
		endFormat = false
	}
	if tableInput && s.opts.SmartEditing && !s.SourceMode && !s.composing && a == z && s.formatPending && !s.editingInlineCodeBody(a, z) && strings.TrimSpace(text) == "" && text != "" {
		a = s.inlineClosingPosition(a)
		z = a
	}
	if tableInput && s.opts.SmartEditing && !s.SourceMode && !s.composing && a == z && s.formatPending && !endFormat && strings.TrimSpace(text) != "" {
		a = s.inlineTypingPosition(a)
		z = a
	}
	if tableInput && s.opts.SmartEditing && !s.SourceMode && !s.composing && a < z && trailingBytes == 0 && strings.TrimSpace(text) == "" {
		a, z, text = s.removeEmptyInlineEdit(a, z, text)
	}
	endpoint := byteBoundary(text, len(text)-trailingBytes)
	var newPair *autoPair
	if tableInput && trailingBytes == 0 {
		var skipped bool
		text, endpoint, newPair, skipped = s.pairInput(a, z, text, kind)
		if skipped {
			return true
		}
	}
	// Validate the paired payload that will actually be committed, after all
	// source-position adjustments and before any Buffer or history mutation.
	if tableInput && s.inlineCodeInputHasBlock(a, z, text) {
		s.report(errors.New("行内代码不能包含多个段落，请使用代码块"))
		return false
	}
	var inlineEdits []editor.Edit
	if tableInput {
		var code bool
		inlineEdits, code = s.inlineCodeInputEdits(a, z, text)
		if !code {
			text, endpoint, inlineEdits = s.formatInput(a, z, text, endpoint)
		}
	}
	_, wasCode := s.editingCode(a, z)
	newDraft := tableInput && !wasCode && text != "" && !strings.ContainsAny(text, "\r\n")
	gapBefore := s.focusedCodeGap() != nil
	var codeEdits []editor.Edit
	gapInput := false
	if tableInput {
		text, endpoint, gapInput = s.codeGapInput(a, z, text, endpoint)
	}
	if tableInput && !gapInput {
		a, z = s.protectLinkEdit(a, z)
		if a == z {
			a = s.codeVisiblePosition(a)
			z = a
		}
		text, endpoint = s.tableInputText(a, z, text, endpoint)
		text, endpoint, codeEdits = s.codeInputText(a, z, text, endpoint)
	}
	codeEdits = append(codeEdits, inlineEdits...)
	before := editorSelection{s.anchor, s.caret}
	var historyBefore any = before
	if gapBefore {
		historyBefore = editorCodeGapHistory{selection: before}
	} else if hit := s.focusedOmittedTableCell(); hit != nil {
		historyBefore = editorOmittedCellHistory{selection: before, start: hit.table.start, row: hit.cell.row, column: hit.cell.column}
	}
	// The caret belongs to the requested insertion, including any unchanged
	// prefix/suffix. Buffer changes describe the minimized diff, not that
	// insertion. Project its raw prefix separately so a retained IME suffix
	// cannot move the caret backward when it is absent from the diff.
	if leadingLine != "" {
		text = leadingLine + text
		endpoint += len(leadingLine)
		if newPair != nil {
			newPair.Open++
			newPair.Close++
		}
	}
	insertedPrefix := text[:endpoint]
	position := a + len(normalizedText(insertedPrefix))
	for _, edit := range codeEdits {
		if edit.End <= a {
			position += len(normalizedText(edit.Text)) - (edit.End - edit.Start)
		}
	}
	if strings.HasPrefix(insertedPrefix, "\n") && a > 0 && s.source[a-1] == '\n' && s.snapshot.OriginalSlice(a-1, a) == "\r" {
		// The existing bare CR and the inserted LF become one projected
		// newline. A CRLF already in the source ends in LF and cannot join.
		position--
	}
	after := editorSelection{position, position}
	if newPair != nil {
		newPair.Close = position
		newPair.Open = position - (z - a) - len(newPair.Opening)
		if a != z {
			after.Anchor = position - (z - a)
			if s.anchor > s.caret {
				after.Anchor, after.Caret = after.Caret, after.Anchor
			}
		}
	}
	// Stable ordering matters for inserting into an empty code card: the
	// insertion must precede a closer replacement at that same old offset.
	edits := append([]editor.Edit{{Start: a, End: z, Text: text}}, codeEdits...)
	var nextDrafts []int
	var nextPairs []autoPair
	result := s.buffer.ApplyTransaction(editor.Transaction{Version: s.snapshot.Version(),
		Edits: edits, Kind: kind, Owner: s,
		SelectionBefore: s.writingHistory(historyBefore, s.fenceDrafts),
		SelectionAfterEdit: func(snapshot editor.Snapshot, changes []editor.Change) any {
			if endFormat {
				s.formatPending = false
				s.pendingFormats = [4]bool{}
			}
			after.Anchor = min(after.Anchor, snapshot.Len())
			after.Caret = min(after.Caret, snapshot.Len())
			candidate := -1
			if newDraft {
				candidate = position
			}
			nextDrafts = s.draftsAfterEdit(snapshot.String(), changes, s.fenceDrafts, candidate)
			nextPairs = s.pairsAfterEdit(snapshot.String(), changes, s.autoPairs)
			if newPair != nil {
				nextPairs = append(nextPairs, *newPair)
			}
			if gapInput && strings.HasSuffix(normalizedText(insertedPrefix), "\n") {
				return s.writingHistory(editorCodeGapHistory{selection: after}, nextDrafts, nextPairs)
			}
			return s.writingHistory(after, nextDrafts, nextPairs)
		}})
	if !result.Applied {
		if result.Rejected {
			s.report(errors.New("Markdown edit exceeds the source size limit"))
		} else {
			s.report(errors.New("Markdown source changed before the edit could be applied"))
		}
		return false
	}
	oldVersion := s.snapshot.Version()
	s.snapshot = s.buffer.Snapshot()
	s.source = s.snapshot.String()
	s.fenceDrafts = nextDrafts
	s.autoPairs = nextPairs
	// Apply the same request-relative endpoint that was stored in history.
	s.anchor, s.caret = byteBoundary(s.source, after.Anchor), byteBoundary(s.source, after.Caret)
	if gapInput {
		s.restoreCodeGap(s.caret)
	}
	s.affinity = ui.TextDownstream
	s.reveal = true
	s.blink = time.Now()
	if s.snapshot.Version() != oldVersion {
		s.notifyChanged()
	}
	return true
}

func (s *EditorState) notifyChanged() {
	s.changed = true
	if s.opts.OnChange != nil {
		s.opts.OnChange()
	}
	s.invalidate()
}
func (s *EditorState) invalidate() { s.services.Invalidate() }
func (s *EditorState) report(err error) {
	if s.opts.OnError != nil {
		s.opts.OnError(err)
	}
}
func (s *EditorState) access(n *platform.AccessNode) {
	n.Value = s.virtualText()
	a, z := s.virtualSelection()
	n.SelStart = utf8.RuneCountInString(n.Value[:a])
	n.SelEnd = utf8.RuneCountInString(n.Value[:z])
	n.States |= platform.AccessMultiline
	if s.opts.ReadOnly {
		n.States |= platform.AccessReadOnly
	}
}

func (s *EditorState) clearComposition() {
	was := s.composing
	s.composing = false
	s.compose = ""
	s.composeSelected = ui.TextInputRange{}
	if was && s.opts.OnCompositionChanged != nil {
		s.opts.OnCompositionChanged(false)
	}
	s.invalidate()
}
