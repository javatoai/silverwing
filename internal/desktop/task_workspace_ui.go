package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
)

func (s *app) taskWorkspace(c *ui.Context, p palette) {
	s.ensureTaskRequirementMetadata()
	w, _ := c.Size()
	// Match Source: keep both panes visible at every width. The 960dp
	// focused-pane helper is unused in Source; only split-pane width is clamped.
	ui.Column(c).Grow(1).MinHeight(0).Padding(8).Children(func() {
		s.taskListPaneSplit(c.Key("task-split"), w-16, p, func() { s.taskIndex(c, p) }, func() { s.taskWorkspaceDetail(c, p) })
	})
}

func (s *app) taskWorkspaceDetail(c *ui.Context, p palette) {
	ui.Column(c).Fill().MinWidth(0).Padding(0, 0, 0, 7).Children(func() {
		ui.Row(c).FillWidth().Height(51).Shrink(0).Padding(8).Gap(2).Label("任务内容页签").Children(func() {
			for _, entry := range []struct {
				id   int
				name string
			}{{1, "任务资料"}, {2, "需求详情"}, {3, "需求说明"}} {
				b := ui.ButtonBase(c.Key(fmt.Sprintf("tab-%d", entry.id))).Height(35).Padding(0, 10).Radius(10).Label(entry.name)
				fg := p.muted
				if s.tab == entry.id {
					fg = p.blue
					b.Background(p.selected)
				} else if b.Hovered() {
					b.Background(p.soft)
				}
				b.Children(func() { label(c, entry.name, 14, fg).NoWrap() })
				if b.Clicked() {
					s.markReadingInteraction()
					if s.tab == 3 && entry.id != 3 {
						s.deactivateInstructionEditor()
					}
					s.tab = entry.id
					if s.tab == 1 {
						s.loadDocuments()
					}
				}
			}
			ui.Spacer(c)
			t := s.taskUnit.currentTask()
			if t.id != "" && s.taskOpenToolEnabled("codex") && iconButtonDisabled(c, "qa-task-codex", "launch", "在 Codex 中打开", p, s.busy || s.backend == nil || !s.taskOpenToolAvailable("codex")).Clicked() {
				s.openTaskInCodex(t)
			}
		})
		ui.Divider(c)
		if s.taskUnit.currentTask().id == "" {
			s.emptyTask(c, p)
			return
		}
		switch s.tab {
		case 2:
			s.requirementDetails(c, p)
		case 3:
			s.renderTaskNotes(c, p)
		default:
			s.materials(c, p)
		}
	})
}

func (s *app) emptyTask(c *ui.Context, p palette) {
	archived := s.page == "archive"
	ui.Column(c).Grow(1).FillWidth().Center().Gap(12).Padding(24).Background(p.surface).Border(1, p.line).Radius(18).Children(func() {
		icon(c, map[bool]string{true: "archive", false: "tasks"}[archived], 28, p.muted)
		title := "还没有任务"
		if archived {
			title = "没有归档任务"
		}
		label(c, title, 18, p.text).FontWeight(600)
		message := "请在需求列表中创建任务。"
		if archived {
			message = "已归档的任务会保留在这里，可随时恢复。"
		}
		label(c, message, 13, p.muted)
	})
}

func (s *app) taskHeader(c *ui.Context, p palette) {
	t := s.taskUnit.currentTask()
	ui.Column(c).FillWidth().Gap(6).Padding(2).Children(func() {
		if t.requirementURL != "" {
			s.renderTaskRequirementMetadata(c, p, t)
			ui.Row(c).FillWidth().MinWidth(0).Gap(5).AlignItems(ui.Center).Children(func() {
				s.renderRequirementURLLink(c, p, t.id, t.requirementURL, 12)
				if s.configBool("showTaskAreaCopyIcons", true) && s.configBool("showTaskAreaRequirementCopyIcons", true) {
					if iconButton(c, "copy-task-detail-requirement-url", "copy", "复制需求链接", p).Shrink(0).Clicked() {
						s.copyTextToClipboard(c.Services(), t.requirementURL, "已复制需求链接")
					}
				}
			})
		}
	})
}

func (s *app) copyTaskWorkspacePath(services ui.Services) {
	s.copyTextToClipboard(services, s.taskUnit.currentTask().path, "已复制任务路径")
}
