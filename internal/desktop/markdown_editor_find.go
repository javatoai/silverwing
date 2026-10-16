package desktop

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"silverwing-go/internal/editor"

	"github.com/egoist/mygo/ui"
)

type markdownEditorFindHighlight struct {
	Range  ui.TextInputRange
	Active bool
}

// markdownEditorFindBlocks mirrors the text each native Markdown field shows.
// It walks document parts and their rows directly; field maps are only used to
// pick up an existing draft for a known, stable key.
func markdownEditorFindBlocks(state *markdownEditorState) []documentFindBlock {
	if state == nil || state.doc == nil {
		return nil
	}
	source := state.doc.Source()
	if state.sourceMode {
		return []documentFindBlock{{key: "source", text: markdownEditorFindNativeText(state, "source", source), lineSize: 20}}
	}

	blocks := make([]documentFindBlock, 0)
	for _, part := range state.doc.Parts() {
		if markdownFoldablePart(part.Kind) && !markdownRawPartExpanded(state, part) {
			continue
		}
		baseLine := strings.Count(source[:clampInt(part.Range.Start, 0, len(source))], "\n")
		baseY := float32(baseLine) * 24
		switch part.Kind {
		case editor.PartRich:
			text := markdownEditorFindRichText(state.richFields[part.ID], part)
			blocks = append(blocks, documentFindBlock{key: markdownRichFieldKey(part.ID), text: text, scrollY: baseY, lineSize: 24})
		case editor.PartTable:
			cells := editor.TableCells(part.Source)
			if state.readOnly || len(cells) == 0 || !markdownEditorFindCanBuildStructuredRichFields(cells) {
				blocks = append(blocks, documentFindBlock{key: fmt.Sprintf("part-%d", part.ID), text: part.Source, scrollY: baseY, lineSize: 20})
				continue
			}
			for _, row := range cells {
				for _, cell := range row {
					key := markdownStructuredTableFieldKey(part.ID, cell.Row, cell.Column)
					text := markdownEditorFindStructuredText(state.structuredRichFields[key], cell.Text)
					blocks = append(blocks, documentFindBlock{key: key, text: text,
						scrollY: baseY + float32(cell.Row)*36, lineSize: 20})
				}
			}
		case editor.PartTasks:
			tasks := editor.TaskItems(part.Source)
			if !state.readOnly && len(tasks) > 0 && markdownNonblankLineCount(part.Source) == len(tasks) &&
				markdownEditorFindCanBuildTaskRichFields(tasks) {
				for _, task := range tasks {
					key := markdownStructuredTaskFieldKey(part.ID, task.Index)
					text := markdownEditorFindStructuredText(state.structuredRichFields[key], task.Text)
					blocks = append(blocks, documentFindBlock{key: key, text: text,
						scrollY: baseY + float32(task.Index)*32, lineSize: 20})
				}
			} else {
				blocks = append(blocks, documentFindBlock{key: fmt.Sprintf("part-%d", part.ID), text: part.Source, scrollY: baseY, lineSize: 20})
			}
		case editor.PartCode, editor.PartMermaid:
			code := part.Source
			if fence := editor.FindFence(part.Source); fence != nil {
				code = fence.Code(part.Source)
			}
			key := fmt.Sprintf("code-%d", part.ID)
			blocks = append(blocks, documentFindBlock{key: key, text: markdownEditorFindNativeText(state, key, code), scrollY: baseY, lineSize: 20})
		default:
			key := fmt.Sprintf("part-%d", part.ID)
			blocks = append(blocks, documentFindBlock{key: key, text: markdownEditorFindNativeText(state, key, part.Source), scrollY: baseY, lineSize: 20})
		}
	}
	return blocks
}

func markdownEditorFindNativeText(state *markdownEditorState, key, source string) string {
	if field := state.nativeFields[key]; field != nil && field.view.Buffer != nil {
		return field.view.Buffer.OriginalText()
	}
	return source
}

func markdownEditorFindRichText(field *markdownRichFieldState, part editor.Part) string {
	if field != nil {
		if field.input != nil {
			return field.input.draftText
		}
		if field.model != nil {
			return field.model.Text()
		}
	}
	model, err := editor.NewRichBlock(part)
	if err == nil {
		return model.Text()
	}
	return part.Source
}

func markdownEditorFindStructuredText(field *markdownStructuredRichFieldState, source string) string {
	if field != nil && field.rich != nil {
		if field.rich.input != nil {
			return field.rich.input.draftText
		}
		if field.rich.model != nil {
			return field.rich.model.Text()
		}
	}
	model, err := editor.NewRichBlock(editor.Part{Kind: editor.PartRich, Source: source})
	if err == nil {
		return model.Text()
	}
	return source
}

func markdownEditorFindCanBuildStructuredRichFields(cells [][]editor.TableCell) bool {
	for _, row := range cells {
		for _, cell := range row {
			if _, err := editor.NewRichBlock(editor.Part{Kind: editor.PartRich, Source: cell.Text}); err != nil {
				return false
			}
		}
	}
	return true
}

func markdownEditorFindCanBuildTaskRichFields(tasks []editor.TaskItem) bool {
	for _, task := range tasks {
		if _, err := editor.NewRichBlock(editor.Part{Kind: editor.PartRich, Source: task.Text}); err != nil {
			return false
		}
	}
	return true
}

func markdownEditorFindHighlights(find *documentFindState, key, text string) []markdownEditorFindHighlight {
	if find == nil || find.query == "" || key == "" || text == "" {
		return nil
	}
	active, hasActive := find.current()
	highlights := make([]markdownEditorFindHighlight, 0)
	for _, hit := range find.hits {
		if hit.block != key || hit.start < 0 || hit.end <= hit.start || hit.end > len(text) ||
			!utf8.RuneStart(text[hit.start]) || hit.end < len(text) && !utf8.RuneStart(text[hit.end]) {
			continue
		}
		highlights = append(highlights, markdownEditorFindHighlight{
			Range:  ui.TextInputRange{Start: ui.UTF16Len(text[:hit.start]), End: ui.UTF16Len(text[:hit.end])},
			Active: hasActive && hit == active,
		})
	}
	return highlights
}

func paintMarkdownEditorFindHighlights(painter *ui.Painter, field *nativeRichTextField, p palette, originX, originY float32) {
	if painter == nil || field == nil || field.layout == nil || len(field.findHighlights) == 0 {
		return
	}
	for _, highlight := range field.findHighlights {
		color := markdownEditorFindHighlightColor(p, highlight.Active)
		for _, rect := range field.layout.SelectionRects(highlight.Range) {
			rect.X += originX
			rect.Y += originY
			painter.Fill(rect, color, 2)
		}
	}
}

func markdownEditorFindHighlightColor(p palette, active bool) ui.Color {
	darkText := p.text.R > 140 && p.text.G > 140 && p.text.B > 140
	if darkText {
		if active {
			return ui.Hex("#526F9C")
		}
		return ui.Hex("#344A67")
	}
	if active {
		return ui.Hex("#92AFFF")
	}
	return ui.Hex("#C9D7FF")
}

func (s *app) decorateMarkdownEditorFind(c *ui.Context, p palette, state *markdownEditorState, key, text string, element ui.Element, rich *markdownRichFieldState) ui.Element {
	if rich != nil && rich.input != nil {
		rich.input.findHighlights = nil
	}
	find := s.markdownEditorFindState(state)
	if key == "" || find == nil {
		if rich != nil {
			return element
		}
		return s.decorateMarkdownEditorFindRaw(state, key, text, element, nil, p, c)
	}
	if rich != nil && rich.input != nil {
		visibleText := rich.input.draftText
		rich.input.findHighlights = markdownEditorFindHighlights(find, key, visibleText)
		if hit, ok := find.activeInBlock(key); ok && find.lastReveal != find.navigation && hit.start >= 0 && hit.end <= len(visibleText) {
			identity, navigation := find.identity, find.navigation
			element.ScrollIntoView().DrawOver(func(_ *ui.Painter, bounds ui.Rect) {
				if find.identity != identity || find.navigation != navigation || find.query == "" ||
					s.markdownEditorFindState(state) != find || bounds.W <= 0 || bounds.H <= 0 ||
					find.lastReveal == navigation || find.viewport.H <= 0 || rich.input.layout == nil ||
					hit.start < 0 || hit.start >= len(visibleText) || !utf8.RuneStart(visibleText[hit.start]) {
					return
				}
				// The field can re-shape to its actual width in Draw, after this
				// frame's tree captured the old box height. Wait for the next build
				// so caret geometry and the outer scroll range describe one layout.
				if absNativeRichTextFloat(bounds.H-maxFloat32(rich.input.boxHeight, nativeRichTextMinHeight)) >= .5 {
					c.AnimationFrame()
					return
				}
				caret := rich.input.layout.CaretAt(ui.TextCaretPosition{
					Index: ui.UTF16Len(visibleText[:hit.start]), Affinity: ui.TextDownstream,
				})
				if caret.H <= 0 {
					return
				}
				top := bounds.Y + nativeRichTextInsetY + caret.Y
				bottom := top + caret.H
				delta := markdownEditorFindRevealDelta(find.viewport, top, bottom)
				scroll := s.markdownEditorFindOuterScrollFor(state)
				if scroll == nil {
					return
				}
				next := clampFloat32(scroll.Y+delta, 0, scroll.MaxY)
				if absNativeRichTextFloat(next-scroll.Y) >= .5 {
					scroll.Y = next
					c.AnimationFrame()
				}
				find.lastReveal = navigation
			})
		}
		return element
	}
	return s.decorateMarkdownEditorFindRaw(state, key, text, element, find, p, c)
}

func (s *app) decorateMarkdownEditorFindRaw(state *markdownEditorState, key, text string, element ui.Element, find *documentFindState, p palette, c *ui.Context) ui.Element {
	if state == nil || key == "" {
		return element
	}
	if field := state.nativeFields[key]; field != nil && field.view.Buffer != nil {
		return s.decorateMarkdownEditorFindNative(c, state, key, text, element, find, field)
	}
	if state.findScrolls == nil {
		state.findScrolls = make(map[string]*ui.ScrollState)
	}
	scroll := state.findScrolls[key]
	if scroll == nil {
		scroll = &ui.ScrollState{}
		state.findScrolls[key] = scroll
	}
	lines := &ui.TextLines{}
	element = element.TrackScroll(scroll).TrackLines(lines)

	paddingX, paddingY, fontSize := float32(10), float32(10), scaleMarkdownSize(12, state.zoom)
	if key == "source" {
		paddingX, paddingY, fontSize = 14, 14, 13
	}
	const border = float32(1)
	contentX, contentY := paddingX+border, paddingY+border
	contentWidthForElement := func(bounds ui.Rect) float32 {
		return maxFloat32(bounds.W-2*contentX, 1)
	}
	font := ui.Font{Family: "Consolas, monospace", Size: fontSize}
	if find == nil || find.query == "" || s.markdownEditorFindState(state) != find {
		return element
	}
	identity, navigation := find.identity, find.navigation
	highlights := markdownEditorFindHighlights(find, key, text)
	activeHit, hasActive := find.activeInBlock(key)
	if hasActive && find.lastReveal != navigation && activeHit.start >= 0 && activeHit.end <= len(text) {
		element.ScrollIntoView()
	}
	element.DrawOver(func(painter *ui.Painter, bounds ui.Rect) {
		if painter == nil || bounds.W <= 0 || bounds.H <= 0 || find.identity != identity || find.navigation != navigation ||
			find.query == "" || s.markdownEditorFindState(state) != find {
			return
		}
		contentWidth := contentWidthForElement(bounds)
		layout := ui.ShapeRichText([]ui.Span{{Text: text, Font: font.Family, Size: font.Size}}, font, contentWidth)
		content := ui.Rect{X: bounds.X + contentX, Y: bounds.Y + contentY,
			W: contentWidth, H: maxFloat32(bounds.H-2*contentY, 1)}
		painter.Clip(content, 0, func() {
			for _, highlight := range highlights {
				for _, rect := range layout.SelectionRects(highlight.Range) {
					rect.X += content.X
					rect.Y += content.Y - scroll.Y
					// TextArea paints its own glyphs before DrawOver. A thin underline
					// keeps those glyphs and the user's selection fully visible.
					rect.Y += maxFloat32(rect.H-2, 0)
					rect.H = 2
					painter.Fill(rect, markdownEditorFindHighlightColor(p, highlight.Active), 1)
				}
			}
		})
		if !hasActive || activeHit.block != key || find.lastReveal == navigation || activeHit.start < 0 || activeHit.end > len(text) {
			return
		}
		rects := layout.SelectionRects(ui.TextInputRange{Start: ui.UTF16Len(text[:activeHit.start]), End: ui.UTF16Len(text[:activeHit.end])})
		if len(rects) == 0 {
			return
		}
		lineIndex, rowIndex := markdownEditorFindTextAreaPosition(lines, text, activeHit.start)
		rows := lines.Rows(lineIndex)
		if rowIndex < 0 || rowIndex >= len(rows) {
			return
		}
		// TextArea lays paragraphs lazily. Measure preceding paragraphs so Top
		// reflects their real wrapped heights before scrolling to this hit.
		for line := 0; line < lineIndex; line++ {
			lines.Rows(line)
		}
		lineTop := lines.Top(lineIndex)
		lineStart := strings.LastIndex(text[:activeHit.start], "\n") + 1
		lineStartCaret := layout.CaretAt(ui.TextCaretPosition{Index: ui.UTF16Len(text[:lineStart]), Affinity: ui.TextDownstream})
		activeCaret := layout.CaretAt(ui.TextCaretPosition{Index: ui.UTF16Len(text[:activeHit.start]), Affinity: ui.TextDownstream})
		viewportHeight := content.H
		_, contentHeight := layout.Size()
		hitTop := lineTop + activeCaret.Y - lineStartCaret.Y
		nextY := markdownEditorFindScrollTarget(scroll.Y, viewportHeight, contentHeight, hitTop, hitTop+activeCaret.H)
		if absNativeRichTextFloat(nextY-scroll.Y) >= .5 {
			scroll.Y = nextY
			c.AnimationFrame()
		}
		find.lastReveal = navigation
	})
	return element
}

func (s *app) decorateMarkdownEditorFindNative(c *ui.Context, state *markdownEditorState, key, text string, element ui.Element, find *documentFindState, field *markdownNativeFieldState) ui.Element {
	if find == nil || find.query == "" || find.lastReveal == find.navigation || s.markdownEditorFindState(state) != find {
		return element
	}
	hit, ok := find.activeInBlock(key)
	if !ok || hit.start < 0 || hit.end > len(text) {
		return element
	}
	identity, navigation := find.identity, find.navigation
	field.view.ScrollTo(field.view.Buffer.Pos(markdownNativeProjectedOffset(text, hit.start)).Line)
	return element.ScrollIntoView().DrawOver(func(_ *ui.Painter, bounds ui.Rect) {
		if bounds.W <= 0 || bounds.H <= 0 || find.identity != identity || find.navigation != navigation ||
			find.query == "" || s.markdownEditorFindState(state) != find || find.lastReveal == navigation {
			return
		}
		if scroll := s.markdownEditorFindOuterScrollFor(state); scroll != nil && find.viewport.H > 0 {
			delta := markdownEditorFindRevealDelta(find.viewport, bounds.Y, bounds.Y+minFloat32(bounds.H, textEditorLineHeight*textEditorFontSize))
			next := clampFloat32(scroll.Y+delta, 0, scroll.MaxY)
			if absNativeRichTextFloat(next-scroll.Y) >= .5 {
				scroll.Y = next
				c.AnimationFrame()
			}
		}
		// Native highlights and inner scrolling use the editor's own geometry;
		// the query keeps keyboard focus while a match is revealed.
		find.lastReveal = navigation
	})
}

func markdownEditorFindRevealDelta(viewport ui.Rect, top, bottom float32) float32 {
	margin := minFloat32(12, maxFloat32(viewport.H, 0)/4)
	if top < viewport.Y+margin {
		return top - (viewport.Y + margin)
	}
	if bottom > viewport.Y+viewport.H-margin {
		return bottom - (viewport.Y + viewport.H - margin)
	}
	return 0
}

func (s *app) markdownEditorFindOuterScroll() *ui.ScrollState {
	scrollID := "markdown-native"
	if s.readingMode() == "SOURCE" {
		scrollID = "source-vertical"
	}
	return s.readingScrollState(scrollID)
}

func markdownEditorFindTextAreaPosition(lines *ui.TextLines, text string, byteOffset int) (line, row int) {
	byteOffset = clampInt(byteOffset, 0, len(text))
	line = strings.Count(text[:byteOffset], "\n")
	lineStart := strings.LastIndex(text[:byteOffset], "\n") + 1
	runeOffset := utf8.RuneCountInString(text[lineStart:byteOffset])
	if lines == nil {
		return line, 0
	}
	rows := lines.Rows(line)
	row = maxInt(sort.Search(len(rows), func(i int) bool { return rows[i] > runeOffset })-1, 0)
	return line, row
}

func markdownEditorFindScrollTarget(scrollY, viewportHeight, contentHeight, hitTop, hitBottom float32) float32 {
	margin := minFloat32(12, maxFloat32(viewportHeight, 0)/4)
	next := scrollY
	if hitTop < scrollY+margin {
		next += hitTop - (scrollY + margin)
	} else if hitBottom > scrollY+viewportHeight-margin {
		next += hitBottom - (scrollY + viewportHeight - margin)
	}
	return clampFloat32(next, 0, maxFloat32(contentHeight-viewportHeight, 0))
}
