package desktop

import (
	"fmt"
	"strings"

	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
)

const uatReleaseButtonWidth = 88

func uatReleaseButton(c *ui.Context, p palette, key, target string, height float32, disabled bool) bool {
	button := ui.ButtonBase(c.Key(key)).Width(uatReleaseButtonWidth).Height(height).Shrink(0).Radius(8).Center().
		FocusRing(true).Label(uatReleaseLabel+" · "+target).Tooltip(uatReleaseLabel+" · "+target).
		Border(1, p.line).Disabled(disabled)
	background, color := p.selected, p.blue
	if disabled {
		background, color = p.soft, p.muted
	} else if button.Hovered() {
		button.Border(1, p.blue)
	}
	button.Background(background).Children(func() { label(c, uatReleaseLabel, 12, color).FontWeight(600).NoWrap() })
	return button.Clicked()
}

func (s *app) renderUATPodActionDialog(c *ui.Context, p palette) {
	d := s.taskServicesUnit.uatDialog
	if !s.uatPodActionCurrent(d) {
		s.closeUATPodAction(d)
		return
	}
	title, scope := d.title(), d.service+" · UAT"
	if d.operation == uatRelease {
		title = "UAT 发布"
	}
	renderUATActionDialog(c, p, "uat-pod-action", title, scope, func() { s.closeUATPodAction(d) },
		func() { s.renderUATPodActionBody(c, p, d) }, func() { s.renderUATPodActionFooter(c, p, d) })
}

func renderUATActionDialog(c *ui.Context, p palette, key, title, scope string, close, body, footer func()) {
	width, height := c.Size()
	open := true
	dialog := ui.DialogBase(c.Key(key), &open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .38))
		panel.Width(max(1, min(560, width-32))).MaxHeight(max(1, height-32)).Padding(18).Gap(12).
			Background(p.surface).Border(1, p.line).Radius(16).Label(title).Description(scope)
		panel.Children(func() {
			ui.Row(c).FillWidth().Shrink(0).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
					label(c, title, 17, p.text).FontWeight(600)
					label(c, scope, 12, p.muted).SingleLine().Tooltip(scope)
				})
				if iconButton(c, key+"-close", "close", "关闭", p).Clicked() {
					open = false
				}
			})
			if !open {
				return
			}
			ui.Divider(c)
			ui.Scroll(c.Key(key + "-content")).FillWidth().MinHeight(0).Shrink(1).Gap(10).Children(body)
			footer()
		})
	})
	if dialog.Dismissed() || !open {
		close()
	}
}

func uatPrimaryButton(c *ui.Context, key, text string, disabled bool) bool {
	button := ui.Button(c.Key(key), text).Height(34).Padding(0, 16).Radius(20).Disabled(disabled)
	if !disabled {
		blue := ui.Hex("#356AE6")
		if button.Hovered() {
			blue = ui.Hex("#2454C6")
		}
		button.Background(blue).TextColor(ui.Hex("#FFFFFF"))
	}
	return button.Clicked()
}

func (s *app) renderUATPodActionBody(c *ui.Context, p palette, d *uatPodActionDialog) {
	submitting := s.taskServicesUnit.uatSubmission == d
	if d.operation == uatRelease {
		if d.pinned != "" || submitting || d.receipt != "" {
			label(c, "Tag · "+strings.TrimSpace(d.tag), 13, p.text).Wrap().Selectable()
		} else {
			s.renderUATTagPicker(c, p, d)
		}
	} else if d.preview != nil {
		renderUATRestartScope(c, p, d)
		if d.receipt == "" && !submitting {
			label(c, "此操作会重启整个 Pod Group。", 12, p.muted).Wrap()
		}
	}
	if d.previewRequest != nil {
		message := "正在检查目标和影响范围…"
		if d.operation == uatRelease {
			message = "正在准备发布…"
		}
		label(c, message, 12, p.muted).Wrap()
	}
	if d.failure != "" {
		label(c, d.failure, 12, p.danger).Wrap().Selectable()
	}
	if d.receipt != "" || submitting {
		renderUATPodReceipt(c, p, d.receipt)
	}
}

func (s *app) renderUATTagPicker(c *ui.Context, p palette, d *uatPodActionDialog) {
	locked := d.previewRequest != nil
	search := func() { s.searchUATTags(d, strings.TrimSpace(d.tag), 1) }
	label(c, "发布 Tag", 12, p.text).FontWeight(600)
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.TextInput(c.Key("uat-tag"), &d.tag).Height(34).Grow(1).MinWidth(0).Label("发布 Tag").ReadOnly(locked).
			Placeholder("输入完整 Tag 或搜索关键词").OnSubmit(search).OnChange(func() {
			s.editUATTag(d)
			d.tags, d.tagsLoaded, d.tagsError = production.GenbuTagPage{}, false, ""
		})
		if ui.Button(c.Key("uat-tag-search"), "搜索 Tag").Disabled(locked || d.tagsRequest != nil).Clicked() {
			search()
		}
	})
	switch {
	case d.tagsRequest != nil:
		label(c, "正在读取 Genbu Tag…", 12, p.muted)
	case d.tagsError != "":
		label(c, "读取 Tag 失败。"+d.tagsError, 12, p.danger).Wrap()
	case d.tagsLoaded && len(d.tags.Items) == 0:
		label(c, "没有匹配的 Tag，也可以直接输入完整 Tag。", 12, p.muted).Wrap()
	case d.tagsLoaded:
		ui.Scroll(c.Key("uat-tag-list")).FillWidth().MaxHeight(192).MinHeight(0).TrackScroll(&d.tagScroll).
			Radius(8).Border(1, p.line).Children(func() {
			for _, item := range d.tags.Items {
				entry := ui.ButtonBase(c.Key("uat-tag-option-"+item.Tag)).Row().FillWidth().Height(32).Padding(0, 9).
					Gap(8).AlignItems(ui.Center).FocusRing(true).Label("选择 Tag " + item.Tag).Tooltip(item.Tag).Disabled(locked)
				if d.tag == item.Tag {
					entry.Background(p.selected)
				} else if entry.Hovered() {
					entry.Background(p.soft)
				}
				entry.Children(func() {
					label(c, item.Tag, 12, p.text).Grow(1).MinWidth(0).SingleLine()
					if item.CreatedAt != "" {
						label(c, item.CreatedAt, 11, p.muted).Shrink(0).NoWrap()
					}
				})
				if entry.Clicked() {
					d.tag = item.Tag
					s.editUATTag(d)
				}
			}
		})
		if d.tags.Total > d.tags.PageSize && d.tags.PageSize > 0 {
			genbuReadPagination(c, p, d.tags.Page, d.tags.PageSize, d.tags.Total, true, locked, func(page int) {
				s.searchUATTags(d, d.tagsKeyword, page)
			})
		}
	}
}

func renderUATRestartScope(c *ui.Context, p palette, d *uatPodActionDialog) {
	preview := d.preview
	group := fmt.Sprintf("ID %d", preview.GroupID)
	if preview.Group != "" {
		group = preview.Group + " · " + group
	}
	fields := []struct{ label, value string }{
		{"Genbu 服务", preview.GenbuService},
		{"命名空间", preview.Namespace},
		{"Pod Group", group},
		{"影响 Pod", fmt.Sprintf("%d 个", preview.PodCount)},
	}
	ui.Column(c).FillWidth().Gap(8).Padding(12).Radius(8).Background(p.soft).Children(func() {
		for _, field := range fields {
			ui.Row(c).FillWidth().Gap(10).Children(func() {
				label(c, field.label, 12, p.muted).Width(74).Shrink(0).NoWrap()
				label(c, field.value, 13, p.text).Grow(1).MinWidth(0).Wrap().Selectable()
			})
		}
	})
}

func renderUATPodReceipt(c *ui.Context, p palette, receipt string) {
	text, detail, background, color := "正在提交…", "", p.soft, p.text
	switch receipt {
	case "submitted":
		text, detail = "已提交", "可打开 Pod 明细确认运行版本与就绪状态。"
		background, color = p.greenBg, p.green
	case "unknown":
		text, detail = "结果待确认", "请先查看 Pod 明细，核对服务状态后再操作。"
		background, color = p.amberBg, p.amber
	}
	ui.Column(c).FillWidth().Gap(4).Padding(10).Radius(8).Background(background).Children(func() {
		label(c, text, 13, color).FontWeight(600).Wrap()
		if detail != "" {
			label(c, detail, 12, p.text).Wrap()
		}
	})
}

func (s *app) renderUATPodActionFooter(c *ui.Context, p palette, d *uatPodActionDialog) {
	ui.Row(c).FillWidth().Shrink(0).Gap(8).AlignItems(ui.Center).Justify(ui.End).Wrap().Children(func() {
		switch {
		case d.receipt != "":
			if ui.Button(c.Key("uat-pod-action-pods"), "查看 Pod 明细").Clicked() && s.uatPodActionCurrent(d) {
				s.closeUATPodAction(d)
				d.showPods()
			}
		case s.taskServicesUnit.uatSubmission == d:
			if ui.Button(c.Key("uat-pod-action-hide"), "关闭").Clicked() {
				s.closeUATPodAction(d)
			}
		case d.operation == uatRestart && d.preview != nil:
			if action(c, "uat-pod-action-confirm", "", "确认重启 UAT Pod", p, true) {
				s.submitUATPodAction(d, d.preview)
			}
		default:
			if ui.Button(c.Key("uat-pod-action-cancel"), "取消").Clicked() {
				s.closeUATPodAction(d)
			}
			if d.operation == uatRestart {
				if ui.Button(c.Key("uat-pod-action-preview"), "检查重启范围").Disabled(d.previewRequest != nil).Clicked() {
					s.previewUATPodAction(d)
				}
				return
			}
			disabled := !d.open || s.busy || d.previewRequest != nil || strings.TrimSpace(d.tag) == ""
			if uatPrimaryButton(c, "uat-pod-action-release", uatReleaseLabel, disabled) {
				s.previewUATPodAction(d)
			}
		}
	})
}
