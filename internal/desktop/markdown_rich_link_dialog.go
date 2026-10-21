package desktop

import (
	"silverwing-go/internal/editor"

	"github.com/egoist/mygo/ui"
)

type markdownRichLinkDialogState struct {
	open      bool
	partID    uint64
	rich      *markdownRichFieldState
	version   uint64
	selection ui.TextInputSelection
	url       string
}

func (s *app) openMarkdownRichLink(state *markdownEditorState, rich *markdownRichFieldState) {
	if !s.markdownRichFieldCurrent(state, rich) || state.doc.Composing() {
		return
	}
	selection := rich.input.getSelection()
	if selection.Range.Start == selection.Range.End {
		s.lastError = "先选择要设置链接的文字。"
		return
	}
	state.richLink = &markdownRichLinkDialogState{open: true, partID: rich.partID, rich: rich, version: state.doc.Snapshot().Version, selection: selection, url: rich.model.SelectedLink(selection.Range.Start, selection.Range.End)}
}

func (s *app) renderMarkdownRichLinkDialog(c *ui.Context, p palette) {
	state := s.activeMarkdownEditorForDialog()
	if state == nil || state.richLink == nil || !state.richLink.open {
		return
	}
	dialog := state.richLink
	ui.DialogBase(c.Key(markdownEditorControlKey(state, "markdown-rich-link-dialog")), &dialog.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .32))
		width, _ := c.Size()
		panel.Width(min(float32(520), width-60)).Padding(22).Gap(14).Background(p.surface).Radius(12)
		panel.OnShortcut(0, ui.KeyEscape, func() { dialog.open = false })
		panel.Children(func() {
			label(c, "编辑链接", 18, p.text).FontWeight(600)
			ui.TextInput(c.Key(markdownEditorControlKey(state, "markdown-rich-link-url")), &dialog.url).FillWidth().Height(40).Placeholder("输入链接地址；留空可移除链接")
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if action(c, markdownEditorControlKey(state, "markdown-rich-link-cancel"), "close", "取消", p, false) {
					dialog.open = false
				}
				if action(c, markdownEditorControlKey(state, "markdown-rich-link-apply"), "check", "保存链接", p, true) {
					s.applyMarkdownRichLink(state)
				}
			})
		})
	})
}

func (s *app) applyMarkdownRichLink(state *markdownEditorState) {
	dialog := state.richLink
	if dialog == nil {
		return
	}
	rich := dialog.rich
	if rich == nil {
		rich = state.richFields[dialog.partID]
	}
	if rich == nil || !s.markdownRichFieldCurrent(state, rich) || state.doc.Snapshot().Version != dialog.version {
		s.lastError = "文档已变化，请重新选择链接文字。"
		dialog.open = false
		return
	}
	rich.input.setSelection(dialog.selection)
	s.formatMarkdownRichSelection(state, rich, editor.FormatLink, 0, dialog.url)
	dialog.open = false
}
