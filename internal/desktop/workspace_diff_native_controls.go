package desktop

import (
	"runtime"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// The SDK's caret is the common navigation state for mouse selection, find,
// go-to-line, keyboard shortcuts and toolbar buttons. CurrentHunk also counts
// the preceding change while the caret is in unchanged source.
func syncWorkspaceNativeHunk(state *workspaceHistoryState, view *diffNativeViewState) bool {
	if state == nil || view == nil {
		return false
	}
	index, _ := view.view.CurrentHunk()
	selected := index >= 0
	changed := state.DiffHunkIndex != index || state.DiffHunkSelected != selected
	state.DiffHunkIndex, state.DiffHunkSelected = index, selected
	return changed
}

func restoreWorkspaceNativeScroll(c *ui.Context, state *workspaceHistoryState, view *diffNativeViewState) {
	saved := workspaceDiffRendererStateFor(state, state.ActiveDiffRendererKey)
	if saved == nil || !saved.NativeRestorePending {
		return
	}
	if !saved.NativeRestoreReady {
		saved.NativeRestoreReady = true
		c.Invalidate()
		return
	}
	saved.NativeRestorePending = false
	if view.view.Cursor() != saved.NativeRestoreCursor || view.view.ActiveSide() != saved.NativeRestoreSide {
		return // An input since file restoration owns the new reading position.
	}
	position := view.view.ScrollPosition()
	position.OldX, position.NewX = saved.NativeOldX, saved.NativeNewX
	if saved.NativeRestoreVertical {
		position.Y = saved.NativeScrollY
	}
	view.view.SetScrollPosition(position)
}

func renderWorkspaceNativeControls(c *ui.Context, p palette, state *workspaceHistoryState, view *diffNativeViewState, ratio float32) {
	comparison := state.Preview.Comparison
	hunks := view.hunkCount()
	previousKey, nextKey, findKey := "Shift+Alt+F5", "Alt+F5", "Ctrl+F"
	if runtime.GOOS == "darwin" {
		previousKey, nextKey, findKey = "Shift+Cmd+F8", "Cmd+F8", "Cmd+F"
	}
	run := func(command editor.Command) {
		view.view.Run(command)
		syncWorkspaceNativeHunk(state, view)
		c.Invalidate()
	}
	workspaceDiffHeader(c, p, state, comparison, ratio, func(width float32) {
		workspaceDiffHunkButton(c, "workspace-diff-hunk-previous", "上一处", p, hunks > 0, func() {
			run(editor.PreviousHunk)
		}).Tooltip("上一处（" + previousKey + "，循环跳转）")
		hunkLabel := "无差异块"
		if hunks > 0 && state.DiffHunkSelected {
			hunkLabel = formatWorkspaceDiffCount(state.DiffHunkIndex+1) + " / " + formatWorkspaceDiffCount(hunks)
		} else if hunks > 0 {
			hunkLabel = formatWorkspaceDiffCount(hunks) + " 处差异"
		}
		label(c, hunkLabel, 10, p.muted).NoWrap().Shrink(0)
		workspaceDiffHunkButton(c, "workspace-diff-hunk-next", "下一处", p, hunks > 0, func() {
			run(editor.NextHunk)
		}).Tooltip("下一处（" + nextKey + "，循环跳转）")
		if width >= 500 {
			added, removed := view.stats()
			label(c, "+"+formatWorkspaceDiffCount(added), 10, p.green).NoWrap().Shrink(0)
			label(c, "−"+formatWorkspaceDiffCount(removed), 10, p.danger).NoWrap().Shrink(0)
		}
	}, func() {
		if workspaceNativeReadingButton(c, p, "workspace-native-find", "查找", "查找当前侧文本（"+findKey+"）").Clicked() {
			run(editor.Find)
		}
		if workspaceNativeReadingButton(c, p, "workspace-native-goto-line", "跳到行", "跳到当前侧原始行（Ctrl+G）").Clicked() {
			run(editor.GoToLine)
		}
	})
}

func workspaceNativeReadingButton(c *ui.Context, p palette, key, title, tooltip string) ui.Element {
	button := ui.ButtonBase(c.Key(key)).Height(24).Padding(0, 8).Shrink(0).Radius(6).AlignItems(ui.Center).Label(title).Tooltip(tooltip)
	if button.Hovered() {
		button.Background(p.selected)
	} else {
		button.Background(p.surface)
	}
	button.Children(func() { label(c, title, 10, p.text).NoWrap() })
	return button
}
