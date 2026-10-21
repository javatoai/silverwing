package desktop

import (
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

const (
	nativeRichTextInsetX    float32 = 12
	nativeRichTextInsetY    float32 = 9
	nativeRichTextMinHeight float32 = 42
)

// nativeRichTextField owns a block's editable display text, selection, and
// IME draft. The Markdown document remains the source of truth; committed
// changes leave this control through onReplace as one UTF-16 range patch.
type nativeRichTextField struct {
	handle     ui.Handle
	text       string
	spans      []ui.Span
	draftText  string
	draftSpans []ui.Span
	dirty      bool
	errorText  string

	selection            ui.TextInputSelection
	anchorAffinity       ui.TextAffinity
	caretAffinity        ui.TextAffinity
	marked               *ui.TextInputRange
	composing            bool
	compositionText      string
	compositionSpans     []ui.Span
	compositionSelection ui.TextInputSelection
	pendingText          string
	pendingSpans         []ui.Span
	hasPending           bool

	layout         *ui.TextLayout
	layoutWidth    float32
	layoutHeight   float32
	boxHeight      float32
	layoutDirty    bool
	pointerDown    bool
	preferredX     float32
	hasPreferredX  bool
	font           ui.Font
	lineHeight     float32
	zoom           float32
	wordNavigation *nativeRichWordNavigationState
	findHighlights []markdownEditorFindHighlight

	onReplace          func(start, end int, value string) (text string, spans []ui.Span, err error)
	onReplacementCaret func() int
	onPointerDown      func()
	onSelection        func(start, end int)
	onComposition      func(bool)
	onScroll           func(event ui.InputEvent) bool
	onCommand          func(c *ui.Context, event ui.InputEvent) bool
	onError            func(error)
}

func newNativeRichTextField(text string, spans []ui.Span) *nativeRichTextField {
	f := &nativeRichTextField{font: ui.Font{Size: 14}, zoom: 1, layoutWidth: 520, layoutDirty: true}
	f.setContent(text, spans)
	end := ui.UTF16Len(text)
	f.setSelection(ui.TextInputSelection{Range: ui.TextInputRange{Start: end, End: end}})
	f.ensureLayout()
	return f
}

func (f *nativeRichTextField) content() (string, []ui.Span) {
	if f == nil {
		return "", nil
	}
	return f.draftText, cloneNativeRichTextSpans(f.draftSpans)
}

func (f *nativeRichTextField) getSelection() ui.TextInputSelection {
	if f == nil {
		return ui.TextInputSelection{}
	}
	return f.selection
}

// setSelection accepts UTF-16 offsets, matching the native input API.
func (f *nativeRichTextField) setSelection(selection ui.TextInputSelection) {
	if f == nil {
		return
	}
	r := normalizeNativeRichTextRange(f.draftText, selection.Range)
	f.selection = ui.TextInputSelection{Range: r, Reversed: selection.Reversed}
	f.anchorAffinity, f.caretAffinity = ui.TextDownstream, ui.TextDownstream
	f.notifySelection()
}

// setContent keeps an active IME draft intact. A changed external value is
// deferred until composition is cancelled; a successful commit installs the
// callback's returned content instead.
func (f *nativeRichTextField) setContent(text string, spans []ui.Span) {
	if f == nil {
		return
	}
	nextSpans := normalizeNativeRichTextSpans(text, spans)
	if f.composing {
		if text != f.text || !equalNativeRichTextSpans(nextSpans, f.spans) {
			f.pendingText, f.pendingSpans, f.hasPending = text, nextSpans, true
		}
		return
	}
	if f.dirty && text == f.text && equalNativeRichTextSpans(nextSpans, f.spans) {
		return
	}
	oldText := f.draftText
	f.text, f.spans = text, cloneNativeRichTextSpans(nextSpans)
	f.draftText, f.draftSpans = text, cloneNativeRichTextSpans(nextSpans)
	f.dirty = false
	f.marked = nil
	f.hasPending = false
	if oldText != text {
		f.selection = ui.TextInputSelection{Range: ui.TextInputRange{Start: 0, End: 0}}
		f.anchorAffinity, f.caretAffinity = ui.TextDownstream, ui.TextDownstream
	} else {
		f.selection.Range = normalizeNativeRichTextRange(text, f.selection.Range)
	}
	f.setError(nil)
	f.layoutDirty = true
	f.ensureLayout()
	f.notifySelection()
}

func (f *nativeRichTextField) setError(err error) {
	if f == nil {
		return
	}
	if err == nil {
		f.errorText = ""
		return
	}
	f.errorText = err.Error()
	if f.onError != nil {
		f.onError(err)
	}
}

func (f *nativeRichTextField) TextForRange(r ui.TextInputRange) (string, ui.TextInputRange) {
	if f == nil {
		return "", ui.TextInputRange{}
	}
	r = normalizeNativeRichTextRange(f.draftText, r)
	start := ui.UTF16ByteOffset(f.draftText, r.Start)
	end := ui.UTF16ByteOffset(f.draftText, r.End)
	actual := ui.TextInputRange{Start: ui.UTF16Len(f.draftText[:start]), End: ui.UTF16Len(f.draftText[:end])}
	return f.draftText[start:end], actual
}

func (f *nativeRichTextField) Selection() ui.TextInputSelection { return f.getSelection() }

func (f *nativeRichTextField) MarkedRange() (ui.TextInputRange, bool) {
	if f == nil || f.marked == nil {
		return ui.TextInputRange{}, false
	}
	return *f.marked, true
}

func (f *nativeRichTextField) ReplaceText(r *ui.TextInputRange, value string) {
	if f == nil {
		return
	}
	explicitReplacement := !f.composing && !f.dirty
	target := f.targetRange(r)
	candidate, spans, caret, err := f.replaceDraft(target, value)
	if err != nil {
		f.setError(err)
		return
	}
	f.draftText, f.draftSpans = candidate, spans
	f.selection = ui.TextInputSelection{Range: ui.TextInputRange{Start: caret, End: caret}}
	f.anchorAffinity, f.caretAffinity = ui.TextDownstream, ui.TextDownstream
	wasComposing := f.composing
	if wasComposing {
		f.marked = &ui.TextInputRange{Start: target.Start, End: target.Start + ui.UTF16Len(value)}
	}
	f.dirty = candidate != f.text || !equalNativeRichTextSpans(spans, f.spans)
	f.layoutDirty = true
	f.ensureLayout()
	f.notifySelection()
	var committed bool
	if explicitReplacement {
		// A selected replacement owns its whole range, including an unchanged
		// prefix or suffix. Reducing it to a diff loses typing style and caret.
		committed = f.commitReplacement(target.Start, target.End, value, candidate != f.text, false)
	} else {
		committed = f.commitDraft(wasComposing)
	}
	if !committed && wasComposing {
		// A failed final commit stays visibly marked and can be retried.
		f.marked = &ui.TextInputRange{Start: target.Start, End: target.Start + ui.UTF16Len(value)}
		f.layoutDirty = true
		f.ensureLayout()
	}
}

func (f *nativeRichTextField) SetMarkedText(r *ui.TextInputRange, value string, selected ui.TextInputRange) {
	if f == nil {
		return
	}
	if !f.composing {
		f.compositionText = f.draftText
		f.compositionSpans = cloneNativeRichTextSpans(f.draftSpans)
		f.compositionSelection = f.selection
		f.composing = true
		if f.onComposition != nil {
			f.onComposition(true)
		}
	}
	target := f.targetRange(r)
	candidate, spans, _, err := f.replaceDraft(target, value)
	if err != nil {
		f.setError(err)
		return
	}
	start, end := target.Start, target.Start+ui.UTF16Len(value)
	selected = normalizeNativeRichTextRange(value, selected)
	f.draftText, f.draftSpans = candidate, spans
	f.marked = &ui.TextInputRange{Start: start, End: end}
	f.selection = ui.TextInputSelection{Range: ui.TextInputRange{Start: start + selected.Start, End: start + selected.End}}
	f.anchorAffinity, f.caretAffinity = ui.TextDownstream, ui.TextDownstream
	f.dirty = candidate != f.text || !equalNativeRichTextSpans(spans, f.spans)
	f.layoutDirty = true
	f.ensureLayout()
	f.notifySelection()
}

func (f *nativeRichTextField) UnmarkText() {
	if f == nil || !f.composing {
		return
	}
	f.commitDraft(true)
}

func (f *nativeRichTextField) BoundsForRange(r ui.TextInputRange) (ui.Rect, ui.TextInputRange, bool) {
	if f == nil || f.layout == nil {
		return ui.Rect{}, ui.TextInputRange{}, false
	}
	r = normalizeNativeRichTextRange(f.draftText, r)
	rects := f.layout.SelectionRects(r)
	var bounds ui.Rect
	if r.Start == r.End || len(rects) == 0 {
		bounds = f.layout.CaretAt(ui.TextCaretPosition{Index: r.End, Affinity: ui.TextDownstream})
	} else {
		bounds = rects[0]
		for _, rect := range rects[1:] {
			left, top := min(bounds.X, rect.X), min(bounds.Y, rect.Y)
			right, bottom := max(bounds.X+bounds.W, rect.X+rect.W), max(bounds.Y+bounds.H, rect.Y+rect.H)
			bounds = ui.Rect{X: left, Y: top, W: right - left, H: bottom - top}
		}
	}
	bounds.X += nativeRichTextInsetX
	bounds.Y += nativeRichTextInsetY
	return bounds, r, true
}

func (f *nativeRichTextField) IndexForPoint(point ui.Point) (int, bool) {
	if f == nil || f.layout == nil {
		return 0, false
	}
	return f.layout.IndexAt(ui.Point{X: point.X - nativeRichTextInsetX, Y: point.Y - nativeRichTextInsetY}), true
}

func renderNativeRichTextField(c *ui.Context, p palette, key string, field *nativeRichTextField) ui.Element {
	if field == nil {
		field = newNativeRichTextField("", nil)
	}
	if field.layoutWidth <= 0 {
		width, _ := c.Size()
		field.layoutWidth = max(width-48, float32(320))
	}
	field.ensureLayout()
	element := ui.Box(c.Key(key)).FillWidth().Height(max(field.boxHeight, nativeRichTextMinHeight)).
		Bind(&field.handle).
		Label("富文本编辑区").Role(ui.RoleTextField).Value(field.draftText).
		HandleTextInput(field).
		HandleInput(func(event ui.InputEvent) bool {
			if event.Kind == ui.InputScroll && field.onScroll != nil && field.onScroll(event) {
				return true
			}
			return field.handleInput(c, event)
		})
	focused, hovered := element.Focused(), element.Hovered()
	element.Draw(func(painter *ui.Painter, bounds ui.Rect) {
		contentWidth := max(bounds.W-2*nativeRichTextInsetX, float32(1))
		if field.setLayoutWidth(contentWidth) {
			c.Invalidate()
		}
		background, outline := p.surface, p.line
		if hovered {
			background = p.soft
		}
		if focused {
			outline = p.blue
		}
		if field.composing {
			outline = p.amber
		}
		if field.errorText != "" {
			outline = p.danger
		}
		painter.Fill(bounds, background, 7)
		painter.Stroke(bounds, outline, 7, 1)
		originX, originY := bounds.X+nativeRichTextInsetX, bounds.Y+nativeRichTextInsetY
		for _, selected := range field.layout.SelectionRects(field.selection.Range) {
			selected.X += originX
			selected.Y += originY
			painter.Fill(selected, p.selected, 2)
		}
		paintMarkdownEditorFindHighlights(painter, field, p, originX, originY)
		painter.TextLayout(field.layout, originX, originY, p.text)
		if focused && field.selection.Range.Start == field.selection.Range.End {
			caret := field.layout.CaretAt(field.caretPosition())
			caret.X += originX
			caret.Y += originY
			caret.W = 1.25
			painter.Fill(caret, p.blue, 0)
		}
	})
	return element
}

func (f *nativeRichTextField) targetRange(r *ui.TextInputRange) ui.TextInputRange {
	if r != nil {
		return normalizeNativeRichTextRange(f.draftText, *r)
	}
	if f.marked != nil {
		return normalizeNativeRichTextRange(f.draftText, *f.marked)
	}
	return normalizeNativeRichTextRange(f.draftText, f.selection.Range)
}

func (f *nativeRichTextField) replaceDraft(r ui.TextInputRange, value string) (string, []ui.Span, int, error) {
	if f == nil {
		return "", nil, 0, errors.New("富文本字段已关闭")
	}
	r = normalizeNativeRichTextRange(f.draftText, r)
	start, end := ui.UTF16ByteOffset(f.draftText, r.Start), ui.UTF16ByteOffset(f.draftText, r.End)
	candidate := f.draftText[:start] + value + f.draftText[end:]
	spans := replaceNativeRichTextSpans(f.draftText, f.draftSpans, r.Start, r.End, value)
	caret := r.Start + ui.UTF16Len(value)
	return candidate, spans, caret, nil
}

func (f *nativeRichTextField) commitDraft(endComposition bool) bool {
	start, end, replacement, changed := nativeRichTextDiff(f.text, f.draftText)
	return f.commitReplacement(start, end, replacement, changed, endComposition)
}

func (f *nativeRichTextField) commitReplacement(start, end int, replacement string, changed, endComposition bool) bool {
	if changed && f.onReplace != nil {
		text, spans, err := f.onReplace(start, end, replacement)
		if err != nil {
			f.setError(err)
			f.dirty = true
			return false
		}
		f.text, f.spans = text, normalizeNativeRichTextSpans(text, spans)
		f.draftText, f.draftSpans = text, cloneNativeRichTextSpans(f.spans)
		if f.onReplacementCaret != nil {
			caret := max(0, min(f.onReplacementCaret(), ui.UTF16Len(text)))
			f.selection = ui.TextInputSelection{Range: ui.TextInputRange{Start: caret, End: caret}}
		}
	} else {
		f.text, f.spans = f.draftText, cloneNativeRichTextSpans(f.draftSpans)
	}
	f.dirty = false
	f.marked = nil
	f.setError(nil)
	if endComposition && f.composing {
		f.composing = false
		f.hasPending = false
		if f.onComposition != nil {
			f.onComposition(false)
		}
	}
	f.selection.Range = normalizeNativeRichTextRange(f.draftText, f.selection.Range)
	f.layoutDirty = true
	f.ensureLayout()
	f.notifySelection()
	return true
}

func (f *nativeRichTextField) cancelComposition() {
	if !f.composing {
		return
	}
	f.draftText, f.draftSpans = f.compositionText, cloneNativeRichTextSpans(f.compositionSpans)
	f.selection = f.compositionSelection
	f.marked = nil
	f.composing = false
	f.dirty = f.draftText != f.text || !equalNativeRichTextSpans(f.draftSpans, f.spans)
	f.layoutDirty = true
	f.ensureLayout()
	f.notifySelection()
	if f.onComposition != nil {
		f.onComposition(false)
	}
	if f.hasPending {
		text, spans := f.pendingText, f.pendingSpans
		f.hasPending = false
		f.setContent(text, spans)
	}
}

func (f *nativeRichTextField) setErrorForProof(err error) { f.setError(err) }

func (f *nativeRichTextField) discardDraft() {
	if f.composing {
		f.cancelComposition()
	}
	f.draftText, f.draftSpans = f.text, cloneNativeRichTextSpans(f.spans)
	f.dirty, f.hasPending = false, false
	f.marked = nil
	f.selection.Range = normalizeNativeRichTextRange(f.text, f.selection.Range)
	f.setError(nil)
	f.layoutDirty = true
	f.ensureLayout()
	f.notifySelection()
}

// setZoom changes display geometry only. Text, base styles and selection keep
// their source offsets; drawing, hit testing and IME use the same new layout.
func (f *nativeRichTextField) setZoom(zoom float32) {
	if f == nil {
		return
	}
	zoom = clampMarkdownZoom(zoom)
	if f.zoom == zoom {
		return
	}
	f.zoom = zoom
	f.layoutDirty = true
	f.ensureLayout()
}

func (f *nativeRichTextField) ensureLayout() {
	if f == nil {
		return
	}
	if f.layoutWidth <= 0 {
		f.layoutWidth = 520
	}
	f.setLayoutWidth(f.layoutWidth)
}

func (f *nativeRichTextField) setLayoutWidth(width float32) bool {
	if f == nil {
		return false
	}
	width = max(width, float32(1))
	if f.layout != nil && !f.layoutDirty && absNativeRichTextFloat(f.layoutWidth-width) < 0.5 {
		return false
	}
	oldHeight := f.boxHeight
	f.layoutWidth = width
	styled := nativeRichTextMarkedSpans(f.draftText, f.draftSpans, f.marked)
	zoom := clampMarkdownZoom(f.zoom)
	font := f.font
	font.Size *= zoom
	for index := range styled {
		styled[index].Size *= zoom
	}
	f.layout = ui.ShapeRichTextWith(styled, font, ui.TextShape{Width: width, LineHeight: f.lineHeight})
	_, contentHeight := f.layout.Size()
	metrics := font.Metrics()
	lineHeight := metrics.Ascent + metrics.Descent + metrics.LineGap
	if lineHeight < 1 {
		lineHeight = 18 * zoom
	}
	f.layoutHeight = max(contentHeight, lineHeight)
	f.boxHeight = max(f.layoutHeight+2*nativeRichTextInsetY, nativeRichTextMinHeight)
	f.layoutDirty = false
	return absNativeRichTextFloat(oldHeight-f.boxHeight) >= 0.5
}

func (f *nativeRichTextField) validate() error {
	if f == nil || f.layout == nil {
		return errors.New("富文本字段布局尚未生成")
	}
	if !utf8.ValidString(f.draftText) {
		return errors.New("富文本字段含有无效 UTF-8")
	}
	if richTextSpansText(f.draftSpans) != f.draftText {
		return errors.New("富文本样式片段与文本不一致")
	}
	return nil
}

func (f *nativeRichTextField) clipboardText() string {
	if f == nil {
		return ""
	}
	return strings.Clone(f.draftText)
}

var _ ui.TextInputClient = (*nativeRichTextField)(nil)
