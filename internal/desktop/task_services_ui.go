package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

const taskServiceCellGap = 12
const taskServiceRowInset = 12

var taskServiceHeadings = [...]string{"服务", "Pod 数量", "运行版本", "Pod 创建时间", "操作"}

func taskServiceColumnWidths(width float32) (widths []float32, useActionMenu bool) {
	widths = []float32{104, 60, 104, 136, 304}
	available := width - float32(2*taskServiceRowInset+(len(widths)-1)*taskServiceCellGap)
	textWidth := widths[0] + widths[2] + widths[3]
	if available < textWidth+widths[1]+widths[4] {
		useActionMenu = true
		widths[4] = 34
	}
	extra := available - widths[1] - widths[4] - textWidth
	if extra < 0 {
		scale := max(0, (textWidth+extra)/textWidth)
		widths[0] *= scale
		widths[2] *= scale
		widths[3] *= scale
	} else {
		widths[0] += extra * .45
		widths[2] += extra * .55
	}
	return widths, useActionMenu
}

func taskServiceCells(c *ui.Context, widths []float32, render func(int)) {
	ui.Row(c).FillWidth().MinWidth(0).Gap(taskServiceCellGap).Padding(0, taskServiceRowInset).Children(func() {
		for i, width := range widths {
			column := i
			ui.Column(c.Key(fmt.Sprintf("service-column-%d", i))).Width(width).Shrink(0).Justify(ui.Center).Children(func() { render(column) })
		}
	})
}

func (s *app) renderTaskServiceEnvironmentTabs(c *ui.Context, p palette, v *taskServicesView) {
	renderServiceEnvironmentTabs(c, p, "task-service-environments", "服务环境", v.environment, func(environment string) {
		s.selectTaskServicesEnvironment(v, environment)
	})
}

func renderServiceEnvironmentTabs(c *ui.Context, p palette, key, title, environment string, selectEnvironment func(string)) {
	selected := 0
	if environment == "prod" {
		selected = 1
	}
	parts := ui.TabsBase(c.Key(key), &selected, 2)
	parts.List.Height(32).Gap(2).Label(title).Children(func() {
		for i, name := range []string{"UAT", "PROD"} {
			tab := parts.Tab(i).Height(32).Padding(0, 12).Radius(8).Label(name + " 环境")
			if selected == i {
				tab.Background(p.selected)
			} else if tab.Hovered() {
				tab.Background(p.soft)
			}
			tab.Children(func() { label(c, name, 13, p.text).FontWeight(600).NoWrap() })
		}
	})
	if parts.List.Changed() {
		selectEnvironment([]string{"uat", "prod"}[selected])
	}
}

func (s *app) renderTaskServices(c *ui.Context, p palette) {
	v := s.ensureTaskServicesCurrent()
	if v == nil {
		label(c, "当前任务服务信息不可用，请重新选择任务。", 13, p.muted).Padding(18).Wrap()
		return
	}
	ui.Column(c.Key("task-services:" + v.identity.documentID(""))).Bind(&v.canvas).Fill().MinWidth(0).MinHeight(0).Background(p.surface).Children(func() {
		windowWidth, windowHeight := c.Size()
		layout := taskServicesLayout{windowWidth, windowHeight, s.taskListPaneLayout().width, s.navigationCollapsed}
		if v.layout != layout || v.canvas.Bounds(c).W <= 0 {
			v.layout = layout
			c.AnimationFrame()
		}
		ui.Row(c).Height(46).FillWidth().Padding(0, 14).Gap(10).AlignItems(ui.Center).Children(func() {
			s.renderTaskServiceEnvironmentTabs(c, p, v)
			rows := v.visibleRows()
			pending := 0
			for _, row := range rows {
				if row.loading {
					pending++
				}
			}
			if pending > 0 {
				status := fmt.Sprintf("正在更新 %d 个服务", pending)
				label(c, status, 12, p.muted).Grow(1).MinWidth(0).SingleLine().Tooltip(status)
			} else {
				ui.Spacer(c)
			}
			if ui.Button(c.Key("task-services-refresh"), "刷新全部").Disabled(len(rows) == 0).Label("刷新当前环境全部服务").Tooltip("刷新 " + strings.ToUpper(v.environment) + " 全部服务").Clicked() {
				s.refreshTaskServicesEnvironment(v)
			}
		})
		rows := v.visibleRows()
		if len(rows) == 0 {
			ui.Column(c).FillWidth().Padding(28).Gap(8).Children(func() {
				if v.hasServices {
					label(c, "暂无已开启探测的服务", 16, p.text).FontWeight(600)
				} else {
					label(c, "此任务尚未加入服务", 16, p.text).FontWeight(600)
				}
			})
			return
		}
		widths, useActionMenu := taskServiceColumnWidths(v.canvas.Bounds(c).W)
		ui.Row(c).Height(35).FillWidth().Background(p.soft).AlignItems(ui.Center).Children(func() {
			taskServiceCells(c, widths, func(i int) {
				title := taskServiceHeadings[i]
				label(c, title, 12, p.muted).FontWeight(600).FillWidth().SingleLine().Tooltip(title)
			})
		})
		ui.Scroll(c.Key("task-services-rows")).TrackScroll(&v.vertical).Grow(1).FillWidth().MinWidth(0).MinHeight(0).Children(func() {
			for _, row := range rows {
				s.renderTaskServiceRow(c, p, v, row, widths, useActionMenu)
				ui.Divider(c)
			}
		})
	})
}

func taskServiceDetailAction(c *ui.Context, p palette, text string) ui.Element {
	b := ui.ButtonBase(c).Height(30).Shrink(0).Padding(0, 7).Radius(6).Center().FocusRing(true).Label(text).Background(p.surface).Border(1, p.line)
	if b.Hovered() {
		b.Background(p.soft)
	}
	b.Children(func() { label(c, text, 12, p.text).NoWrap() })
	return b
}

func (s *app) renderTaskServiceRow(c *ui.Context, p palette, v *taskServicesView, row *taskServiceRow, widths []float32, useActionMenu bool) {
	versions := taskServiceVersions(row.pods)
	container := ui.ButtonBase(c.Key("service-row:"+row.key())).FillWidth().MinWidth(0).MinHeight(48).Padding(6, 0).
		AlignItems(ui.Center).FocusRing(true).Role(ui.RoleButton).Label(row.name + " " + strings.ToUpper(row.environment))
	if container.Hovered() {
		container.Background(p.soft)
	}
	container.Children(func() {
		taskServiceCells(c, widths, func(column int) {
			s.renderTaskServiceCell(c, p, v, row, versions, column, useActionMenu)
		})
	})
	if container.Clicked() {
		s.openTaskServiceDialog(v, row, taskServicePodsDetail)
	}
}

func (s *app) renderTaskServiceCell(c *ui.Context, p palette, v *taskServicesView, row *taskServiceRow, versions []taskServiceVersionCount, column int, useActionMenu bool) {
	if row.loading && !row.loaded && column >= 1 && column <= 3 {
		ui.Box(c).FillWidth().MaxWidth(60).Height(8).Radius(3).Background(p.line)
		return
	}
	switch column {
	case 0:
		label(c, row.name, 13, p.text).FontWeight(600).SingleLine().FillWidth().Tooltip(row.name + "\n" + row.genbuName)
	case 1:
		if !row.loaded {
			label(c, "—", 13, p.muted)
			return
		}
		b := ui.ButtonBase(c.Key("pods-count")).Height(30).Label("查看 " + row.name + " " + row.environment + " Pod 明细").Tooltip("查看 Pod 明细")
		b.Children(func() { label(c, fmt.Sprintf("%d 个", len(row.pods)), 13, p.text).FontWeight(600) })
		if b.Clicked() {
			s.openTaskServiceDialog(v, row, taskServicePodsDetail)
		}
	case 2:
		if row.error != "" {
			label(c, row.error, 12, p.danger).SingleLine().FillWidth().Tooltip(row.error)
		}
		if row.loaded && len(versions) > 0 {
			for _, version := range versions {
				ui.Row(c).FillWidth().MinWidth(0).Gap(6).AlignItems(ui.Center).Children(func() {
					label(c, version.name, 12, p.text).SingleLine().Grow(1).MinWidth(0).Tooltip(version.name)
					label(c, fmt.Sprintf("× %d", version.count), 12, p.muted).Shrink(0).NoWrap()
				})
			}
		} else if row.loading {
			label(c, "正在读取…", 12, p.muted)
		} else if row.error == "" {
			label(c, "—", 12, p.muted)
		}
	case 3:
		if row.loaded {
			stamp := taskServiceFirstCreationTime(row.pods)
			label(c, stamp, 12, p.text).SingleLine().FillWidth().Tooltip(stamp)
		} else {
			label(c, "—", 12, p.muted)
		}
	case 4:
		s.renderTaskServiceActions(c, p, v, row, useActionMenu)
	}
}

func (s *app) renderTaskServiceActions(c *ui.Context, p palette, v *taskServicesView, row *taskServiceRow, useActionMenu bool) {
	type serviceAction struct {
		key, text, label string
		disabled         bool
		run              func()
	}
	target := row.name + " " + row.environment
	genbuUnavailable := row.genbuName == ""
	actions := []serviceAction{
		{key: "pods-detail-action", text: "Pod 明细", label: target + " Pod 明细", run: func() {
			s.openTaskServiceDialog(v, row, taskServicePodsDetail)
		}},
		{key: "config", text: "查看配置", label: "查看 " + target + " 配置", disabled: genbuUnavailable, run: func() {
			s.openTaskServiceDialog(v, row, taskServiceConfigDetail)
		}},
	}
	if !v.identity.archived {
		actions = append(actions, serviceAction{key: "config-edit", text: "修改配置", label: "修改 " + target + " 配置", disabled: genbuUnavailable, run: func() {
			s.openTaskServiceDialog(v, row, taskServiceConfigEditDetail)
		}})
	}
	actions = append(actions, serviceAction{key: "refresh", text: "刷新", label: "刷新 " + target, run: func() {
		s.requestTaskServicePods(v, row)
	}})

	ui.Row(c).FillWidth().MinWidth(0).Gap(4).AlignItems(ui.Center).Children(func() {
		if !useActionMenu {
			for _, action := range actions {
				if taskServiceDetailAction(c.Key(action.key), p, action.text).Label(action.label).Disabled(action.disabled).Clicked() {
					action.run()
				}
			}
		}
		s.overflowMenuOwned(c, p, "service-more", v.identity.documentID(row.key()+":service-more"), "更多 · "+row.name, !useActionMenu && genbuUnavailable, func(menu *overflowMenuBuilder) {
			if useActionMenu {
				for _, action := range actions {
					if menu.Item(action.key, action.text).Disabled(action.disabled).Chosen() {
						action.run()
					}
				}
			}
			if menu.Item("approvals", "查看配置审批").Disabled(genbuUnavailable).Chosen() {
				s.openTaskServiceApprovals(v, row, 0)
			}
		})
	})
}
