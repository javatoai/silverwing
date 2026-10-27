package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

func (s *app) sidebar(c *ui.Context, p palette, compact bool) {
	width := float32(128)
	if compact {
		width = 72
	}
	ui.Column(c).Width(width).Shrink(0).Padding(16, 6).Gap(4).Background(p.surface).Border(1, p.line).Radius(0, 16, 16, 0).Children(func() {
		if !compact {
			label(c, "工作空间", 11, p.muted).Padding(5, 6)
		}
		items := s.sidebarNavigationItems()
		groupStart := "settings"
		for _, item := range items {
			if item.page != "tasks" && item.page != "requirements" {
				groupStart = item.page
				break
			}
		}
		for _, n := range items {
			if n.page == groupStart {
				ui.Spacer(c)
			}
			active := s.page == n.page || n.page == "tasks" && (s.page == "archive" || s.page == "materials")
			b := ui.ButtonBase(c.Key("nav-"+n.page)).Row().FillWidth().Height(44).Padding(0, 5).Gap(6).Radius(13).Label(n.name).Justify(ui.Start)
			fg := p.text
			if active {
				b.Background(p.selected)
				fg = p.blue
			} else if b.Hovered() {
				b.Background(p.soft)
			}
			if compact {
				b.Height(48).Width(48).AlignSelf(ui.Center).Center().Tooltip(n.name)
			}
			b.Children(func() {
				if !compact {
					if active {
						ui.Box(c).Width(1).Height(24).Radius(3).Background(p.blue)
					} else {
						ui.Box(c).Width(1)
					}
				}
				icon(c, n.image, 20, fg)
				if !compact {
					weight := 400
					if active {
						weight = 600
					}
					label(c, n.name, 14, fg).FontWeight(weight).NoWrap().Grow(1)
					if badge, ok := s.sidebarBadge(n.page, false); ok {
						ui.Box(c).Padding(2, 4).Radius(20).Background(p.soft).Tooltip(sidebarBadgeDescription(n.page, badge)).Children(func() { label(c, badge, 11, p.muted).NoWrap() })
					}
				} else if badge, ok := s.sidebarBadge(n.page, true); ok {
					badgeColor := p.soft
					textColor := p.muted
					if active {
						badgeColor = p.blue
						textColor = p.surface
					}
					ui.Box(c).Absolute().Top(1).Right(1).Padding(2, 4).Radius(20).Background(badgeColor).Disabled(true).Children(func() {
						label(c, badge, 10, textColor).NoWrap()
					})
				}
			})
			if b.Clicked() {
				s.markReadingInteraction()
				if n.page == "tasks" && (s.page == "archive" || s.currentTask().archived) {
					s.switchTaskIndexCategory(false)
					continue
				}
				if s.tab == 3 {
					s.deactivateInstructionEditor()
				}
				s.page = n.page
				if n.page == "production" {

				}
				if n.page == "requirements" {
					s.lastResult = ""
				}
			}
		}
	})
}

func sidebarBadgeDescription(page, badge string) string {
	if page == "requirements" {
		if badge == "—" {
			return "所选 Sprint 我的估时总计：暂不可用"
		}
		return "所选 Sprint 我的估时总计：" + badge + " 天"
	}
	return badge + " 项"
}

func (s *app) sidebarBadge(page string, compact bool) (string, bool) {
	switch page {
	case "tasks":
		count := len(s.activeTasks(false))
		return fmt.Sprint(count), !compact || count > 0
	case "requirements":
		return s.requirementsEstimateBadge(), true
	default:
		return "", false
	}
}
