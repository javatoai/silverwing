package desktop

import (
	"fmt"
	"math"
	"strings"

	"github.com/egoist/mygo/ui"
)

// readonlyMarkdownReaderState keeps find, mode, zoom, and scroll independent
// for each read-only Markdown preview (instructions, requirements, and Skills).
type readonlyMarkdownReaderState struct {
	find             documentFindState
	findOpen         bool
	findDraft        string
	findFocusPending bool
	sourceMode       bool
	zoom             float32

	previewScroll  ui.ScrollState
	sourceScroll   ui.ScrollState
	externalScroll *ui.ScrollState
}

func ensureReadonlyMarkdownReader(state *markdownEditorState) *readonlyMarkdownReaderState {
	if state == nil || !state.readOnly || state.doc == nil || state.closed {
		return nil
	}
	if state.readonlyReader == nil {
		state.readonlyReader = &readonlyMarkdownReaderState{zoom: 1}
	}
	if state.readonlyReader.zoom == 0 {
		state.readonlyReader.zoom = 1
	}
	return state.readonlyReader
}

// bindReadonlyMarkdownScroll connects a reader to a caller-owned
// reading position, when the caller already persists one in its own session.
// Passing nil falls back to the reader's private mode-specific scroll states.
func bindReadonlyMarkdownScroll(state *markdownEditorState, scroll *ui.ScrollState) {
	if state == nil || !state.readOnly {
		return
	}
	reader := ensureReadonlyMarkdownReader(state)
	if reader != nil {
		reader.externalScroll = scroll
	}
}

func readonlyMarkdownReaderScroll(reader *readonlyMarkdownReaderState, sourceMode bool) *ui.ScrollState {
	if reader.externalScroll != nil {
		return reader.externalScroll
	}
	if sourceMode {
		return &reader.sourceScroll
	}
	return &reader.previewScroll
}

func readonlyMarkdownReaderIdentity(state *markdownEditorState, sourceMode bool) string {
	if state == nil || state.doc == nil {
		return ""
	}
	mode := "rendered"
	if sourceMode {
		mode = "source"
	}
	return strings.Join([]string{"readonly-markdown", state.taskID, normalizedReadingPath(state.path), mode}, "\x00")
}

func prepareReadonlyMarkdownReaderFind(state *markdownEditorState, reader *readonlyMarkdownReaderState) {
	if state == nil || state.doc == nil || reader == nil {
		return
	}
	identity := readonlyMarkdownReaderIdentity(state, reader.sourceMode)
	var blocks []documentFindBlock
	if reader.sourceMode {
		blocks = readonlyMarkdownSourceFindBlocks(state.doc.Source())
	} else {
		blocks = frameworkMarkdownFindBlocks(state)
	}
	reader.find.set(identity, reader.find.query, blocks)
}

func readonlyMarkdownSourceFindBlocks(source string) []documentFindBlock {
	blocks := make([]documentFindBlock, 0, strings.Count(source, "\n")+1)
	for index, offset := 0, 0; offset <= len(source); index++ {
		end := strings.IndexByte(source[offset:], '\n')
		lineEnd := len(source)
		next := len(source) + 1
		if end >= 0 {
			lineEnd = offset + end
			next = lineEnd + 1
		}
		line := source[offset:lineEnd]
		if strings.HasSuffix(line, "\r") {
			line = strings.TrimSuffix(line, "\r")
		}
		blocks = append(blocks, documentFindBlock{key: readonlyMarkdownSourceLineKey(index), text: line, scrollY: float32(index) * 20, lineSize: 20})
		if next > len(source) {
			break
		}
		offset = next
	}
	return blocks
}

func readonlyMarkdownSourceLineKey(line int) string {
	return fmt.Sprintf("readonly-markdown-source-line-%d", line)
}

func openReadonlyMarkdownFind(c *ui.Context, state *markdownEditorState) bool {
	reader := ensureReadonlyMarkdownReader(state)
	if reader == nil {
		return false
	}
	prepareReadonlyMarkdownReaderFind(state, reader)
	reader.findOpen = true
	reader.find.navigation++
	if c != nil {
		reader.find.focus.Focus(c)
	}
	return true
}

func moveReadonlyMarkdownFind(state *markdownEditorState, delta int) bool {
	reader := ensureReadonlyMarkdownReader(state)
	if reader == nil || !reader.findOpen {
		return false
	}
	prepareReadonlyMarkdownReaderFind(state, reader)
	reader.find.move(delta)
	return true
}

func closeReadonlyMarkdownFind(state *markdownEditorState) {
	reader := ensureReadonlyMarkdownReader(state)
	if reader == nil {
		return
	}
	reader.findOpen = false
	reader.find.close()
}

func setReadonlyMarkdownSourceMode(state *markdownEditorState, source bool) bool {
	reader := ensureReadonlyMarkdownReader(state)
	if reader == nil || reader.sourceMode == source {
		return false
	}
	reader.sourceMode = source
	prepareReadonlyMarkdownReaderFind(state, reader)
	return true
}

func setReadonlyMarkdownZoom(state *markdownEditorState, delta float32) float32 {
	reader := ensureReadonlyMarkdownReader(state)
	if reader == nil {
		return 0
	}
	zoom := math.Round(float64(reader.zoom+delta)*10) / 10
	reader.zoom = float32(math.Max(.7, math.Min(1.6, zoom)))
	return reader.zoom
}

func renderReadonlyMarkdownReaderFindBar(c *ui.Context, p palette, state *markdownEditorState, reader *readonlyMarkdownReaderState) {
	if reader == nil || !reader.findOpen {
		return
	}
	f := &reader.find
	ui.Row(c).Height(38).Shrink(0).Padding(0, 12).Gap(7).Background(p.soft).AlignItems(ui.Center).Children(func() {
		query := f.query
		input := ui.TextInput(c.Key(readonlyMarkdownInputKey(state)), &query).Grow(1).MinWidth(90).MaxWidth(250).Height(30).Padding(7).Placeholder("查找正文").Background(p.bg).Border(1, p.line).Radius(6).Bind(&f.focus)
		input.OnChange(func() { f.set(f.identity, query, f.blocks) })
		input.OnSubmit(func() { f.move(1) })
		input.OnShortcut(0, ui.KeyEscape, func() { closeReadonlyMarkdownFind(state) })
		if iconButtonDisabled(c, readonlyMarkdownInputKey(state)+"-previous", "chevron-left", "上一个结果 · Shift+F3", p, len(f.hits) == 0).Clicked() {
			f.move(-1)
		}
		if iconButtonDisabled(c, readonlyMarkdownInputKey(state)+"-next", "chevron", "下一个结果 · F3", p, len(f.hits) == 0).Clicked() {
			f.move(1)
		}
		label(c, documentFindStatus(f), 11, p.muted).NoWrap()
		if f.truncated {
			label(c, "仅统计前 10,000 处", 10, p.amber).NoWrap()
		}
		ui.Spacer(c)
		if iconButton(c, readonlyMarkdownInputKey(state)+"-close", "close", "关闭查找", p).Clicked() {
			closeReadonlyMarkdownFind(state)
		}
	})
}

func readonlyMarkdownInputKey(state *markdownEditorState) string {
	if state == nil {
		return "readonly-markdown-find"
	}
	return "readonly-markdown-find-" + state.taskID + "-" + normalizeIdentityPath(state.path)
}

// renderReadonlyMarkdownReader shares the native Markdown AST renderer while
// keeping reader interaction state detached from the task-material editor.
func (s *app) renderReadonlyMarkdownReader(c *ui.Context, p palette, state *markdownEditorState) {
	s.renderReadonlyMarkdownReaderWithMenu(c, p, state, nil)
}

// renderReadonlyMarkdownReaderActions binds document actions to the native
// selection menu. It reserves no title-row space.
func (s *app) renderReadonlyMarkdownReaderActions(c *ui.Context, p palette, state *markdownEditorState, extraMenu func(*overflowMenuBuilder)) {
	reader := ensureReadonlyMarkdownReader(state)
	if reader == nil {
		return
	}
	prepareReadonlyMarkdownReaderFind(state, reader)
	home := s.snapshot.Home
	state.contextMenu = func(c *ui.Context, native *ui.Menu) {
		if state.closed || home != s.snapshot.Home || isSkillMarkdownPreview(state) && !s.skillMarkdownPreviewCurrent(state) {
			return
		}
		native.Separator()
		menu := &overflowMenuBuilder{native: native}
		if menu.Item("find", "查找正文 · Ctrl+F").Icon("search").Chosen() {
			openReadonlyMarkdownFind(c, state)
			reader.findFocusPending = true
		}
		label := "查看 Markdown 源码"
		if reader.sourceMode {
			label = "查看 Markdown 预览"
		}
		if menu.Item("toggle-markdown-mode", label).Icon("code").Chosen() {
			setReadonlyMarkdownSourceMode(state, !reader.sourceMode)
		}
		if extraMenu != nil {
			menu.Separator()
			extraMenu(menu)
		}
	}
}

// A document owner may append its actions to the same native reading menu.
func (s *app) renderReadonlyMarkdownReaderWithMenu(c *ui.Context, p palette, state *markdownEditorState, extraMenu func(*overflowMenuBuilder)) {
	reader := ensureReadonlyMarkdownReader(state)
	if reader == nil {
		label(c, "Markdown 预览暂不可用。", 13, p.muted)
		return
	}
	s.renderReadonlyMarkdownReaderActions(c, p, state, extraMenu)
	s.renderReadonlyMarkdownReaderContentWithInset(c, p, state, 0)
}

// renderReadonlyMarkdownReaderContent renders only the find bar (when open)
// and the scrollable document. Callers with a title row can render actions
// there via renderReadonlyMarkdownReaderActions and avoid a floating control.
func (s *app) renderReadonlyMarkdownReaderContent(c *ui.Context, p palette, state *markdownEditorState) {
	s.renderReadonlyMarkdownReaderContentWithInset(c, p, state, 0)
}

func (s *app) renderReadonlyMarkdownReaderContentWithInset(c *ui.Context, p palette, state *markdownEditorState, rightInset float32) {
	reader := ensureReadonlyMarkdownReader(state)
	if reader == nil {
		label(c, "Markdown 预览暂不可用。", 13, p.muted)
		return
	}
	state.sourceMode = reader.sourceMode
	prepareReadonlyMarkdownReaderFind(state, reader)
	c.OnShortcut(ui.Ctrl, ui.KeyF, func() { openReadonlyMarkdownFind(c, state) })
	c.OnShortcut(ui.Super, ui.KeyF, func() { openReadonlyMarkdownFind(c, state) })
	c.OnShortcut(0, ui.KeyF3, func() { moveReadonlyMarkdownFind(state, 1) })
	c.OnShortcut(ui.Shift, ui.KeyF3, func() { moveReadonlyMarkdownFind(state, -1) })
	ui.Column(c).FillWidth().Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		if reader.findOpen {
			renderReadonlyMarkdownReaderFindBar(c, p, state, reader)
			if reader.findFocusPending {
				focused := reader.find.focus.Focused(c)
				reader.find.focus.Focus(c)
				if focused {
					reader.findFocusPending = false
				}
			}
		}
		preview := ui.ScrollBoth(c).TrackScroll(readonlyMarkdownReaderScroll(reader, reader.sourceMode)).Grow(1).MinHeight(0)
		preview.Draw(func(_ *ui.Painter, rect ui.Rect) { reader.find.viewport = rect })
		preview.ContextMenu(func(menu *ui.Menu) {
			menu.EditItems()
			if state.contextMenu != nil {
				state.contextMenu(c, menu)
			}
		})
		preview = preview.HandleInput(func(event ui.InputEvent) bool {
			if event.Kind != ui.InputScroll || event.Mods&ui.Ctrl == 0 || event.DY == 0 {
				return false
			}
			step := float32(-.1)
			if event.DY < 0 {
				step = .1
			}
			return setReadonlyMarkdownZoom(state, step) > 0
		})
		if reader.sourceMode {
			renderReadonlyMarkdownSource(preview, c, p, state, reader, rightInset)
			return
		}
		padding := []float32{18, 26}
		if rightInset > 0 {
			// Preserve the document's normal top inset while keeping the text
			// clear of the floating actions button.
			padding = []float32{18, rightInset, 26, 26}
		}
		preview.Padding(padding...).Children(func() { s.renderMarkdownPreview(c, p, state) })
	})
}

func renderReadonlyMarkdownSource(preview ui.Element, c *ui.Context, p palette, state *markdownEditorState, reader *readonlyMarkdownReaderState, rightInset float32) {
	source := state.doc.Source()
	zoom := clampMarkdownZoom(reader.zoom)
	preview.Selectable().ContextMenu(func(menu *ui.Menu) {
		menu.EditItems()
		if state.contextMenu != nil {
			state.contextMenu(c, menu)
		}
	}).Padding(15, max(float32(15), rightInset), 15, 15).Children(func() {
		if strings.TrimSpace(source) == "" {
			label(c, "（空）", scaleMarkdownSize(13, zoom), p.muted)
			return
		}
		f := &reader.find
		for index, offset := 0, 0; offset <= len(source); index++ {
			end := strings.IndexByte(source[offset:], '\n')
			lineEnd := len(source)
			next := len(source) + 1
			if end >= 0 {
				lineEnd = offset + end
				next = lineEnd + 1
			}
			line := source[offset:lineEnd]
			if strings.HasSuffix(line, "\r") {
				line = strings.TrimSuffix(line, "\r")
			}
			key := readonlyMarkdownSourceLineKey(index)
			spans := f.spans(key, line, 0, p.text, nil)
			if len(spans) == 0 {
				spans = []ui.Span{{Text: " "}}
			}
			text := ui.RichText(c, spans...).Font("Consolas, monospace").FontSize(scaleMarkdownSize(13, zoom)).LineHeight(1.5).NoWrap().Selectable()
			if f.shouldRevealRange(key, 0, maxInt(len(line), 1)) {
				text.ScrollIntoView()
			}
			if next > len(source) {
				break
			}
			offset = next
		}
	})
}
