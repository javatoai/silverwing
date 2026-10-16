package desktop

import (
	"path/filepath"

	"silverwing-go/internal/documents"

	"github.com/egoist/mygo/ui"
)

func (s *app) materialsTextEditingVisible() bool {
	state := s.currentMaterialTextEditor()
	return state != nil && state.editing && documents.EditableMaterialText(state.path) && !s.documentReadOnly && !s.materialsSearchSourcePreviewActive()
}

func (s *app) renderMaterialsTextEditor(c *ui.Context, p palette) {
	state := s.currentMaterialTextEditor()
	if state == nil || state.view == nil {
		return
	}
	s.materialTextActive = state
	_ = s.captureMaterialTextState(state)
	state.view.Label = filepath.Base(state.path)
	state.view.ReadOnly = s.materialsFileReadBlocked() || state.discarding
	state.view.TabSize = 4
	state.view.SoftWrap = textEditorLanguage(state.path) == nil
	state.view.ClipboardWrite = func(text string) bool { return s.copyTextToClipboard(c.Services(), text, "") }
	state.view.OnInput = func(event ui.InputEvent) bool {
		if !s.materialTextBindingCurrent(state) || s.materialsFileReadBlocked() || state.discarding {
			return true
		}
		if event.Kind == ui.InputKeyDown && event.Key == ui.KeyS && (event.Mods&ui.Ctrl != 0 || event.Mods&ui.Super != 0) {
			s.flushMaterialTextAutosave()
			return true
		}
		return false
	}
	ui.Column(c.Key("material-text-editor-" + state.key)).Grow(1).FillWidth().MinHeight(0).Padding(12).Children(func() {
		TextEditor(c, p, state.view).Grow(1).FillWidth().MinHeight(0)
	})
}

func (s *app) toggleMaterialsTextEditing() {
	state := s.currentMaterialTextEditor()
	if state == nil {
		return
	}
	if err := s.captureMaterialTextInput(); err != nil {
		s.lastError = err.Error()
		return
	}
	state.editing = !state.editing
	s.documentEditing = state.editing
}

// Keep one fixed header slot; successful/ordinary autosaves are silent.
func (s *app) renderMaterialsTextEditorHeader(c *ui.Context, p palette) {
	state := s.currentMaterialTextEditor()
	if state == nil || s.documentReadOnly {
		return
	}
	_ = s.captureMaterialTextState(state)
	status := s.markdownAutosave.Status(state.key)
	ui.Row(c.Key("material-text-editor-header")).Height(32).Shrink(0).Padding(0, 12).Gap(7).AlignItems(ui.Center).Children(func() {
		message := ""
		switch {
		case state.inputError != "":
			message = state.inputError
		case status.Conflict:
			message = "文件有外部修改"
		case status.Error != "":
			message = "自动保存失败：" + status.Error
		case status.CacheError != "":
			message = "草稿缓存失败：" + status.CacheError
		case state.restored:
			message = "有未保存草稿"
		}
		label(c, message, 11, p.amber).Grow(1).SingleLine().Tooltip(message)
		if status.Conflict {
			if action(c, "material-text-use-disk", "refresh", "使用磁盘版本", p, false) {
				s.confirmNative("使用磁盘版本？", "确认后将丢弃当前文件的本地草稿。", filepath.Base(state.path), func() { s.discardMaterialTextAndReload(state) })
			}
			if action(c, "material-text-overwrite", "save", "覆盖磁盘版本", p, true) {
				s.confirmNative("覆盖磁盘版本？", "确认后将用当前本地内容替换磁盘上的外部修改。", filepath.Base(state.path), func() { s.retryMaterialTextAutosave(state, true) })
			}
		} else if status.Error != "" || status.CacheError != "" || state.restored {
			if action(c, "material-text-retry", "refresh", "重试保存", p, false) {
				s.retryMaterialTextAutosave(state, false)
			}
			if state.restored && action(c, "material-text-discard", "close", "丢弃草稿", p, false) {
				s.confirmNative("丢弃草稿？", "确认后将使用磁盘上的当前版本。", filepath.Base(state.path), func() { s.discardMaterialTextAndReload(state) })
			}
		}
		if kind := documents.Kind(state.path); kind == "json" || kind == "table" {
			text := "编辑源码"
			if state.editing {
				text = "预览"
			}
			if action(c, "material-text-toggle-source", "edit", text, p, false) {
				s.toggleMaterialsTextEditing()
			}
		}
	})
}
