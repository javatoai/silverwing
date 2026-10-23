package desktop

import (
	"strings"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
)

func productionTagRecordText(b production.Batch, tag production.Tag) string {
	return firstNonEmpty(strings.TrimSpace(b.Target.ServiceName), b.Target.ServiceID) + "：" + tag.Name
}

func (s *app) productionDisplayedBatchCurrent(v *productionView, b production.Batch) bool {
	if v == nil || s.page != "production" || s.productionView() != v || v.BatchID != b.ID ||
		s.production.loadedHome != s.snapshot.Home || s.production.configScope != s.currentProductionScope() {
		return false
	}
	current := s.productionBatch(v)
	return current != nil && current.ID == b.ID && current.Target == b.Target
}

func productionTagBuildLabel(stage domain.GenbuStageStatus) string {
	switch stage {
	case domain.GenbuSuccess:
		return "构建成功"
	case domain.GenbuFailed:
		return "构建失败"
	case domain.GenbuBuilding:
		return "构建中"
	case domain.GenbuInitial:
		return "等待构建"
	default:
		return "构建状态待查询"
	}
}

func (s *app) renderProductionTagRecords(c *ui.Context, p palette, v *productionView, b production.Batch) {
	if len(b.Tags) == 0 || v == nil {
		return
	}
	ui.Column(c.Key("production-tag-records-" + b.ID)).FillWidth().MaxWidth(600).AlignSelf(ui.Start).MinWidth(0).Gap(12).Label("Tag 记录").Children(func() {
		for _, tag := range b.Tags {
			s.renderProductionTagRecord(c, p, v, b, tag)
		}
	})
}

func (s *app) renderProductionBuildRefresh(c *ui.Context, p palette, v *productionView, b production.Batch) {
	if len(b.Tags) == 0 || v == nil {
		return
	}
	if productionButton(c, "production-refresh-build", "刷新构建状态", p, false, v.Busy) && s.productionDisplayedBatchCurrent(v, b) {
		s.productionRequestFor(v, application.Action{Kind: "production.refresh-build", Payload: map[string]any{"batchId": b.ID}}, "")
	}
}

func (s *app) renderProductionTagRecord(c *ui.Context, p palette, v *productionView, b production.Batch, tag production.Tag) {
	text := productionTagRecordText(b, tag)
	ui.Column(c.Key("production-tag-record-" + b.ID + "-" + tag.Name)).FillWidth().MinWidth(0).Gap(8).Children(func() {
		ui.Row(c).FillWidth().MinWidth(0).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
			timestamp := "时间未知"
			if pushed, err := time.Parse(time.RFC3339Nano, tag.PushedAt); err == nil {
				timestamp = pushed.Local().Format("2006-01-02 15:04")
			}
			ui.Box(c).Shrink(0).Label("Tag 推送时间 " + tag.Name).Children(func() {
				label(c, timestamp, 11, p.muted).NoWrap().Tooltip("Tag 推送时间")
			})
			ui.Box(c).MinWidth(0).Label("Tag 服务与版本 " + tag.Name).Children(func() {
				label(c, text, 14, p.text).FontWeight(550).MinWidth(0).MinHeight(28).Padding(4, 0).Wrap().Selectable()
			})
			ui.Row(c).Shrink(0).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Row(c).Shrink(0).AlignItems(ui.Center).Label("Tag 构建状态 " + tag.Name).Children(func() {
					compactState(c, productionTagBuildLabel(tag.Status.Build), p)
				})
				if tagRecordAction(c, p, "production-copy-tag-"+tag.Name, "copy", "复制", false).Label("复制 Tag 记录 "+tag.Name).Tooltip("复制 "+text).Clicked() && s.productionDisplayedBatchCurrent(v, b) {
					s.copyTextToClipboard(c.Services(), text, "Tag 记录已复制")
				}
			})
		})
		if tag.Status.FailureReason != nil {
			label(c, *tag.Status.FailureReason, 11, p.amber).Wrap()
		}
	})
}
