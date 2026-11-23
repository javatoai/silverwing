package desktop

import (
	"strings"

	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
	code "github.com/egoist/mygo/ui/editor"
)

func (s *app) renderTaskServiceDialog(c *ui.Context, p palette) {
	s.observeCatalogRuntime()
	v := s.taskServicesUnit.current
	if catalog := s.taskServicesUnit.catalog; catalog != nil {
		v = catalog
	}
	if v == nil || v.dialog == nil {
		return
	}
	detail := v.dialog
	if !s.taskServiceDialogCurrent(v, detail) {
		s.closeTaskServiceDialog(v, detail)
		return
	}
	width, height := c.Size()
	width, height = max(1, width*0.85), max(1, height*0.88)
	title := "Pod 明细"
	switch detail.kind {
	case taskServiceConfigDetail:
		title = "查看配置"
	case taskServiceConfigEditDetail:
		title = "修改配置"
	}
	const padding, border = 18, 1
	row := detail.row
	scope := v.dialogScope(row)
	key := v.documentID(row.key() + ":" + title)
	editOpen := detail.open
	open := &detail.open
	closeDialog := func() { s.closeTaskServiceDialog(v, detail) }
	if detail.kind == taskServiceConfigEditDetail {
		// A native dismissal is a close request while a draft still owns the dialog.
		open = &editOpen
		closeDialog = func() { s.requestTaskServiceConfigEditClose(v, detail) }
	}
	dialog := ui.DialogBase(c.Key(key), open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .38))
		panel.Width(width).Height(height).Padding(padding).Gap(10).
			Background(p.surface).Border(border, p.line).Radius(16).Label(title).Description(scope)
		if !*open {
			return
		}
		panel.Children(func() {
			ui.Row(c).FillWidth().Shrink(0).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
					label(c, title, 17, p.text).FontWeight(600)
					label(c, scope, 12, p.muted).SingleLine().Tooltip(scope)
				})
				if iconButton(c, "task-service-dialog-close", "close", "关闭", p).Clicked() {
					closeDialog()
				}
			})
			ui.Divider(c)
			if !detail.open {
				return
			}
			switch detail.kind {
			case taskServiceConfigDetail:
				s.renderTaskServiceConfig(c, p, v, detail)
			case taskServiceConfigEditDetail:
				s.renderTaskServiceConfigEdit(c, p, v, detail)
			default:
				if row.error != "" {
					if ui.Button(c.Key("retry-pods"), "重试").AlignSelf(ui.Start).Disabled(row.loading).Clicked() {
						s.requestTaskServicePods(v, row)
					}
				}
				renderTaskServicePods(c, p, detail, width-2*(padding+border))
			}
		})
	})
	if dialog.Dismissed() || !*open {
		closeDialog()
	}
}

const taskServicePodTableMinWidth = float32(920)

func renderTaskServicePods(c *ui.Context, p palette, detail *taskServiceDialog, width float32) {
	row := detail.row
	if row.loading {
		label(c, "正在读取 Pod…", 12, p.muted).Wrap()
	}
	if row.error != "" {
		label(c, row.error, 12, p.danger).Wrap()
	}
	if !row.loaded {
		return
	}
	if len(row.pods) == 0 {
		label(c, "该环境当前没有 Pod。", 12, p.muted)
		return
	}
	width = max(taskServicePodTableMinWidth, width)
	extra := (width - taskServicePodTableMinWidth) / 2
	widths := []float32{250 + extra, 90, 90, 200 + extra, 210}
	ui.ScrollHorizontal(c.Key("task-service-pod-horizontal")).TrackScroll(&detail.horizontal).Grow(1).FillWidth().MinHeight(0).Children(func() {
		ui.Column(c).Width(width).FillHeight().MinHeight(0).Children(func() {
			ui.Row(c).Height(32).Shrink(0).FillWidth().Background(p.soft).AlignItems(ui.Center).Children(func() {
				titles := []string{"Pod / 分组", "namespace", "状态", "运行版本", "Pod 创建时间"}
				taskServiceCells(c, widths, func(i int) { label(c, titles[i], 12, p.muted).FontWeight(600).NoWrap() })
			})
			ui.Scroll(c.Key("task-service-pod-rows")).TrackScroll(&detail.vertical).Grow(1).FillWidth().MinHeight(0).Children(func() {
				for _, pod := range row.pods {
					renderTaskServicePod(c.Key(pod.Namespace+":"+pod.Name), p, pod, widths)
					ui.Divider(c)
				}
			})
		})
	})
}

func renderTaskServicePod(c *ui.Context, p palette, pod production.RuntimePod, widths []float32) {
	ui.Column(c).FillWidth().Shrink(0).Padding(6, 0).Children(func() {
		taskServiceCells(c, widths, func(i int) {
			switch i {
			case 0:
				label(c, pod.Name, 12, p.text).SingleLine().FillWidth().Tooltip(pod.Name)
				if pod.GroupName != "" {
					label(c, pod.GroupName, 11, p.muted).SingleLine().FillWidth().Tooltip(pod.GroupName)
				}
			case 1:
				label(c, pod.Namespace, 12, p.text).SingleLine().Tooltip(pod.Namespace)
			case 2:
				status := pod.Phase
				if !pod.Ready {
					status = strings.TrimPrefix(status+" · 未就绪", " · ")
				}
				label(c, firstNonEmpty(status, "—"), 12, p.text).SingleLine().Tooltip(status)
			case 3:
				label(c, firstNonEmpty(pod.Version, "未知版本"), 12, p.text).SingleLine().Tooltip(pod.Version)
			case 4:
				label(c, firstNonEmpty(pod.CreatedTime, "—"), 12, p.text).SingleLine().Tooltip(pod.CreatedTime)
			}
		})
	})
}

func (s *app) renderTaskServiceConfig(c *ui.Context, p palette, v *taskServicesView, detail *taskServiceDialog) {
	row, config := detail.row, &detail.config
	if len(config.namespaceOptions) > 0 || config.loaded || config.error != "" {
		ui.Row(c).FillWidth().Shrink(0).Gap(8).AlignItems(ui.Center).Children(func() {
			if len(config.namespaceOptions) > 0 {
				selected := firstNonEmpty(config.namespace, "选择 namespace")
				if ui.Select(c.Key("namespace-select"), &selected, config.namespaceOptions).Width(170).MinWidth(100).Height(30).Label("选择 namespace").Changed() {
					s.selectTaskServiceConfigNamespace(v, detail, selected)
				}
			}
			if config.loaded || config.error != "" {
				readLabel := "刷新配置"
				if config.error != "" {
					readLabel = "重试"
				}
				if ui.Button(c.Key("read-config"), readLabel).Disabled(config.loading).Clicked() {
					s.requestTaskServiceConfig(v, detail)
				}
			}
			if config.loaded {
				if config.preview.view != nil && ui.Button(c.Key("find-config"), "查找").Clicked() {
					config.preview.view.Run(code.Find)
				}
				if ui.Button(c.Key("copy-config"), "复制").Clicked() {
					s.copyTextToClipboard(c.Services(), config.text, "已复制脱敏配置")
				}
			}
		})
	}
	if config.loading {
		label(c, "正在读取最新配置…", 12, p.muted)
	}
	if config.error != "" {
		label(c, config.error, 12, p.danger).Wrap()
	}
	if config.loaded {
		// The find field has its own Escape shortcut inside the preview.
		renderNativeCodePreview(c, p, &config.preview, v.documentID(row.key()+":"+config.namespace),
			row.name+"-"+row.environment+".properties", config.text, "service-config").Grow(1).
			OnShortcut(0, ui.KeyEscape, func() { s.closeTaskServiceDialog(v, detail) })
	}
}
