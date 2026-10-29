package desktop

import (
	"fmt"

	"silverwing-go/internal/application"

	"github.com/egoist/mygo/ui"
)

func (s *app) renderRequirementListProgressDialog(c *ui.Context, p palette) {
	s.observeRequirementListProgress()
	state := s.requirementUnit.state.requirementListProgress
	if !s.requirementListProgressCurrent(state) || !state.open || s.page != "requirements" {
		return
	}
	dialog := ui.DialogBase(c.Key("requirement-list-progress-dialog"), &state.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .38))
		width, height := c.Size()
		panel.Width(max(float32(1), min(float32(600), width-32))).MaxHeight(height-32).
			Padding(20).Gap(16).Background(p.surface).Border(1, p.line).Radius(16).Label("需求刷新进度弹窗")
		panel.Children(func() {
			ui.Column(c).FillWidth().MinWidth(0).Gap(6).Shrink(0).Children(func() {
				label(c, "刷新需求", 18, p.text).FontWeight(600)
				label(c, state.sprintTitle, 12, p.muted).FillWidth().Wrap()
			})
			ui.Scroll(c.Key("requirement-list-progress-steps")).TrackScroll(&state.scroll).FillWidth().MinWidth(0).MinHeight(0).
				MaxHeight(max(float32(80), height-220)).Gap(12).Label("需求刷新阶段").Children(func() {
				for _, phase := range []struct{ key, label string }{
					{"account", "核对账号"}, {"sprints", "读取 Sprint"}, {"items", "查询工作项"}, {"estimates", "读取个人估分"}, {"apply", "更新列表"},
				} {
					event := state.steps[phase.key]
					s.renderRequirementListProgressRow(c, p, phase.label, event)
					if phase.key == "items" {
						ui.Column(c).FillWidth().MinWidth(0).Padding(0, 16).Gap(6).Children(func() {
							for _, typ := range []struct{ key, label string }{{"userstory", "需求"}, {"technical", "技术改进"}, {"bug", "缺陷"}, {"othertask", "任务"}} {
								s.renderRequirementListProgressRow(c, p, typ.label, state.steps["items:"+typ.key])
							}
						})
					}
				}
				if len(state.failures) > 0 {
					ui.Column(c).FillWidth().MinWidth(0).Gap(6).Children(func() {
						for _, failure := range state.failures {
							label(c, failure, 12, p.danger).FillWidth().Wrap().Selectable()
						}
					})
				}
			})
			ui.Row(c).FillWidth().Gap(8).Wrap().Justify(ui.End).Shrink(0).Children(func() {
				if !state.running && len(state.failures) > 0 && requirementProgressButton(c, p, "requirement-list-progress-retry", "重试", s.requirementUnit.state.requirementsLoading).Clicked() {
					s.retryRequirementListProgress(state)
				}
				text := "关闭"
				if state.running {
					text = "收起"
				}
				button := requirementProgressButton(c, p, "requirement-list-progress-hide", text, false).AutoFocus()
				if button.Clicked() {
					state.open = false
				}
			})
		})
	})
	if dialog.Dismissed() {
		state.open = false
	}
}

func requirementProgressButton(c *ui.Context, p palette, key, text string, disabled bool) ui.Element {
	button := ui.ButtonBase(c.Key(key)).Height(34).Padding(0, 16).Center().Radius(20).Border(1, p.line).Label(text).Disabled(disabled)
	if button.Hovered() {
		button.Background(p.soft)
	}
	if button.Focused() {
		button.Border(2, p.blue)
	}
	button.Children(func() { label(c, text, 13, p.text).NoWrap() })
	return button
}

func (s *app) renderRequirementListProgressRow(c *ui.Context, p palette, name string, event application.RequirementListProgress) {
	status, color := "待执行", p.muted
	switch event.Status {
	case "running":
		status, color = "进行中", p.blue
	case "done":
		status, color = "已完成", p.green
	case "failed":
		status, color = "未完成", p.danger
	}
	ui.Column(c).FillWidth().MinWidth(0).Gap(4).Children(func() {
		ui.Row(c).FillWidth().MinWidth(0).Gap(12).AlignItems(ui.Center).Children(func() {
			label(c, name, 13, p.text).Grow(1).MinWidth(0).Wrap()
			if event.Total > 0 && event.WorkItemType == "" {
				label(c, fmt.Sprintf("已完成 %d／%d", event.Completed, event.Total), 11, p.muted).Shrink(0).NoWrap()
			}
			label(c, status, 12, color).Shrink(0).NoWrap().Tooltip(event.Message)
		})
		if event.Status == "running" && event.Message != "" && event.WorkItemType == "" {
			label(c, event.Message, 11, p.muted).FillWidth().Wrap()
		}
	})
}
