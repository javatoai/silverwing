package desktop

import (
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (s *app) taskIndex(c *ui.Context, p palette) {
	archived := s.page == "archive"
	state := s.taskIndexBrowsingFor(archived)
	rows := s.taskIndexRows(archived)
	state.visibleRows = append(state.visibleRows[:0], rows...)
	s.prepareTaskIndexHandles(state, archived)
	ui.Column(c).Fill().MinWidth(0).Background(p.soft).Children(func() {
		s.taskIndexToolbar(c, p, state)
		ui.Divider(c)
		ui.Scroll(c.Key("task-index-" + s.page)).TrackScroll(&state.scroll).Grow(1).MinHeight(0).Padding(8).Gap(4).Children(func() {
			for _, index := range rows {
				s.taskIndexTask(c, p, state, index)
			}
			if len(rows) == 0 {
				message := "还没有研发任务\n请在需求列表中创建任务。"
				switch {
				case strings.TrimSpace(state.search) != "":
					message = "没有匹配的任务\n试试其他关键词，或清除搜索。"
				case archived:
					message = "还没有已归档任务。"
				}
				ui.Column(c).FillWidth().Padding(24, 12).Gap(8).Center().Children(func() {
					label(c, message, 12, p.muted).FillWidth().Wrap()
				})
			}
		})
		ui.Divider(c)
		b := ui.ButtonBase(c.Key("archive-link")).Height(48).Row().Gap(6).Center().Label("查看归档")
		b.Children(func() {
			icon(c, "archive", 18, p.blue)
			text := fmt.Sprintf("查看归档 (%d)", len(s.activeTasks(true)))
			if archived {
				text = "返回研发任务"
			}
			label(c, text, 14, p.blue)
		})
		if b.Clicked() {
			s.markReadingInteraction()
			// Default page selection is not an explicit task visit.
			s.switchTaskIndexCategory(!archived)
		}
	})
}

func (s *app) taskIndexToolbar(c *ui.Context, p palette, state *taskIndexBrowsingState) {
	ui.Column(c).FillWidth().MinWidth(0).Padding(10, 8).Gap(7).Children(func() {
		ui.Row(c).FillWidth().MinWidth(0).Gap(4).AlignItems(ui.Center).Children(func() {
			search := ui.TextInput(c.Key("task-index-search-"+s.page), &state.search).
				Bind(&state.searchInput).Grow(1).MinWidth(0).Height(34).
				Placeholder("搜索任务").Label("搜索任务").Tooltip("搜索任务名称、需求编号、项目组或分支").
				Background(p.surface).Border(1, p.line).Radius(7)
			search.OnChange(func() {
				state.scroll.X, state.scroll.Y = 0, 0
				s.markReadingInteraction()
			})
			search.OnShortcut(0, ui.KeyEscape, func() { s.clearTaskIndexSearch(c, state) })
			search.OnShortcut(0, ui.KeyDown, func() { s.focusTaskIndexRow(c, state, 0) })
			search.OnShortcut(0, ui.KeyUp, func() { s.focusTaskIndexRow(c, state, len(state.visibleRows)-1) })
			search.OnSubmit(func() {
				if len(state.visibleRows) > 0 {
					index := state.visibleRows[0]
					s.openTaskIndex(index)
					if handle := state.rowHandles[taskNavigationKey(s.tasks[index])]; handle != nil {
						handle.Focus(c)
					}
				}
			})
			if state.search != "" {
				clear := ui.ButtonBase(c.Key("task-index-clear-search")).Width(26).Height(28).Shrink(0).
					Center().Radius(6).Label("清除任务搜索").Tooltip("清除搜索 (Esc)")
				if clear.Hovered() {
					clear.Background(p.side)
				}
				clear.Children(func() { icon(c, "close", 15, p.muted) })
				if clear.Clicked() {
					s.clearTaskIndexSearch(c, state)
				}
			}
		})
		if message := s.taskNavigationFor().error; message != "" {
			label(c, "任务导航偏好未保存", 11, p.danger).FillWidth().SingleLine().Tooltip(message)
		}
	})
}

func (s *app) clearTaskIndexSearch(c *ui.Context, state *taskIndexBrowsingState) {
	state.search = ""
	state.scroll.X, state.scroll.Y = 0, 0
	state.searchInput.Focus(c)
	s.markReadingInteraction()
}

func (s *app) prepareTaskIndexHandles(state *taskIndexBrowsingState, archived bool) {
	if state.rowHandles == nil {
		state.rowHandles = make(map[string]*ui.Handle)
	}
	valid := make(map[string]bool)
	for _, t := range s.tasks {
		if t.archived != archived {
			continue
		}
		key := taskNavigationKey(t)
		valid[key] = true
		if state.rowHandles[key] == nil {
			state.rowHandles[key] = &ui.Handle{}
		}
	}
	for key, handle := range state.rowHandles {
		if !valid[key] {
			handle.CancelFocus()
			delete(state.rowHandles, key)
		}
	}
}

func (s *app) focusTaskIndexRow(c *ui.Context, state *taskIndexBrowsingState, position int) {
	if position < 0 || position >= len(state.visibleRows) {
		return
	}
	key := taskNavigationKey(s.tasks[state.visibleRows[position]])
	if handle := state.rowHandles[key]; handle != nil {
		handle.Focus(c)
		state.revealTaskKey = key
	}
	s.markReadingInteraction()
}

func (s *app) taskIndexTask(c *ui.Context, p palette, state *taskIndexBrowsingState, i int) {
	t := s.tasks[i]
	key := taskNavigationKey(t)
	selected := s.selected == i
	row := ui.Row(c.Key("task-row-" + key)).FillWidth().MinWidth(0).Height(40).Radius(8).AlignItems(ui.Center)
	if selected {
		row.Background(p.selected)
	} else if row.Hovered() {
		row.Background(p.side)
	}
	row.Children(func() {
		if selected {
			ui.Box(c).Width(1).FillHeight().Background(p.blue)
		}
		button := ui.ButtonBase(c.Key("task-"+t.id)).Bind(state.rowHandles[key]).Grow(1).MinWidth(0).
			FillHeight().Padding(5, 8).Radius(7).Label(t.title).Tooltip(t.title)
		pinned := s.taskIsPinned(t)
		if pinned {
			button.Description("已置顶")
		}
		button.Children(func() {
			ui.Row(c).FillWidth().MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
				if pinned {
					ui.Icon(c, icons["pin"]).Width(16).Height(16).Shrink(0).TextColor(p.blue).
						Role(ui.RoleImage).Label("已置顶：" + t.title).Tooltip("已置顶")
				}
				weight := 400
				if selected {
					weight = 600
				}
				label(c, t.title, 14, p.text).FontWeight(weight).Grow(1).MinWidth(48).SingleLine()
				taskIndexStatus(c, s.taskRequirementStatus(t), p)
			})
		})
		if state.revealTaskKey == key {
			button.ScrollIntoView()
			state.revealTaskKey = ""
		}
		// Direction keys move focus only; Enter/Space use the button's normal
		// activation without changing the list's stable pin-first order.
		button.OnShortcut(0, ui.KeyUp, func() {
			position := slices.Index(state.visibleRows, i)
			if position == 0 {
				state.searchInput.Focus(c)
			} else {
				s.focusTaskIndexRow(c, state, position-1)
			}
		})
		button.OnShortcut(0, ui.KeyDown, func() { s.focusTaskIndexRow(c, state, slices.Index(state.visibleRows, i)+1) })
		button.OnShortcut(0, ui.KeyHome, func() { s.focusTaskIndexRow(c, state, 0) })
		button.OnShortcut(0, ui.KeyEnd, func() { s.focusTaskIndexRow(c, state, len(state.visibleRows)-1) })
		button.OnShortcut(0, ui.KeyEscape, func() { state.searchInput.Focus(c) })
		if button.Clicked() {
			s.openTaskIndex(i)
		}
	})
	row.ContextMenu(func(menu *ui.Menu) { s.taskContextMenu(c.Services(), t, menu) })
}

func taskIndexStatus(c *ui.Context, text string, p palette) {
	if text == "" {
		text = "未标记"
	}
	bg, fg := statusBadgeColors(text, p)
	ui.Box(c).Shrink(1).MinWidth(0).MaxWidth(96).Padding(2, 7).Radius(20).
		Border(1, p.line).Background(bg).Clip().Tooltip(text).Children(func() {
		label(c, text, 11, fg).MinWidth(0).SingleLine()
	})
}

func (s *app) taskRequirementStatus(t task) string {
	if strings.TrimSpace(t.requirementURL) != "" {
		state := s.taskRequirementMetadataFor(t)
		switch state.Status {
		case requirementMetadataLoading:
			return "读取中"
		case requirementMetadataFailed:
			return "读取失败"
		case requirementMetadataLoaded:
			if state.Metadata.Status != nil {
				return *state.Metadata.Status
			}
			return "未标记"
		default:
			return "未读取"
		}
	}
	return t.status
}

func (s *app) selectTaskIndex(i int) bool {
	if i != s.selected {
		if err := s.captureMaterialTextInput(); err != nil {
			s.lastError = err.Error()
			return false
		}
	}
	if i != s.selected {
		if err := s.captureMarkdownEditorInput(s.markdown); err != nil {
			s.lastError = err.Error()
			return false
		}
	}
	if i == s.selected && i >= 0 && i < len(s.tasks) {
		s.markReadingInteraction()
		s.taskListFocused = false
		s.requestWorkspaceGitStatus(true)
		return true
	}
	s.clearWorkspaceCommandSelection()
	if i != s.selected {
		s.dismissWorkspaceHistory()
	}
	s.clearWorkspaceMergeSelection()
	s.markReadingInteraction()
	s.beforeReadingTaskSelection()
	s.deactivateInstructionEditor()
	s.cancelWorkspaceGitStatus()
	s.cancelWorkspaceGitFlow()
	s.cancelMaterialsSearch()
	s.dismissMarkdownBitmapViewer()
	s.selected = i
	s.taskListFocused = false
	s.closeNativeMedia()
	s.closeMarkdownEditor()
	s.documents = nil
	s.documentsLoaded = false
	s.documentsRequested = false
	s.documentTabs = nil
	s.documentReadPending = false
	s.documentDrafts = make(map[string]string)
	s.documentDirtyMap = make(map[string]bool)
	s.documentPath, s.documentContent = "", ""
	s.documentHash = ""
	s.documentDirty, s.documentEditing = false, false
	s.afterReadingTaskSelection()
	s.requestWorkspaceGitStatus(true)
	return true
}
