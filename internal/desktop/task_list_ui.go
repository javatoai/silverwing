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
	sections := s.taskIndexSections(archived, rows)
	state.visibleRows = state.visibleRows[:0]
	for _, section := range sections {
		if section.groupID == "" || !state.collapsedGroups[section.groupID] {
			state.visibleRows = append(state.visibleRows, section.rows...)
		}
	}
	s.prepareTaskIndexHandles(state, archived)
	ui.Column(c).Fill().MinWidth(0).Background(p.soft).Children(func() {
		s.taskIndexToolbar(c, p, state)
		ui.Divider(c)
		ui.Scroll(c.Key("task-index-" + s.page)).TrackScroll(&state.scroll).Grow(1).MinHeight(0).Padding(8).Gap(4).Children(func() {
			for _, section := range sections {
				if section.groupID != "" {
					s.taskIndexGroupHeader(c, p, state, section.groupID, section.title, len(section.rows))
					if state.collapsedGroups[section.groupID] {
						continue
					}
				} else if section.title != "" {
					ui.Row(c).FillWidth().Height(27).Padding(0, 8).Gap(5).AlignItems(ui.Center).Children(func() {
						if section.title == "置顶" {
							icon(c, "pin", 13, p.blue)
						}
						label(c, section.title, 11, p.muted).Grow(1).SingleLine()
						label(c, fmt.Sprint(len(section.rows)), 10, p.muted)
					})
				}
				for _, index := range section.rows {
					s.taskIndexTask(c, p, state, index)
				}
			}
			if len(rows) == 0 {
				message := "还没有研发任务\n请在需求列表中创建任务。"
				switch {
				case strings.TrimSpace(state.search) != "":
					message = "没有匹配的任务\n试试其他关键词，或清除搜索。"
				case state.recentOnly:
					message = "暂无最近访问的任务\n打开任务后会显示在这里。"
				case archived:
					message = "还没有已归档任务。"
				}
				ui.Column(c).FillWidth().Padding(24, 12).Gap(8).Center().Children(func() {
					label(c, message, 12, p.muted).FillWidth().Wrap()
					if state.recentOnly && summaryTextButton(c, "task-index-show-all", "查看全部任务", p.blue, false) {
						state.recentOnly = false
						state.scroll.Y = 0
						s.markReadingInteraction()
					}
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
		ui.Row(c).FillWidth().MinWidth(0).Gap(4).Children(func() {
			for _, option := range []struct {
				recent bool
				label  string
			}{{false, "全部"}, {true, "最近访问"}} {
				active := state.recentOnly == option.recent
				button := ui.ButtonBase(c.Key("task-index-mode-" + option.label)).Grow(1).MinWidth(0).Height(28).
					Center().Radius(6).Label(option.label)
				color := p.muted
				if active {
					button.Background(p.selected)
					color = p.blue
				} else if button.Hovered() {
					button.Background(p.side)
				}
				if option.recent {
					button.Tooltip("按打开顺序显示最近 20 个任务中的当前分类")
				}
				button.Children(func() { label(c, option.label, 12, color).SingleLine() })
				if button.Clicked() && !active {
					state.recentOnly = option.recent
					state.scroll.X, state.scroll.Y = 0, 0
					s.markReadingInteraction()
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

func (s *app) taskIndexGroupHeader(c *ui.Context, p palette, state *taskIndexBrowsingState, groupID, name string, count int) {
	button := ui.ButtonBase(c.Key("task-group-"+groupID)).Row().FillWidth().Height(31).Padding(5, 8).Gap(7).Radius(7).Label(name)
	if button.Hovered() {
		button.Background(p.side)
	}
	button.Children(func() {
		marker := "▾"
		if state.collapsedGroups[groupID] {
			marker = "›"
		}
		label(c, marker, 13, p.muted).Width(12)
		label(c, name, 12, p.text).FontWeight(600).Grow(1).SingleLine()
		label(c, fmt.Sprint(count), 10, p.muted)
	})
	if button.Clicked() {
		state.collapsedGroups[groupID] = !state.collapsedGroups[groupID]
		s.markReadingInteraction()
	}
}

func (s *app) taskIndexTask(c *ui.Context, p palette, state *taskIndexBrowsingState, i int) {
	t := s.tasks[i]
	key := taskNavigationKey(t)
	pinned := s.taskIsPinned(t)
	selected := s.selected == i
	row := ui.Row(c.Key("task-row-" + key)).FillWidth().MinWidth(0).Height(52).Radius(8).AlignItems(ui.Center)
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
		button.Children(func() {
			ui.Column(c).FillWidth().MinWidth(0).Gap(2).Children(func() {
				weight := 400
				if selected {
					weight = 600
				}
				label(c, t.title, 14, p.text).FontWeight(weight).FillWidth().SingleLine()
				ui.Row(c).FillWidth().MinWidth(0).Gap(5).AlignItems(ui.Center).Children(func() {
					context := firstNonEmpty(s.taskGroupName(t), t.requirementID)
					label(c, context, 10, p.muted).Grow(1).MinWidth(0).SingleLine().Tooltip(context)
					taskIndexStatus(c, s.taskRequirementStatus(t), p)
				})
			})
		})
		if state.revealTaskKey == key {
			button.ScrollIntoView()
			state.revealTaskKey = ""
		}
		// Direction keys move focus only; Enter/Space use the button's normal
		// activation. This keeps recent-order navigation stable until opening.
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
		pinLabel, pinColor := "置顶任务："+t.title, p.muted
		if pinned {
			pinLabel, pinColor = "取消置顶："+t.title, p.blue
		}
		pin := ui.ButtonBase(c.Key("task-pin-"+key)).Width(28).Height(30).Shrink(0).Margin(0, 3).
			Center().Radius(6).Label(pinLabel).Tooltip(pinLabel).Disabled(t.id == "")
		if pin.Hovered() {
			pin.Background(p.surface)
		}
		pin.Children(func() { icon(c, "pin", 15, pinColor) })
		if pin.Clicked() {
			s.markReadingInteraction()
			s.setTaskPinned(t, !pinned)
		}
	})
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
