package desktop

import (
	"errors"
	"fmt"

	"silverwing-go/internal/editor"

	"github.com/egoist/mygo/ui"
)

// Each source group owns a persistent native text client. Document owns source
// ranges and undo; this state owns presentation and the uncommitted IME draft.
type markdownRichFieldState struct {
	partID        uint64
	source        string
	model         *editor.RichBlock
	input         *nativeRichTextField
	selection     ui.TextInputSelection
	synchronizing bool
	palette       palette
}

func (s *app) markdownRichField(state *markdownEditorState, part editor.Part, p palette) (*markdownRichFieldState, error) {
	if state.richFields == nil {
		state.richFields = make(map[uint64]*markdownRichFieldState)
	}
	rich := state.richFields[part.ID]
	if rich == nil {
		model, err := editor.NewRichBlock(part)
		if err != nil {
			return nil, err
		}
		rich = &markdownRichFieldState{partID: part.ID, source: part.Source, model: model, palette: p}
		rich.input = newNativeMarkdownRichTextField(model.Text(), markdownRichSpans(model, p))
		rich.selection = rich.input.getSelection()
		state.richFields[part.ID] = rich
		rich.input.onReplace = func(start, end int, value string) (string, []ui.Span, error) {
			if err := s.replaceMarkdownRichText(state, rich, start, end, value); err != nil {
				return "", nil, err
			}
			return rich.model.Text(), markdownRichSpans(rich.model, rich.palette), nil
		}
		rich.input.onReplacementCaret = func() int { return rich.model.ReplacementCaretUTF16() }
		rich.input.onPointerDown = func() {
			if s.markdownRichFieldCurrent(state, rich) && !state.doc.Composing() {
				state.doc.ClearSelection()
			}
		}
		rich.input.onSelection = func(start, end int) {
			if rich.synchronizing || !s.markdownRichFieldCurrent(state, rich) || rich.input.composing || rich.input.dirty {
				return
			}
			rich.selection = rich.input.getSelection()
			state.selectedStructuredRich = nil
			state.selectionPart = rich.partID
			text := rich.model.Text()
			state.doc.SetCursor(&editor.Cursor{Part: rich.partID, Start: ui.UTF16ByteOffset(text, start), End: ui.UTF16ByteOffset(text, end), Field: "rich"})
		}
		rich.input.onComposition = func(active bool) {
			if s.markdownRichFieldCurrent(state, rich) {
				s.observeMarkdownComposition(state, markdownRichFieldKey(rich.partID), active)
			}
		}
		rich.input.onError = func(err error) {
			if s.markdownRichFieldCurrent(state, rich) {
				s.lastError = err.Error()
			}
		}
	} else if rich.source != part.Source && (rich.input.composing || rich.input.dirty || rich.input.errorText != "") {
		model, err := editor.NewRichBlock(part)
		if err != nil {
			return nil, err
		}
		rich.synchronizing = true
		if rich.input.composing {
			rich.input.setContent(model.Text(), markdownRichSpans(model, p))
		} else {
			s.lastError = "文档内容已变化，当前未提交的富文本输入已保留。"
		}
		rich.synchronizing = false
	} else if rich.source != part.Source {
		model, err := editor.NewRichBlock(part)
		if err != nil {
			return nil, err
		}
		rich.model, rich.source = model, part.Source
	}
	rich.palette = p
	rich.synchronizing = true
	rich.input.setContent(rich.model.Text(), markdownRichSpans(rich.model, p))
	rich.synchronizing = false
	return rich, nil
}

func newNativeMarkdownRichTextField(text string, spans []ui.Span) *nativeRichTextField {
	field := &nativeRichTextField{font: ui.Font{Family: markdownSansFontFamily, Size: 15},
		lineHeight: 1.6, zoom: 1, layoutWidth: 520, layoutDirty: true}
	field.setContent(text, spans)
	end := ui.UTF16Len(text)
	field.setSelection(ui.TextInputSelection{Range: ui.TextInputRange{Start: end, End: end}})
	return field
}

func markdownRichFieldKey(id uint64) string { return fmt.Sprintf("rich-%d", id) }

func (s *app) markdownRichFieldCurrent(state *markdownEditorState, rich *markdownRichFieldState) bool {
	if state == nil || rich == nil || state.closed || state.readOnly || state.detached || state.doc == nil ||
		!s.markdownEditorIsCurrent(state) {
		return false
	}
	if s.markdownStructuredRichFieldCurrent(state, rich) {
		return true
	}
	if state.richFields[rich.partID] != rich {
		return false
	}
	for _, part := range state.doc.Parts() {
		if part.ID == rich.partID && part.Kind == editor.PartRich {
			return true
		}
	}
	return false
}

func (s *app) renderMarkdownRichEditor(c *ui.Context, p palette, state *markdownEditorState, part editor.Part) {
	rich, err := s.markdownRichField(state, part, p)
	if err != nil {
		label(c, err.Error(), 12, p.danger)
		return
	}
	key := markdownRichFieldKey(part.ID)
	s.observeMarkdownComposition(state, key, rich.input.composing)
	rich.input.setZoom(state.zoom)
	rich.input.onScroll = func(event ui.InputEvent) bool {
		return s.handleMarkdownEditorZoomInput(state, event)
	}
	element := renderNativeRichTextField(c, p, markdownEditorInputKey(state, key), rich.input)
	element = s.decorateMarkdownEditorFind(c, p, state, key, rich.model.Text(), element, rich)
	state.editFields[key] = markdownEditFieldRef{key: key, partID: part.ID, value: rich.model.Text(), element: element, row: -1, column: -1, rich: rich}
	rich.input.onCommand = func(c *ui.Context, event ui.InputEvent) bool {
		return s.handleMarkdownRichCommand(c, state, rich, key, event)
	}
	snapshot := state.doc.Snapshot()
	if snapshot.FocusRequest != state.lastFocusRequest && snapshot.Cursor != nil && snapshot.Cursor.Part == part.ID {
		text := rich.model.Text()
		position := editor.ByteToUTF16Offset(text, snapshot.Cursor.Start)
		end := editor.ByteToUTF16Offset(text, max(snapshot.Cursor.Start, snapshot.Cursor.End))
		rich.input.setSelection(ui.TextInputSelection{Range: ui.TextInputRange{Start: position, End: end}})
		element.Focus()
		state.lastFocusRequest = snapshot.FocusRequest
	}
	if rich.input.errorText != "" {
		label(c, rich.input.errorText, 12, p.danger).Wrap()
		if action(c, markdownEditorControlKey(state, key+"-discard-draft"), "undo", "丢弃未提交输入", p, false) {
			rich.input.discardDraft()
		}
	}
}

func (s *app) handleMarkdownEditorZoomInput(state *markdownEditorState, event ui.InputEvent) bool {
	if !s.materialsUnit.state.documentEditing || state != s.materialsUnit.state.markdown || !s.markdownEditorIsCurrent(state) || state.sourceMode {
		return false
	}
	return s.handleMarkdownZoomInput(event)
}

func (s *app) replaceMarkdownRichText(state *markdownEditorState, rich *markdownRichFieldState, start, end int, value string) error {
	if s.markdownRichFieldCurrent(state, rich) && state.doc.Snapshot().Selection != nil {
		if err := state.doc.ReplaceSelection(value); err != nil {
			return err
		}
		s.commitMarkdownEditorSource(state, state.doc.Source())
		// Document requests focus for the replacement group on the next frame.
		parts := state.doc.Parts()
		cursor := state.doc.Snapshot().Cursor
		for _, part := range parts {
			if cursor != nil && part.ID == cursor.Part && part.Kind == editor.PartRich {
				model, err := editor.NewRichBlock(part)
				if err != nil {
					return err
				}
				rich.model = model
				return nil
			}
		}
		return nil
	}
	return s.commitMarkdownRichMutation(state, rich, true, func(model *editor.RichBlock) error { return model.ReplaceUTF16(start, end, value) })
}

func (s *app) commitMarkdownRichMutation(state *markdownEditorState, rich *markdownRichFieldState, coalesce bool, mutation func(*editor.RichBlock) error) error {
	if handled, err := s.commitMarkdownStructuredRichMutation(state, rich, coalesce, mutation); handled {
		return err
	}
	if !s.markdownRichFieldCurrent(state, rich) {
		return errors.New("Markdown 编辑块已失效")
	}
	found := false
	for _, part := range state.doc.Parts() {
		if part.ID != rich.partID {
			continue
		}
		found = true
		if part.Source != rich.source {
			return errors.New("文档内容已变化，请保留当前输入并重新确认后再编辑")
		}
		break
	}
	if !found {
		return errors.New("Markdown 编辑块已移除")
	}
	before := rich.model.Clone()
	if err := mutation(rich.model); err != nil {
		rich.model = before
		return err
	}
	previousSelection := state.doc.Snapshot().Selection
	state.doc.ClearSelection()
	if err := state.doc.ReplacePart(rich.partID, rich.model.Source(), coalesce); err != nil {
		rich.model = before
		state.doc.SetSelection(previousSelection)
		return err
	}
	rich.source = rich.model.Source()
	if ref, ok := state.editFields[markdownRichFieldKey(rich.partID)]; ok {
		ref.value = rich.model.Text()
		state.editFields[markdownRichFieldKey(rich.partID)] = ref
	}
	s.commitMarkdownEditorSource(state, state.doc.Source())
	return nil
}

func (s *app) formatMarkdownRichSelection(state *markdownEditorState, rich *markdownRichFieldState, command editor.FormatCommand, level int, value string) {
	if state.doc.Composing() {
		s.lastError = editor.ErrComposing.Error()
		return
	}
	selection := rich.input.getSelection()
	if err := s.commitMarkdownRichMutation(state, rich, false, func(model *editor.RichBlock) error {
		return model.FormatUTF16(selection.Range.Start, selection.Range.End, command, level, value)
	}); err != nil {
		s.lastError = err.Error()
		return
	}
	s.installMarkdownRichModel(rich, selection)
}

func (s *app) installMarkdownRichModel(rich *markdownRichFieldState, selection ui.TextInputSelection) {
	rich.synchronizing = true
	rich.input.setContent(rich.model.Text(), markdownRichSpans(rich.model, rich.palette))
	rich.synchronizing = false
	rich.input.setSelection(selection)
}

func (s *app) pruneMarkdownRichFields(state *markdownEditorState) {
	s.pruneMarkdownStructuredRichFields(state)
	valid := make(map[uint64]bool)
	for _, part := range state.doc.Parts() {
		if part.Kind == editor.PartRich {
			valid[part.ID] = true
		}
	}
	for id, rich := range state.richFields {
		if !valid[id] {
			rich.input.cancelComposition()
			s.observeMarkdownComposition(state, markdownRichFieldKey(id), false)
			delete(state.richFields, id)
		}
	}
	if state.richLink != nil && !s.markdownRichFieldCurrent(state, state.richLink.rich) && !valid[state.richLink.partID] {
		state.richLink = nil
	}
}

func allMarkdownRichFields(state *markdownEditorState) []*markdownRichFieldState {
	if state == nil {
		return nil
	}
	fields := allMarkdownStructuredRichFields(state)
	for _, rich := range state.richFields {
		if rich != nil && rich.input != nil {
			fields = append(fields, rich)
		}
	}
	return fields
}

func markdownRichSpans(model *editor.RichBlock, p palette) []ui.Span {
	var result []ui.Span
	for _, run := range model.Spans() {
		span := ui.Span{Text: run.Text, Font: markdownSansFontFamily, Size: 15, Weight: 400, Color: p.text, Italic: run.Style.Italic, Strikethrough: run.Style.Strike}
		if run.Heading > 0 {
			span.Size = []float32{15, 28, 23, 20, 18, 16, 15}[min(run.Heading, 6)]
			span.Weight = 650
		}
		if run.Style.Bold {
			span.Weight = 700
		}
		if run.Style.Code {
			span.Font, span.Background = "Consolas, monospace", p.soft
		}
		if run.Style.Link != "" {
			span.Color, span.Underline = p.blue, true
		}
		if run.ListPrefix {
			span.Color = p.muted
		}
		result = append(result, span)
	}
	return result
}
