package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

func (s *app) renderTaskTerminals(c *ui.Context, p palette) {
	s.drainTaskTerminalEvents()
	owner := s.ensureTaskTerminalsCurrent()
	if owner == nil {
		label(c, "当前任务终端不可用，请重新选择任务。", 13, p.muted).Padding(18)
		return
	}
	ui.Column(c.Key("task-terminal:" + owner.identity.id + ":" + owner.identity.createdAt)).Fill().MinWidth(0).MinHeight(0).Background(p.surface).Children(func() {
		ui.Column(c.Key("console")).Grow(1).FillWidth().MinHeight(0).Children(func() {
			var active *taskTerminalSession
			for _, session := range owner.sessions {
				if session.id == owner.selected {
					active = session
					break
				}
			}
			if active == nil {
				ui.Column(c).Fill().Center().Gap(12).Children(func() {
					icon(c, "terminal", 28, p.muted)
					if ui.Button(c.Key("new-empty-terminal"), "新建终端").Disabled(s.taskTerminalUnit.deleting[owner.identity.id]).Clicked() {
						s.addTaskTerminal(owner)
						c.AnimationFrame()
					}
				})
			} else if active.loading {
				ui.Column(c).Fill().Center().Gap(10).Children(func() {
					ui.Spinner(c).Size(20, 20).TextColor(p.blue).Label("正在启动终端").Tooltip("正在启动终端")
				})
			} else if active.error != "" {
				ui.Column(c).Fill().Center().Padding(24).Gap(10).Children(func() {
					label(c, active.error, 13, p.danger).Wrap()
					if ui.Button(c.Key("retry-terminal"), "重试").Clicked() {
						s.startTaskTerminal(owner, active)
						owner.focus = true
						c.AnimationFrame()
					}
				})
			} else if active.process != nil {
				console := active.process.View(c.Key(fmt.Sprintf("session-%d-%d", active.id, active.generation))).Fill().MinWidth(0).MinHeight(0)
				if owner.focus {
					console.Focus()
					owner.focus = false
				}
			}
		})
		ui.Divider(c)
		ui.Row(c.Key("terminal-tab-bar")).FillWidth().Height(36).Shrink(0).Gap(4).Padding(2, 6).AlignItems(ui.Center).Background(p.side).Children(func() {
			ui.ScrollHorizontal(c.Key("terminal-tabs")).Grow(1).MinWidth(0).Height(32).TrackScroll(&owner.scroll).Children(func() {
				ui.Row(c).Height(32).Gap(4).Children(func() {
					for _, session := range append([]*taskTerminalSession(nil), owner.sessions...) {
						s.renderTaskTerminalTab(c.Key(fmt.Sprintf("terminal-tab-%d", session.id)), p, owner, session)
					}
				})
			})
			if iconButton(c, "add-terminal", "plus", "新建终端", p).Size(28, 28).Disabled(s.taskTerminalUnit.deleting[owner.identity.id]).Clicked() {
				s.addTaskTerminal(owner)
				c.AnimationFrame()
			}
		})
	})
}

func (s *app) renderTaskTerminalTab(c *ui.Context, p palette, owner *taskTerminalOwner, session *taskTerminalSession) {
	title := fmt.Sprintf("终端 %d", session.number)
	tip := title + "\n" + session.directory
	if session.exited {
		tip += fmt.Sprintf("\n退出码：%d", session.exitCode)
	} else if session.error != "" {
		tip += "\n" + session.error
	}
	if session.failedStart() && session.error == "" {
		tip += "\n终端启动失败，可重试"
	}
	ui.Row(c).Height(30).Gap(2).Padding(0, 4).Radius(6).Background(p.surface).Border(1, p.line).Children(func() {
		button := ui.ButtonBase(c.Key("select")).Height(28).Padding(0, 7).Center().Radius(5).Label(fmt.Sprintf("切换到终端 %d", session.number)).Tooltip(tip)
		fg := p.text
		if owner.selected == session.id {
			button.Background(p.selected)
			fg = p.blue
		} else if button.Hovered() {
			button.Background(p.soft)
		}
		button.Children(func() {
			ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
				if session.loading {
					ui.Spinner(c).Size(11, 11).TextColor(p.muted).Label("正在启动")
				} else if session.failedStart() {
					ui.Box(c).Size(6, 6).Radius(3).Background(p.danger).Label("启动失败").Tooltip(firstNonEmpty(session.error, "终端进程启动失败，请重试"))
				} else if session.exited {
					ui.Box(c).Size(6, 6).Radius(3).Background(p.muted).Label("已退出").Tooltip(fmt.Sprintf("已退出，退出码：%d", session.exitCode))
				}
				label(c, title, 12, fg).NoWrap()
			})
		})
		if button.Clicked() {
			owner.selected, owner.focus = session.id, true
			c.AnimationFrame()
		}
		if session.exited && session.exitCode == -1 {
			if iconButton(c, "retry-start", "refresh", "重试启动终端", p).Size(22, 24).Clicked() {
				s.startTaskTerminal(owner, session)
				owner.focus = true
				c.AnimationFrame()
			}
		}
		if iconButton(c, "close", "close", fmt.Sprintf("关闭终端 %d", session.number), p).Size(22, 24).Clicked() {
			s.closeTaskTerminal(owner, session.id)
			c.AnimationFrame()
		}
	})
}
