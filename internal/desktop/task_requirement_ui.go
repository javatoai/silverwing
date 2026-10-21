package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (s *app) requirementDetails(c *ui.Context, p palette) {
	t := s.currentTask()
	reader := s.requirementReader(t.id, t.requirementURL)
	ui.Column(c).FillWidth().Grow(1).MinHeight(0).Padding(16).Gap(10).Background(p.surface).Children(func() {
		ui.Row(c).FillWidth().MinWidth(0).AlignItems(ui.Center).Children(func() {
			title := s.taskRequirementDetailTitle(t)
			label(c, title, 14, p.text).FontWeight(400).Grow(1).MinWidth(0).Wrap().Tooltip(title)
			if t.requirementURL != "" {
				s.renderRequirementReaderActions(c, p, reader, func(menu *overflowMenuBuilder) {
					if menu.Item("refresh-requirement", s.requirementRefreshLabel(t.requirementURL)).Icon("refresh").Disabled(s.requirementRefreshing(t.requirementURL)).Chosen() {
						s.startRequirementRefresh(t.requirementURL)
					}
				})
			}
		})
		s.renderTaskRequirementParticipants(c, p, t)
		if t.requirementURL != "" {
			ui.Row(c).FillWidth().Gap(5).AlignItems(ui.Center).Children(func() {
				s.renderRequirementURLLink(c, p, t.id, t.requirementURL, 12)
				if iconButton(c, "copy-task-requirement-url", "copy", "复制需求链接", p).Clicked() {
					s.copyTextToClipboard(c.Services(), t.requirementURL, "已复制需求链接")
				}
			})
		}
		ui.Divider(c)
		s.renderRequirementContent(c, p, t.id, t.requirementURL)
	})
}

func (s *app) taskRequirementDetailTitle(item task) string {
	if strings.TrimSpace(item.requirementURL) == "" {
		return "需求详情"
	}
	state := s.taskRequirementMetadataFor(item)
	if state.Metadata.Title != nil {
		if title := strings.TrimSpace(*state.Metadata.Title); title != "" {
			return title
		}
	}
	if requirement, ok := s.requirementForURL(item.requirementURL); ok {
		if title := strings.TrimSpace(requirement.Title); title != "" {
			return title
		}
		if title := strings.TrimSpace(requirement.WorkItem.Title); title != "" {
			return title
		}
	}
	if title := strings.TrimSpace(item.requirementTitle); title != "" {
		return title
	}
	switch state.Status {
	case requirementMetadataLoading:
		return "获取需求信息中"
	case requirementMetadataFailed, requirementMetadataLoaded:
		return "未获取需求名称"
	}
	return "需求详情"
}

func (s *app) requirementDescription(c *ui.Context, p palette) {
	selected := s.noteTab
	if selected < 0 || selected >= len(s.notes) {
		selected = 0
		s.noteTab = 0
	}
	name := []string{"AGENTS.md", "TASK-CONTEXT.md", "TASK-RULES.md"}[selected]
	ui.Column(c).Grow(1).MinHeight(0).Padding(18).Gap(8).Background(p.surface).Children(func() {
		ui.Row(c).Height(45).Gap(4).Children(func() {
			for i, tab := range []string{"AGENTS.md", "TASK-CONTEXT.md", "TASK-RULES.md"} {
				b := ui.ButtonBase(c.Key("note-"+tab)).Height(45).Padding(0, 20).Label(tab)
				if selected == i {
					b.Border(3, p.blue).BorderWidth(0, 0, 3, 0)
				}
				b.Children(func() { label(c, tab, 14, p.blue).NoWrap() })
				if b.Clicked() {
					s.noteTab = i
					s.noteEditing = false
					s.loadInstructions()
				}
			}
		})
		ui.Divider(c)
		path := s.notePaths[selected]
		if path == "" {
			path = name
		}
		label(c, path, 11, p.muted).Font("Consolas").Padding(0, 18)
		if s.noteEditing {
			ui.TextArea(c.Key(fmt.Sprintf("notes-editor-%d", selected)), &s.notes[selected]).Grow(1).FillWidth().MinHeight(140).Padding(14).FontSize(14).Background(p.surface).Border(1, p.line).Radius(7)
		} else {
			ui.Row(c).Gap(2).Children(func() {
				if iconButton(c, "notes-find", "search", "在说明中查找", p).Clicked() {
					s.openForm("document.read", "查找任务说明", s.currentTask().id, "", "", path, formField{key: "query", label: "查找文本"})
				}
				ui.Spacer(c)
				s.overflowMenu(c, p, "notes-menu", "更多说明操作", false, func(m *overflowMenuBuilder) {
					if m.Item("copy-instructions", "复制说明").Icon("copy").Chosen() {
						s.copyTextToClipboard(c.Services(), s.notes[selected], "已复制任务说明")
					}
					if m.Item("open-instructions", "打开说明文件").Icon("launch").Disabled(s.systemOpenBusy()).Chosen() {
						s.act("system.open", s.currentTask().id, "", "", path, "", nil)
					}
					if m.Item("reveal-instructions", "显示文件位置").Icon("folder").Disabled(s.systemRevealBusy()).Chosen() {
						s.act("system.reveal", s.currentTask().id, "", "", path, "", nil)
					}
				})
			})
			ui.Scroll(c).Grow(1).MinHeight(0).Padding(12, 18).Children(func() {
				if strings.TrimSpace(s.notes[selected]) == "" {
					label(c, "选择“编辑”填写任务说明，保存后会写入任务工作区。", 13, p.muted)
					return
				}
				for _, line := range strings.Split(s.notes[selected], "\n") {
					line = strings.TrimSpace(line)
					switch {
					case line == "":
						ui.Box(c).Height(6)
					case strings.HasPrefix(line, "# "):
						label(c, strings.TrimPrefix(line, "# "), 24, p.text).Bold()
					case strings.HasPrefix(line, "## "):
						label(c, strings.TrimPrefix(line, "## "), 18, p.text).Bold()
					case strings.HasPrefix(line, "- "):
						ui.Row(c).Gap(8).Children(func() {
							ui.Box(c).Width(4).Height(4).Radius(2).Background(p.text)
							label(c, strings.TrimPrefix(line, "- "), 14, p.text).LineHeight(1.7)
						})
					default:
						label(c, line, 14, p.text).LineHeight(1.7)
					}
				}
			})
		}
		ui.Divider(c)
		ui.Row(c).Height(44).Gap(8).Children(func() {
			if s.noteHashes[selected] != "" {
				label(c, "已读取", 12, p.muted)
			}
			ui.Spacer(c)
			for _, mode := range []struct {
				title   string
				editing bool
			}{{"阅读", false}, {"编辑", true}} {
				b := ui.ButtonBase(c.Key("note-mode-"+mode.title)).Height(32).Padding(0, 16).Radius(16).Label(mode.title)
				if s.noteEditing == mode.editing {
					b.Background(p.soft)
				} else {
					b.Border(1, p.line)
				}
				b.Children(func() { label(c, mode.title, 14, p.text) })
				if b.Clicked() {
					s.noteEditing = mode.editing
				}
			}
			if iconButton(c, "note-refresh", "refresh", "重新读取说明", p).Clicked() {
				s.loadInstructions()
			}
			if action(c, "note-save", "save", "保存", p, true) {
				s.act("instructions.save", s.currentTask().id, "", "", path, s.notes[selected], map[string]any{"expectedHash": s.noteHashes[selected]})
			}
		})
	})
}

func (s *app) loadInstructions() {
	t := s.currentTask()
	if t.id == "" {
		return
	}
	name := []string{"AGENTS.md", "TASK-CONTEXT.md", "TASK-RULES.md"}[s.noteTab]
	s.act("instructions.read", t.id, "", "", name, "", nil)
}
