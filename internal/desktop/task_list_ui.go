package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
	"strings"
)

func (s *app) taskIndex(c *ui.Context, p palette) {
	archived := s.page == "archive"
	state := s.taskIndexBrowsingFor(archived)
	ui.Column(c).Fill().Background(p.soft).Children(func() {
		ui.Scroll(c.Key("task-index-" + s.page)).TrackScroll(&state.scroll).Grow(1).MinHeight(0).Padding(8).Gap(4).Children(func() {
			rows := s.taskIndexRows(archived)
			if len(s.snapshot.Groups) <= 1 {
				for _, i := range rows {
					s.taskIndexTask(c, p, i)
				}
			} else {
				for _, group := range s.snapshot.Groups {
					groupRows := make([]int, 0)
					for _, i := range rows {
						if s.tasks[i].groupID == group.ID {
							groupRows = append(groupRows, i)
						}
					}
					s.taskIndexGroupHeader(c, p, state, group.ID, firstNonEmpty(group.Name, group.ID), len(groupRows))
					if !state.collapsedGroups[group.ID] {
						for _, i := range groupRows {
							s.taskIndexTask(c, p, i)
						}
					}
				}
			}
			if len(rows) == 0 {
				message := "还没有研发任务\n请在需求列表中创建任务。"
				if archived {
					message = "还没有已归档任务。"
				}
				ui.Column(c).FillWidth().Padding(24, 12).Gap(4).Center().Children(func() {
					label(c, message, 12, p.muted).Wrap()
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
			if archived {
				s.page = "tasks"
				if indices := s.activeTasks(false); len(indices) > 0 {
					s.selectTaskIndex(indices[0])
				} else {
					s.selectTaskIndex(-1)
				}
			} else {
				s.page = "archive"
				if indices := s.activeTasks(true); len(indices) > 0 {
					s.selectTaskIndex(indices[0])
				} else {
					s.selectTaskIndex(-1)
				}
			}
		}
	})
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
	}
}

func (s *app) taskIndexTask(c *ui.Context, p palette, i int) {
	t := s.tasks[i]
	b := ui.ButtonBase(c.Key("task-" + t.id)).Row().FillWidth().Height(40).Radius(8).Label(t.title)
	selected := s.selected == i
	if selected {
		b.Background(p.selected)
	} else {
		b.Background(p.soft)
		if b.Hovered() {
			b.Background(p.side)
		}
	}
	b.Children(func() {
		if selected {
			ui.Box(c).Width(1).FillHeight().Background(p.blue)
		}
		ui.Row(c).Grow(1).MinWidth(0).Padding(0, 12).Gap(5).Children(func() {
			weight := 400
			if selected {
				weight = 600
			}
			ui.Box(c).Grow(1).MinWidth(0).Clip().Children(func() {
				label(c, t.title, 14, p.text).FontWeight(weight).FillWidth().SingleLine()
			})
			compactState(c, s.taskRequirementStatus(t), p)
		})
	})
	if b.Clicked() {
		s.selectTaskIndex(i)
	}
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

func (s *app) selectTaskIndex(i int) {
	if i != s.selected {
		if err := s.captureMaterialTextInput(); err != nil {
			s.lastError = err.Error()
			return
		}
	}
	if i != s.selected {
		if err := s.captureMarkdownEditorInput(s.markdown); err != nil {
			s.lastError = err.Error()
			return
		}
	}
	if i == s.selected && i >= 0 && i < len(s.tasks) {
		s.markReadingInteraction()
		s.taskListFocused = false
		s.requestWorkspaceGitStatus(true)
		return
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
}

func (s *app) taskIndexRows(archived bool) []int {
	return s.activeTasks(archived)
}
