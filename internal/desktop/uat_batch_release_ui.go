package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

const uatBatchReleaseLabel = "批量发布"

func (s *app) renderUATBatchActions(c *ui.Context, p palette, history *tagHistoryViewState, group tagHistoryGroup) string {
	scope := s.tagBatchReleaseScope(group.Item.GroupID)
	if group.Item.BatchID == nil || !s.tagBatchReleaseCurrent(scope, history) {
		return ""
	}
	if !s.integrationReady("genbu") {
		return ""
	}
	records := s.tagBatchReleaseRecords(history.TaskID, scope.groupID)
	if len(records) != len(group.Item.Operations) {
		return "批次记录不完整，请刷新 Tag 历史"
	}
	targets, reason := s.tagBatchReleaseTargets(records, scope.groupID)
	if reason == "" && len(targets) == 0 {
		return ""
	}
	disabledReason := reason
	if disabledReason == "" && (s.busy || s.taskServicesUnit.uatWriteRunning()) {
		disabledReason = "有操作正在执行"
	}
	if tagPublishAction(c, p, "uat-batch-release", uatBatchReleaseLabel, disabledReason != "").
		Label(uatBatchReleaseLabel + " · " + group.Item.GroupID).Description(disabledReason).
		Tooltip(firstNonEmpty(disabledReason, "批量发布到 UAT")).Clicked() {
		s.openUATBatchRelease(history, group, targets)
	}
	return reason
}

func (s *app) renderUATBatchDialog(c *ui.Context, p palette) {
	d := s.taskServicesUnit.uatBatchDialog
	if !s.uatBatchDialogCurrent(d) {
		s.closeUATBatchDialog(d)
		return
	}
	title, scope := "UAT 批量发布", fmt.Sprintf("%s · UAT · %d 项", d.title, len(d.targets))
	if d.completed {
		title = "UAT 批量发布结果"
	}
	close := func() { s.closeUATBatchDialog(d) }
	submitting := s.taskServicesUnit.uatBatchSubmission == d
	renderUATActionDialog(c, p, "uat-batch-dialog", title, scope, close, func() {
		if d.failure != "" {
			label(c, d.failure, 12, p.danger).Wrap().Selectable()
		}
		if d.previewRequest != nil {
			label(c, "正在准备发布…", 12, p.muted)
		} else if submitting {
			label(c, "正在提交…", 12, p.muted)
		}
		for i, target := range d.targets {
			outcome := uatBatchRowOutcome{}
			if i < len(d.rows) {
				outcome = d.rows[i]
			}
			renderUATBatchTarget(c.Key(target.item.OperationID), p, target, outcome)
		}
	}, func() {
		ui.Row(c).FillWidth().Shrink(0).Gap(8).AlignItems(ui.Center).Justify(ui.End).Children(func() {
			if d.completed || submitting {
				if ui.Button(c.Key("uat-batch-close"), "关闭").Clicked() {
					close()
				}
				return
			}
			if ui.Button(c.Key("uat-batch-cancel"), "取消").Clicked() {
				close()
			}
			if uatPrimaryButton(c, "uat-batch-submit", uatBatchReleaseLabel, !d.open || s.busy || d.previewRequest != nil || s.taskServicesUnit.uatWriteRunning()) {
				s.previewUATBatchRelease(d)
			}
		})
	})
}

func renderUATBatchTarget(c *ui.Context, p palette, target uatBatchTarget, outcome uatBatchRowOutcome) {
	ui.Column(c).FillWidth().MinWidth(0).Padding(8, 10).Gap(4).Background(p.soft).Radius(8).Children(func() {
		ui.Row(c).FillWidth().MinWidth(0).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
				label(c, target.service, 13, p.text).FontWeight(600).Wrap()
				label(c, target.item.Tag, 12, p.muted).Wrap().Selectable()
			})
			switch outcome.status {
			case "submitted":
				label(c, "已提交", 12, p.green).NoWrap()
			case "not-sent":
				label(c, "未提交", 12, p.danger).NoWrap()
			case "unknown":
				label(c, "结果待确认", 12, p.amber).NoWrap()
			}
		})
		if outcome.detail != "" {
			label(c, outcome.detail, 12, p.muted).Wrap().Selectable()
		}
	})
}
