package desktop

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
)

const (
	taskServiceCellGap      = 12
	taskServiceRowInset     = 12
	taskServicePublishWidth = 42
)

var taskServiceHeadings = [...]string{"服务", "Pod 数量", "运行版本", "Pod 创建时间", "操作"}
var taskServiceColumnWidths = [...]float32{176, 72, 154, 144, 396}

var taskServiceTableMinWidth = func() float32 {
	width := float32(2*taskServiceRowInset + (len(taskServiceColumnWidths)-1)*taskServiceCellGap)
	for _, columnWidth := range taskServiceColumnWidths {
		width += columnWidth
	}
	return width
}()

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
		s.renderTaskServiceTable(c, p, v, rows)
	})
}

func (s *app) renderTaskServiceTable(c *ui.Context, p palette, v *taskServicesView, rows []*taskServiceRow) {
	width := max(taskServiceTableMinWidth, v.canvas.Bounds(c).W)
	widths := taskServiceColumnWidths
	widths[len(widths)-1] += width - taskServiceTableMinWidth
	ui.ScrollBoth(c.Key("task-services-table-scroll")).TrackScroll(&v.scroll).KeepScrollbars(true).
		Grow(1).FillWidth().MinWidth(0).MinHeight(0).Children(func() {
		ui.Column(c).Width(width).Shrink(0).Label("服务信息表格").Children(func() {
			ui.Row(c).Height(35).Shrink(0).FillWidth().Background(p.soft).AlignItems(ui.Center).Sticky().Top(0).Children(func() {
				taskServiceCells(c, widths[:], func(i int) {
					title := taskServiceHeadings[i]
					heading := label(c, title, 12, p.muted).FontWeight(600).SingleLine().Tooltip(title)
					switch i {
					case 1:
						heading.AlignSelf(ui.Center)
					case 4:
						heading.AlignSelf(ui.End)
					}
				})
			})
			for _, row := range rows {
				s.renderTaskServiceRow(c, p, v, row, widths[:])
				ui.Divider(c)
			}
		})
	})
}

func taskServiceDetailAction(c *ui.Context, p palette, text string) ui.Element {
	b := ui.ButtonBase(c).Height(26).Shrink(0).Padding(0, 7).Radius(4).Center().FocusRing(true).Label(text).Background(p.surface)
	if b.Hovered() {
		b.Background(p.soft)
	}
	b.Children(func() { label(c, text, 12, p.text).NoWrap() })
	return b
}

func taskServiceActionGroup(c *ui.Context, p palette, name string) ui.Element {
	return ui.Row(c).Height(30).Shrink(0).Padding(2).Gap(2).AlignItems(ui.Center).
		Radius(6).Background(p.surface).Border(1, p.line).Label(name)
}

func (s *app) renderTaskServiceRow(c *ui.Context, p palette, v *taskServicesView, row *taskServiceRow, widths []float32) {
	versions := taskServiceVersions(row.pods)
	container := ui.ButtonBase(c.Key("service-row:"+row.key())).FillWidth().MinWidth(0).MinHeight(48).Shrink(0).Padding(6, 0).
		AlignItems(ui.Center).FocusRing(true).Role(ui.RoleButton).Label(row.name + " " + strings.ToUpper(row.environment))
	if container.Hovered() {
		container.Background(p.soft)
	}
	container.Children(func() {
		taskServiceCells(c, widths, func(column int) {
			s.renderTaskServiceCell(c, p, v, row, versions, column)
		})
	})
	if container.Clicked() {
		s.openTaskServiceDialog(v, row, taskServicePodsDetail)
	}
}

func (s *app) renderTaskServiceCell(c *ui.Context, p palette, v *taskServicesView, row *taskServiceRow, versions []taskServiceVersionCount, column int) {
	if row.loading && !row.loaded && column >= 1 && column <= 3 {
		placeholder := ui.Box(c).FillWidth().MaxWidth(60).Height(8).Radius(3).Background(p.line)
		if column == 1 {
			placeholder.AlignSelf(ui.Center)
		}
		return
	}
	switch column {
	case 0:
		label(c, row.name, 13, p.text).FontWeight(600).SingleLine().FillWidth().Tooltip(row.name + "\n" + row.genbuName)
	case 1:
		if !row.loaded {
			label(c, "—", 13, p.muted).AlignSelf(ui.Center)
			return
		}
		b := ui.ButtonBase(c.Key("pods-count")).Height(30).AlignSelf(ui.Center).
			Label("查看 " + row.name + " " + row.environment + " Pod 明细").Tooltip("查看 Pod 明细")
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
					label(c, version.name, 12, p.text).SingleLine().Shrink(1).MinWidth(0).Tooltip(version.name)
					count := strconv.Itoa(version.count)
					detail := version.name + " · " + count + " 个 Pod"
					ui.Box(c).MinWidth(18).Height(18).Padding(0, 3).Radius(9).Shrink(0).Center().
						Background(p.soft).Label(detail).Tooltip(detail).Children(func() {
						label(c, count, 11, p.muted).FontWeight(600).NoWrap()
					})
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
		s.renderTaskServiceActions(c, p, v, row)
	}
}

func (s *app) renderTaskServiceActions(c *ui.Context, p palette, v *taskServicesView, row *taskServiceRow) {
	target := row.name + " " + row.environment
	genbuUnavailable := row.genbuName == ""
	uat := row.environment == "uat"
	showUAT := s.taskUATPodActionAvailable(v, row)
	uatBusy := s.busy || s.taskServicesUnit.uatWriteRunning()
	ui.Row(c).FillWidth().MinWidth(0).Gap(6).AlignItems(ui.Center).Justify(ui.End).Children(func() {
		taskServiceActionGroup(c.Key("runtime-actions"), p, "运行操作").Children(func() {
			if taskServiceDetailAction(c.Key("pods-detail-action"), p, "查看").
				Label(target + " Pod 明细").Tooltip("查看 Pod 明细").Clicked() {
				s.openTaskServiceDialog(v, row, taskServicePodsDetail)
			}
			if showUAT {
				publish := uatReleaseLabel + " · " + row.name
				if taskServiceDetailAction(c.Key("uat-release"), p, "发布").Width(taskServicePublishWidth).
					Label(publish).Tooltip(publish).Disabled(uatBusy).Clicked() {
					s.openTaskUATPodAction(v, row, uatRelease)
				}
				restart := uatRestartLabel + " · " + row.name
				if taskServiceDetailAction(c.Key("uat-restart"), p, "重启").
					Label(restart).Tooltip(restart).Disabled(uatBusy).Clicked() {
					s.openTaskUATPodAction(v, row, uatRestart)
				}
			}
		})
		taskServiceActionGroup(c.Key("config-actions"), p, "配置操作").Children(func() {
			if taskServiceDetailAction(c.Key("config"), p, "查看配置").Label("查看 " + target + " 配置").Disabled(genbuUnavailable).Clicked() {
				s.openTaskServiceDialog(v, row, taskServiceConfigDetail)
			}
			if !uat {
				return
			}
			if !v.identity.archived && taskServiceDetailAction(c.Key("config-edit"), p, "修改配置").
				Label("修改 "+target+" 配置").Disabled(genbuUnavailable).Clicked() {
				s.openTaskServiceDialog(v, row, taskServiceConfigEditDetail)
			}
			if taskServiceDetailAction(c.Key("approvals"), p, "查看审批").
				Label("查看 " + target + " 配置审批").Disabled(genbuUnavailable).Clicked() {
				s.openTaskServiceApprovals(v, row, 0)
			}
		})
		if uat && taskServiceDetailAction(c.Key("refresh"), p, "刷新").Height(30).Radius(6).
			Border(1, p.line).Label("刷新 "+target).Clicked() {
			s.requestTaskServicePods(v, row)
		}
	})
}
