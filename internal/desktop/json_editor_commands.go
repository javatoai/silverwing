package desktop

import "github.com/egoist/mygo/ui"

// The source projection owns clipboard/history commands. Native selection,
// pointer input, composing and ordinary typing remain MyGO's.
func (s *app) bindJSONEditorCommands(c *ui.Context, state *toolPageState) {
	services := c.Services()
	state.jsonEditor.native.ReplaceContextMenu = true
	state.jsonEditor.native.ContextMenu = func(menu *ui.Menu) {
		s.pullJSONNativeInput(state)
		choose := func(label, command string, disabled bool) {
			if menu.Item(label).Disabled(disabled).Chosen() {
				s.runJSONNativeCommand(state, services, command)
			}
		}
		choose("撤销", "undo", state.jsonEditor.historyIndex <= 0)
		choose("重做", "redo", state.jsonEditor.historyIndex+1 >= len(state.jsonEditor.history))
		menu.Separator()
		choose("剪切", "cut", false)
		choose("复制", "copy", false)
		choose("粘贴", "paste", false)
		choose("全选", "selectAll", false)
		menu.Separator()
		if menu.Item("复制完整 JSON").Disabled(state.jsonSource == "").Chosen() {
			s.copyTextToClipboard(services, state.jsonSource, "")
		}
		start, _ := state.jsonEditor.nativeSelection()
		caret := originalOffset(state.jsonEditor.projection, start)
		if request, ok := jsonEditorNodeCopyAtCaret(state, caret, services); ok {
			chosen := ""
			for _, item := range []struct{ label, command string }{
				{"复制节点值", "copy-node-value"}, {"复制 JSON 片段", "copy-node-fragment"}, {"复制 JSON Pointer", "copy-node-pointer"},
			} {
				if menu.Item(item.label).Chosen() {
					chosen = item.command
				}
			}
			if chosen == "" {
				state.jsonEditor.nativeNodeMenu = &request
			} else if state.jsonEditor.nativeNodeMenu != nil {
				s.copyJSONEditorNode(state, chosen, state.jsonEditor.nativeNodeMenu)
			}
		} else {
			state.jsonEditor.nativeNodeMenu = nil
		}
	}
}

func jsonEditorSelectedSource(state *toolPageState, start, end int) string {
	runes := []rune(state.jsonSource)
	from := originalOffset(state.jsonEditor.projection, min(start, end))
	to := originalOffset(state.jsonEditor.projection, max(start, end))
	from = bounded(from, 0, len(runes))
	return string(runes[from:bounded(to, from, len(runes))])
}
