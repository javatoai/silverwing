package desktop

import "github.com/egoist/mygo/ui"

// The materials tree keeps its management menu. These are reading actions only.
func (s *app) bindMaterialsReadingContextMenu(state *markdownEditorState) {
	home, taskID, path := s.snapshot.Home, s.currentTask().id, s.documentPath
	state.contextMenu = func(c *ui.Context, menu *ui.Menu) {
		if home != s.snapshot.Home || taskID != s.currentTask().id || path != s.documentPath || !s.markdownEditorIsCurrent(state) {
			return
		}
		menu.Separator()
		if menu.Item("查找正文 · Ctrl+F").Chosen() {
			s.openDocumentFind(c)
		}
		mode, next := "查看 Markdown 源码", "SOURCE"
		if s.readingMode() == "SOURCE" {
			mode, next = "查看 Markdown 预览", "RENDERED"
		}
		if menu.Item(mode).Chosen() {
			s.setReadingMode(next)
		}
		if menu.Item("重新读取文档").Disabled(s.documentReadPending || s.materialsFileInputBusy()).Chosen() {
			s.rereadSelectedMaterial(true)
		}
	}
}
