package desktop

import (
	"fmt"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

// An instruction owns its query and viewport; parsing and hit navigation stay
// in the same documentFindState used by materials and read-only readers.
type markdownInstructionFindState struct {
	find         documentFindState
	query        string
	open         bool
	focusQuery   bool
	mode         bool
	restoreFocus bool
	scroll       ui.ScrollState
}

func (s *app) markdownEditorFindState(state *markdownEditorState) *documentFindState {
	if state == nil || state.doc == nil || state.closed || state.detached || !s.markdownEditorIsCurrent(state) {
		return nil
	}
	if state.binding != nil {
		if state.instructionFind != nil && state.instructionFind.open {
			return &state.instructionFind.find
		}
		return nil
	}
	if s.materialsUnit.state.markdown == state && s.materialsUnit.state.documentFindOpen {
		return s.ensureDocumentFindState()
	}
	return nil
}

func prepareMarkdownInstructionFind(state *markdownEditorState) {
	f := state.instructionFind
	if f.mode != state.sourceMode {
		f.mode = state.sourceMode
	}
	f.find.set(markdownEditorHostID(state)+"\x00"+fmt.Sprint(state.sourceMode), f.query, markdownEditorFindBlocks(state))
}

func (s *app) renderInstructionMarkdown(c *ui.Context, p palette, state *markdownEditorState, height float32) {
	if state.instructionFind == nil {
		state.instructionFind = &markdownInstructionFindState{}
	}
	f := state.instructionFind
	prepareMarkdownInstructionFind(state)
	open := func() {
		if !s.markdownEditorIsCurrent(state) {
			return
		}
		f.open, f.focusQuery = true, true
		c.Invalidate()
	}
	move := func(delta int) {
		if !f.open || !s.markdownEditorIsCurrent(state) || state.doc.Composing() {
			return
		}
		f.find.move(delta)
		c.Invalidate()
	}
	close := func() {
		f.restoreFocus = true
		f.open, f.focusQuery = false, false
		f.query = ""
		f.find.close()
		c.Invalidate()
	}
	c.OnShortcut(ui.Ctrl, ui.KeyF, open)
	c.OnShortcut(ui.Super, ui.KeyF, open)
	c.OnShortcut(0, ui.KeyF3, func() { move(1) })
	c.OnShortcut(ui.Shift, ui.KeyF3, func() { move(-1) })
	if f.open {
		c.OnShortcut(0, ui.KeyEscape, close)
	}
	panel := ui.Column(c.Key(markdownEditorControlKey(state, "instruction-editor-host"))).FillWidth().MinHeight(0).Focusable()
	if height <= 0 {
		panel.Grow(1)
	} else {
		panel.Height(height)
	}
	panel.Children(func() {
		if f.open {
			ui.Row(c).Height(38).Shrink(0).Gap(7).AlignItems(ui.Center).Children(func() {
				input := ui.TextInput(c.Key(markdownEditorControlKey(state, "instruction-find-input")), &f.query).Width(250).Height(30).Label("查找说明正文").Placeholder("查找说明正文").Bind(&f.find.focus)
				input.OnChange(func() {
					prepareMarkdownInstructionFind(state)
				})
				input.OnSubmit(func() { move(1) })
				input.OnShortcut(0, ui.KeyEscape, close)
				if f.focusQuery {
					input.Focus().SetTextSelection(0, utf8.RuneCountInString(f.query))
					f.focusQuery = false
				}
				if action(c, markdownEditorControlKey(state, "instruction-find-previous"), "chevron-left", "上一个结果 · Shift+F3", p, false) {
					move(-1)
				}
				if action(c, markdownEditorControlKey(state, "instruction-find-next"), "chevron", "下一个结果 · F3", p, false) {
					move(1)
				}
				label(c, documentFindStatus(&f.find), 11, p.muted).NoWrap()
				ui.Spacer(c)
				if iconButton(c, markdownEditorControlKey(state, "instruction-find-close"), "close", "关闭说明查找", p).Clicked() {
					close()
				}
			})
		}
		ui.Scroll(c.Key(markdownEditorControlKey(state, "instruction-editor-scroll"))).Grow(1).MinHeight(0).TrackScroll(&f.scroll).Draw(func(_ *ui.Painter, rect ui.Rect) {
			f.find.viewport = rect
		}).Children(func() { s.renderMarkdownEditor(c, p, state) })
	})
	if f.mode != state.sourceMode {
		prepareMarkdownInstructionFind(state)
		c.Invalidate()
	}
	if f.restoreFocus {
		f.restoreFocus = false
		if cursor := state.doc.Snapshot().Cursor; cursor != nil {
			state.doc.RequestFocusCursor(cursor)
			c.Invalidate()
		}
	}
}

func (s *app) markdownEditorFindOuterScrollFor(state *markdownEditorState) *ui.ScrollState {
	if state.binding != nil && state.instructionFind != nil {
		return &state.instructionFind.scroll
	}
	return s.markdownEditorFindOuterScroll()
}
