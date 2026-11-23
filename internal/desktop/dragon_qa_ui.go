package desktop

import (
	_ "embed"
	"strings"
	"sync"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"

	"github.com/egoist/mygo/ui"
)

//go:embed assets/dragon-qa.png
var dragonQAIcon []byte

var dragonQAIconBitmap = sync.OnceValue(func() *ui.Bitmap {
	bitmap, _ := ui.DecodeBitmap(dragonQAIcon)
	return bitmap
})

func dragonQAIconButton(c *ui.Context, p palette) ui.Element {
	button := ui.ButtonBase(c.Key("task-dragon-qa")).Width(34).Height(34).Radius(20).Center().Label("青龙提测").Tooltip("青龙提测")
	if button.Hovered() {
		button.Background(p.soft)
	}
	if bitmap := dragonQAIconBitmap(); bitmap != nil {
		button.Children(func() {
			ui.Image(c, bitmap).Size(28, 28).Fit(ui.Contain)
		})
	}
	return button
}

func (s *app) renderDragonQADialog(c *ui.Context, p palette) {
	state := s.taskUnit.state.dragonQA
	if !s.dragonQACurrent(state) {
		s.taskUnit.state.dragonQA = nil
		return
	}
	running := s.taskUnit.state.dragonQAJobs[state.scope]
	ui.DialogBase(c.Key("dragon-qa-dialog"), &state.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.34))
		w, h := c.Size()
		panel.Width(min(560, w-32)).MaxHeight(h-32).Padding(18).Gap(14).
			Background(p.surface).Border(1, p.line).Radius(14)
		panel.Children(func() {
			label(c, "青龙提测", 18, p.text).FontWeight(600)
			ui.Scroll(c).FillWidth().MinHeight(0).MaxHeight(max(96, h-156)).Gap(12).Children(func() {
				s.renderDragonQAContent(c, p, state, running)
				if running != nil {
					ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Spinner(c).Size(16, 16).TextColor(p.blue)
						label(c, dragonQAProgressLabel(running.kind, state.result.Phase), 13, p.muted).Wrap()
					})
				}
				if state.err != "" && (!state.loaded || state.err != state.result.Message && state.err != state.result.CommentError) {
					label(c, state.err, 13, p.danger).Wrap()
				}
			})
			ui.Row(c).FillWidth().Shrink(0).Gap(8).Justify(ui.End).Children(func() {
				if toolButtonWithEnabled(c, "dragon-qa-close", "关闭", p, false, true) {
					state.open = false
				}
				if running != nil {
					return
				}
				switch {
				case !state.loaded && state.err != "":
					if toolButtonWithEnabled(c, "dragon-qa-load", "重新读取状态", p, true, true) {
						s.requestDragonQAJob(state, dragonQALoad, "")
					}
				case state.canEdit():
					if toolButtonWithEnabled(c, "dragon-qa-submit", "提交提测", p, true, state.canSubmit()) {
						s.requestDragonQAJob(state, dragonQASubmit, "")
					}
				case state.canCheckApproval():
					if toolButtonWithEnabled(c, "dragon-qa-check", "核查审批", p, true, true) {
						s.requestDragonQAJob(state, dragonQACheck, "")
					}
				}
			})
		})
	})
	if !state.open {
		s.taskUnit.state.dragonQA = nil
	}
}

func dragonQAProgressLabel(job dragonQAJob, phase string) string {
	if phase == application.DragonQAApproved || job == dragonQAComment {
		return "正在处理 Meego 评论…"
	}
	switch job {
	case dragonQASubmit:
		return "正在提交青龙审批…"
	case dragonQACheck:
		return "正在核查审批…"
	default:
		return "正在读取提测状态…"
	}
}

func (s *app) renderDragonQAContent(c *ui.Context, p palette, state *dragonQAViewState, running *dragonQARunningJob) {
	if !state.loaded {
		return
	}
	switch {
	case state.result.Phase == application.DragonQAApproved:
		s.renderDragonQAReceipt(c, p, state, running != nil)
	case running != nil && running.kind == dragonQASubmit:
		return
	case state.canCheckApproval():
		title := "审批结果待核查"
		if state.result.Phase == application.DragonQASubmitting {
			title = "审批正在提交"
		}
		label(c, title, 15, p.amber).FontWeight(600)
		if state.result.Message != "" {
			label(c, state.result.Message, 13, p.muted).Wrap()
		}
		s.renderDragonQACandidates(c, p, state, running != nil)
	case state.canEdit():
		renderDragonQAForm(c, p, state)
	default:
		label(c, "当前提测状态无法识别，请关闭后重新读取。", 13, p.danger).Wrap()
	}
}

func renderDragonQAForm(c *ui.Context, p palette, state *dragonQAViewState) {
	if state.result.Phase == application.DragonQAFailed && state.result.Message != "" {
		label(c, state.result.Message, 13, p.danger).Wrap()
	}
	ui.Column(c).FillWidth().Gap(5).Children(func() {
		label(c, "Meego 需求链接", 13, p.text).FontWeight(500)
		ui.TextInput(c.Key("dragon-qa-requirement"), &state.result.RequirementURL).ReadOnly(true).
			FillWidth().Height(34).Padding(0, 10).Background(p.soft).Border(1, p.line).Radius(7).Label("Meego 需求链接")
		if domain.ParseFeishuWorkItemLink(state.result.RequirementURL) == nil {
			label(c, "请先在任务中关联有效的 Meego 需求链接。", 12, p.amber).Wrap()
		}
	})
	ui.Column(c).FillWidth().Gap(5).Children(func() {
		label(c, "提测服务", 13, p.text).FontWeight(500)
		if len(state.choices) == 0 {
			label(c, "请先为任务添加服务。", 12, p.amber).Wrap()
		}
		for i := range state.choices {
			choice := &state.choices[i]
			ui.Checkbox(c.Key("dragon-qa-service-"+choice.service.ID), &choice.selected, "").
				FillWidth().Padding(6, 8).Gap(8).AlignItems(ui.Center).
				Background(p.soft).Radius(7).Label("提测服务 " + choice.service.Name).
				Children(func() { label(c, choice.service.Name, 13, p.text).Wrap() })
		}
	})
	ui.Column(c).FillWidth().Gap(5).Children(func() {
		label(c, "备注（可选）", 13, p.text).FontWeight(500)
		ui.TextArea(c.Key("dragon-qa-remark"), &state.remark).FillWidth().Height(70).Padding(8, 10).
			Background(p.surface).Border(1, p.line).Radius(7).Label("提测备注")
	})
	ui.Checkbox(c.Key("dragon-qa-sync-comment"), &state.syncComment, "同步审批链接到 Meego 需求评论").
		FillWidth().Padding(4, 0).Gap(8).AlignItems(ui.Center)
}

func (s *app) renderDragonQACandidates(c *ui.Context, p palette, state *dragonQAViewState, busy bool) {
	if len(state.result.Candidates) == 0 {
		return
	}
	if state.result.Message == "" {
		label(c, "请查看候选审批，确认它是否由本次提测创建。", 13, p.text).Wrap()
	}
	for _, item := range state.result.Candidates {
		ui.Column(c.Key("dragon-qa-candidate-" + item.ApprovalID)).FillWidth().Gap(5).Padding(9).
			Background(p.soft).Radius(7).Children(func() {
			label(c, "审批 "+item.ApprovalID, 13, p.text).FontWeight(500)
			if item.CreatedAt != "" {
				label(c, "提交于 "+item.CreatedAt, 12, p.muted).Wrap()
			}
			if len(item.Apps) > 0 {
				label(c, "应用 "+strings.Join(item.Apps, "、"), 12, p.muted).Wrap()
			}
			ui.Row(c).Gap(8).Wrap().Children(func() {
				openable := dragonQACanOpenURL(item.ApprovalURL)
				if toolButtonWithEnabled(c, "dragon-qa-candidate-open", "打开候选审批", p, false, openable) {
					s.openDragonQALink(c, state, item.ApprovalURL)
				}
				if toolButtonWithEnabled(c, "dragon-qa-candidate-confirm", "确认为本次审批", p, false, !busy && openable) {
					s.requestDragonQAJob(state, dragonQACheck, item.ApprovalID)
				}
			})
		})
	}
}

func (s *app) renderDragonQAReceipt(c *ui.Context, p palette, state *dragonQAViewState, busy bool) {
	label(c, "提测审批已创建", 15, p.green).FontWeight(600)
	ui.Column(c).FillWidth().Gap(7).Padding(11).Background(p.soft).Radius(8).Children(func() {
		link := state.result.ApprovalURL
		if link == "" {
			label(c, "暂未读取到审批链接。", 13, p.muted).Wrap()
			return
		}
		label(c, link, 13, p.text).Wrap()
		ui.Row(c).Gap(8).Wrap().Children(func() {
			if toolButtonWithEnabled(c, "dragon-qa-open-link", "打开审批", p, false, dragonQACanOpenURL(link)) {
				s.openDragonQALink(c, state, link)
			}
			if toolButtonWithEnabled(c, "dragon-qa-copy-link", "复制链接", p, false, true) {
				s.copyTextToClipboard(c.Services(), link, "已复制提测审批链接")
			}
		})
	})
	status, color := "未同步", p.muted
	switch state.result.CommentStatus {
	case application.DragonQACommentSynced:
		status, color = "已同步", p.green
	case application.DragonQACommentPending:
		status = "待同步"
	case application.DragonQACommentFailed:
		status, color = "同步失败", p.danger
	case application.DragonQACommentUnknown:
		status, color = "发送结果待核对", p.amber
	}
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(8).Wrap().Children(func() {
		label(c, "Meego 评论", 13, p.text).FontWeight(500)
		label(c, status, 13, color)
		if state.canRetryComment() {
			actionLabel := "补发评论"
			if state.result.CommentStatus == application.DragonQACommentUnknown {
				actionLabel = "核对评论"
			}
			if toolButtonWithEnabled(c, "dragon-qa-retry-comment", actionLabel, p, false, !busy) {
				s.requestDragonQAJob(state, dragonQAComment, "")
			}
		}
	})
	if state.result.CommentError != "" {
		label(c, state.result.CommentError, 13, color).Wrap()
	}
}

func dragonQACanOpenURL(link string) bool {
	return ui.SafeToOpen(link) && domain.IsHTTPURL(link)
}

func (s *app) openDragonQALink(c *ui.Context, state *dragonQAViewState, link string) {
	c.OpenURLThen(link, func(err error) {
		if err != nil && s.dragonQACurrent(state) {
			state.err = "浏览器未能打开审批链接。"
		}
	})
}
