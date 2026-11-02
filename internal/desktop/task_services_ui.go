package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	code "github.com/egoist/mygo/ui/editor"
)

const taskServiceTableMinWidth = float32(940)

func taskServiceColumnWidths(width float32) []float32 {
	extra := max(float32(0), width-taskServiceTableMinWidth) / 2
	return []float32{220 + extra, 80, 220 + extra, 168, 180}
}

func taskServiceCells(c *ui.Context, widths []float32, render func(int)) {
	ui.Row(c).FillWidth().MinWidth(0).Gap(12).Padding(0, 12).Children(func() {
		for i, width := range widths {
			column := i
			ui.Column(c.Key(fmt.Sprintf("service-column-%d", i))).Width(width).Shrink(0).Justify(ui.Center).Children(func() { render(column) })
		}
	})
}

func (s *app) renderTaskServiceEnvironmentTabs(c *ui.Context, p palette, v *taskServicesView) {
	selected := 0
	if v.environment == "prod" {
		selected = 1
	}
	parts := ui.TabsBase(c.Key("task-service-environments"), &selected, 2)
	parts.List.Height(32).Gap(2).Label("服务环境").Children(func() {
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
		s.selectTaskServicesEnvironment(v, []string{"uat", "prod"}[selected])
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
				label(c, fmt.Sprintf("正在更新 %d 个服务", pending), 12, p.muted)
			}
			ui.Spacer(c)
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
		width := max(taskServiceTableMinWidth, v.canvas.Bounds(c).W)
		widths := taskServiceColumnWidths(width)
		ui.ScrollHorizontal(c.Key("task-services-horizontal")).TrackScroll(&v.horizontal).Grow(1).FillWidth().MinHeight(0).Children(func() {
			ui.Column(c.Key("task-services-table")).Width(width).FillHeight().MinHeight(0).Children(func() {
				ui.Row(c).Height(35).FillWidth().Background(p.soft).AlignItems(ui.Center).Children(func() {
					titles := []string{"服务", "Pod 数量", "运行版本", "Pod 创建时间", "操作"}
					taskServiceCells(c, widths, func(i int) { label(c, titles[i], 12, p.muted).FontWeight(600).NoWrap() })
				})
				ui.Scroll(c.Key("task-services-rows")).TrackScroll(&v.vertical).Grow(1).FillWidth().MinHeight(0).Children(func() {
					for _, row := range rows {
						s.renderTaskServiceRow(c, p, v, row, widths)
						if v.expanded == row.key() {
							s.renderTaskServiceDetail(c.Key("detail:"+row.key()), p, v, row, width)
						}
						ui.Divider(c)
					}
				})
			})
		})
	})
}

func taskServiceDetailAction(c *ui.Context, p palette, text string, selected bool) ui.Element {
	b := ui.ButtonBase(c).Height(30).Padding(0, 7).Radius(6).Center().FocusRing(true).Label(text).Background(p.surface).Border(1, p.line)
	if selected {
		b.Border(1, p.blue).Description("已展开，再次点击收起")
	} else {
		if b.Hovered() {
			b.Background(p.soft)
		}
	}
	b.Children(func() { label(c, text, 12, p.text).NoWrap() })
	return b
}

func (s *app) renderTaskServiceRow(c *ui.Context, p palette, v *taskServicesView, row *taskServiceRow, widths []float32) {
	versions := taskServiceVersions(row.pods)
	height := max(float32(48), float32(len(versions)*19+12))
	container := ui.Row(c.Key("service-row:" + row.key())).FillWidth().Height(height).AlignItems(ui.Center).Focusable().Role(ui.RoleButton).Label(row.name + " " + strings.ToUpper(row.environment))
	container.OnShortcut(0, ui.KeyEnter, func() {
		if v.expanded == row.key() {
			v.expanded = ""
		} else {
			v.expanded = row.key()
			v.detail = "pods"
		}
	})
	if v.expanded == row.key() {
		container.Background(p.selected)
	} else if container.Hovered() {
		container.Background(p.soft)
	}
	container.Children(func() {
		taskServiceCells(c, widths, func(i int) {
			if row.loading && !row.loaded && i >= 1 && i <= 3 {
				ui.Box(c).Width(widths[i] * .65).Height(8).Radius(3).Background(p.line)
				return
			}
			switch i {
			case 0:
				label(c, row.name, 13, p.text).FontWeight(600).SingleLine().FillWidth().Tooltip(row.name + "\n" + row.genbuName)
			case 1:
				if row.loaded {
					b := ui.ButtonBase(c.Key("pods-count")).Height(30).Label("查看 " + row.name + " " + row.environment + " Pod 明细").Tooltip("展开 Pod 明细")
					b.Children(func() { label(c, fmt.Sprintf("%d 个", len(row.pods)), 13, p.text).FontWeight(600) })
					if b.Clicked() {
						v.expanded = row.key()
						v.detail = "pods"
					}
				} else {
					label(c, "—", 13, p.muted)
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
				ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
					if taskServiceDetailAction(c.Key("pods-detail-action"), p, "Pod 明细", v.expanded == row.key() && v.detail == "pods").Label("展开 " + row.name + " " + row.environment + " Pod 明细").Clicked() {
						if v.expanded == row.key() && v.detail == "pods" {
							v.expanded = ""
						} else {
							v.expanded = row.key()
							v.detail = "pods"
						}
					}
					if taskServiceDetailAction(c.Key("config"), p, "查看配置", v.expanded == row.key() && v.detail == "config").Label("查看 " + row.name + " " + row.environment + " 配置").Disabled(row.genbuName == "").Clicked() {
						if v.expanded == row.key() && v.detail == "config" {
							v.expanded = ""
						} else {
							v.expanded = row.key()
							v.detail = "config"
							s.requestTaskServiceConfig(v, row)
						}
					}
					if taskServiceDetailAction(c.Key("refresh"), p, "刷新", false).Label("刷新 " + row.name + " " + row.environment).Clicked() {
						s.requestTaskServicePods(v, row)
					}
				})
			}
		})
	})
	if container.Clicked() {
		if v.expanded == row.key() {
			v.expanded = ""
		} else {
			v.expanded = row.key()
			v.detail = "pods"
		}
	}
}

func taskServicePodColumnWidths(width float32) []float32 {
	extra := max(float32(0), width-920) / 2
	return []float32{250 + extra, 90, 90, 200 + extra, 210}
}

func (s *app) renderTaskServiceDetail(c *ui.Context, p palette, v *taskServicesView, row *taskServiceRow, width float32) {
	ui.Column(c).FillWidth().Padding(12, 16).Gap(10).Background(p.soft).Children(func() {
		if v.detail == "config" {
			s.renderTaskServiceConfig(c, p, v, row)
			return
		}
		if !row.loaded {
			label(c, firstNonEmpty(row.error, "正在读取 Pod…"), 12, p.muted).Wrap()
			return
		}
		if len(row.pods) == 0 {
			label(c, "该环境当前没有 Pod。", 12, p.muted)
			return
		}
		widths := taskServicePodColumnWidths(width)
		ui.Row(c).FillWidth().Gap(12).Children(func() {
			for i, title := range []string{"Pod / 分组", "namespace", "状态", "运行版本", "Pod 创建时间"} {
				label(c, title, 11, p.muted).Width(widths[i]).FontWeight(600)
			}
		})
		for _, pod := range row.pods {
			ui.Row(c.Key(pod.Namespace+":"+pod.Name)).FillWidth().Gap(12).AlignItems(ui.Start).Padding(4, 0).Children(func() {
				ui.Column(c).Width(widths[0]).Children(func() {
					label(c, pod.Name, 12, p.text).SingleLine().FillWidth().Tooltip(pod.Name)
					label(c, pod.GroupName, 10, p.muted).SingleLine().FillWidth().Tooltip(pod.GroupName)
				})
				label(c, pod.Namespace, 12, p.text).Width(widths[1]).SingleLine().Tooltip(pod.Namespace)
				status := pod.Phase
				if !pod.Ready {
					status += " · 未就绪"
				}
				label(c, status, 12, p.text).Width(widths[2]).SingleLine().Tooltip(status)
				label(c, firstNonEmpty(pod.Version, "未知版本"), 12, p.text).Width(widths[3]).SingleLine().Tooltip(pod.Version)
				label(c, firstNonEmpty(pod.CreatedTime, "—"), 12, p.text).Width(widths[4]).SingleLine().Tooltip(pod.CreatedTime)
			})
		}
	})
}

func (s *app) renderTaskServiceConfig(c *ui.Context, p palette, v *taskServicesView, row *taskServiceRow) {
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		label(c, "namespace", 12, p.muted)
		ui.TextInput(c.Key("namespace"), &row.namespace).Width(210).Height(30).Label("配置 namespace").Placeholder("自动选择或手动输入").OnChange(func() { s.changeTaskServiceNamespace(v, row) })
		options := taskServiceNamespaces(row.pods)
		if len(options) > 1 {
			selected := row.namespace
			if ui.Select(c.Key("namespace-select"), &selected, options).Width(170).Height(30).Label("选择 namespace").Changed() {
				row.namespace = selected
				s.changeTaskServiceNamespace(v, row)
			}
		}
		readLabel := "读取配置"
		if row.configLoaded {
			readLabel = "刷新配置"
		}
		if ui.Button(c.Key("read-config"), readLabel).Disabled(row.configLoading).Clicked() {
			s.requestTaskServiceConfig(v, row)
		}
		if row.configView.view != nil && ui.Button(c.Key("find-config"), "查找").Clicked() {
			row.configView.view.Run(code.Find)
		}
		if row.configLoaded && ui.Button(c.Key("copy-config"), "复制").Clicked() {
			s.copyTextToClipboard(c.Services(), row.configText, "已复制脱敏配置")
		}
	})
	if row.configLoading {
		label(c, "正在读取最新配置…", 12, p.muted)
	}
	if row.configError != "" {
		label(c, row.configError, 12, p.danger).Wrap()
	}
	if row.configLoaded {
		ui.Column(c).FillWidth().Height(330).MinHeight(0).Children(func() {
			renderNativeCodePreview(c, p, &row.configView, v.identity.documentID(row.key()+":"+row.namespace), row.name+"-"+row.environment+".txt", row.configText, "service-config").Grow(1)
		})
	} else if !row.configLoading && row.configError == "" {
		label(c, "点击读取该环境最新的脱敏配置。", 12, p.muted)
	}
}
