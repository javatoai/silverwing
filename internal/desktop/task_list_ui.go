package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (s *app) taskIndex(c *ui.Context, p palette) {
	s.observeTaskOpenTools()
	archived := s.page == "archive"
	state := s.taskIndexBrowsingFor(archived)
	rows := s.taskIndexRows(archived)
	state.visibleRows = rows
	s.prepareTaskIndexHandles(state, archived)
	ui.Column(c).Fill().MinWidth(0).Background(p.soft).Children(func() {
		s.taskIndexToolbar(c, p, state)
		ui.Divider(c)
		if len(rows) > 0 {
			catalog := s.taskUnit.taskCatalog()
			state.list.Key = func(position int) any { return catalog.keys[rows[position]] }
			if position, found := state.projection.keyPositions[state.revealTaskKey]; state.revealTaskKey != "" && found {
				state.list.ScrollIntoView(position)
			}
			ui.List(c.Key("task-index-"+s.page), &state.list, len(rows)).TrackScroll(&state.scroll).Grow(1).MinHeight(0).Padding(8).Gap(4).Rows(func(row ui.ListRow) {
				s.taskIndexTask(row.Context, p, state, rows[row.Index])
			})
		} else {
			ui.Scroll(c.Key("task-index-empty-" + s.page)).Grow(1).MinHeight(0).Padding(8).Children(func() {
				message := "还没有任务\n点击上方“新建任务”开始，也可从需求列表创建。"
				switch {
				case strings.TrimSpace(state.search) != "":
					message = "没有匹配的任务\n试试其他关键词，或清除搜索。"
				case archived:
					message = "还没有已归档任务。"
				}
				ui.Column(c).FillWidth().Padding(24, 12).Gap(8).Center().Children(func() {
					label(c, message, 12, p.muted).FillWidth().Wrap()
				})
			})
		}
		ui.Divider(c)
		b := ui.ButtonBase(c.Key("archive-link")).Height(48).Row().Gap(6).Center().Label("查看归档")
		b.Children(func() {
			icon(c, "archive", 18, p.blue)
			text := fmt.Sprintf("查看归档 (%d)", len(s.taskUnit.activeTasks(true)))
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
					s.focusTaskIndexRow(c, state, 0)
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
			if s.page == "tasks" && iconButtonDisabled(c, "task-index-create", "plus", "新建任务", p, s.busy || s.backend == nil || s.dialogOpen).
				Width(30).Height(34).Shrink(0).FocusRing(true).Tooltip("新建任务，需求和服务都可以不选").Clicked() {
				s.openTaskCreateForm("新建任务", "", "")
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
	catalog := s.taskUnit.taskCatalog()
	if state.rowHandles == nil {
		state.rowHandles = make(map[string]*ui.Handle)
	}
	if state.handlesValid && state.handlesRevision == s.taskUnit.state.tasksRevision {
		return
	}
	state.handlesValid, state.handlesRevision = true, s.taskUnit.state.tasksRevision
	valid := make(map[string]bool)
	for _, index := range s.taskUnit.activeTasks(archived) {
		key := catalog.keys[index]
		valid[key] = true
	}
	for key, handle := range state.rowHandles {
		if !valid[key] {
			handle.CancelFocus()
			delete(state.rowHandles, key)
		}
	}
	if !valid[state.pendingFocusKey] {
		state.pendingFocusKey = ""
	}
	if !valid[state.revealTaskKey] {
		state.revealTaskKey = ""
	}
}

func (s *app) focusTaskIndexRow(c *ui.Context, state *taskIndexBrowsingState, position int) {
	if position < 0 || position >= len(state.visibleRows) {
		return
	}
	key := s.taskUnit.taskCatalog().keys[state.visibleRows[position]]
	if state.rowHandles[key] == nil {
		state.rowHandles[key] = &ui.Handle{}
	}
	state.rowHandles[key].Focus(c)
	state.revealTaskKey, state.pendingFocusKey = key, key
	state.list.ScrollIntoView(position)
	s.markReadingInteraction()
}

func (s *app) taskIndexTask(c *ui.Context, p palette, state *taskIndexBrowsingState, i int) {
	t := s.taskUnit.state.tasks[i]
	key := s.taskUnit.taskCatalog().keys[i]
	if state.rowHandles[key] == nil {
		state.rowHandles[key] = &ui.Handle{}
	}
	selected := s.taskUnit.state.selected == i
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
		button := ui.ButtonBase(c.Key("task-"+key)).Bind(state.rowHandles[key]).Grow(1).MinWidth(0).
			FillHeight().Padding(5, 8).Radius(7).Label(t.title).Tooltip(t.title)
		pinned := s.taskNavigationFor().pinned[key]
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
		if state.pendingFocusKey == key {
			button.Focus()
			state.pendingFocusKey = ""
		}
		// Direction keys move focus only; Enter/Space use the button's normal
		// activation without changing the list's stable pin-first order.
		button.OnShortcut(0, ui.KeyUp, func() {
			position := state.projection.positions[i]
			if position == 0 {
				state.searchInput.Focus(c)
			} else {
				s.focusTaskIndexRow(c, state, position-1)
			}
		})
		button.OnShortcut(0, ui.KeyDown, func() { s.focusTaskIndexRow(c, state, state.projection.positions[i]+1) })
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
	transition := newTaskTransition(s.taskUnit.currentTask(), taskLifecycleSelectionAt(s.taskUnit.state.tasks, i), taskTransitionUser, i != s.taskUnit.state.selected)
	if !transition.indexChanged && i >= 0 && i < len(s.taskUnit.state.tasks) {
		s.markReadingInteraction()
		s.taskUnit.selectIndex(i)
		s.refreshWorkspaceTaskSelection(true)
		return true
	}
	if err := s.prepareFeatureTaskSelection(transition); err != nil {
		s.lastError = err.Error()
		return false
	}
	s.taskUnit.selectIndex(i)
	s.materialsUnit.resetTaskPresentation(transition, s)
	s.afterReadingTaskSelection()
	s.refreshWorkspaceTaskSelection(true)
	return true
}
