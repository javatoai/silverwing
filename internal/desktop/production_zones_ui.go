package desktop

import (
	"fmt"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
)

func productionZone(c *ui.Context, p palette, title string, body func()) {
	ui.Column(c).FillWidth().MinWidth(0).Gap(12).Padding(16, 0, 4).Children(func() {
		ui.Box(c).FillWidth().Height(1).Background(p.line)
		label(c, title, 16, p.text).FontWeight(650).Padding(4, 0, 0)
		body()
	})
}

func productionDisclosure(c *ui.Context, p palette, key, title string, open *bool, body func()) {
	d := ui.CollapsibleBase(c.Key(key), open)
	d.Trigger.AlignSelf(ui.Start).MinHeight(26).Padding(2, 4).Radius(6).Gap(6).AlignItems(ui.Center).Label(title)
	if d.Trigger.Hovered() {
		d.Trigger.Background(p.soft)
	}
	if d.Trigger.Focused() {
		d.Trigger.Border(1, p.blue)
	}
	d.Trigger.Children(func() {
		icon(c, map[bool]string{true: "chevron", false: "chevron-right"}[*open], 12, p.muted)
		label(c, title, 12, p.muted).NoWrap()
	})
	d.Panel(body).FillWidth().MinWidth(0).Gap(8).Padding(4, 0)
}

func (s *app) renderProductionSectionFeedback(c *ui.Context, p palette, result productionSectionPresentation) {
	if result.Message != "" && result.Message != result.Activity {
		color := p.muted
		switch result.State {
		case "failed":
			color = p.danger
		case "warning":
			color = p.amber
		case "skipped":
			color = p.text
		}
		label(c, result.Message, 12, color).Wrap().Selectable()
	}
}

func (s *app) renderProductionRelations(c *ui.Context, p palette, v *productionView, b production.Batch, blocked bool) {
	for _, relation := range productionRelations(v.Preview, b) {
		if relation.Included || !relation.Known {
			continue
		}
		ui.Row(c.Key("production-relation-" + relation.Key)).FillWidth().MinWidth(0).Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
			label(c, relation.Label, 12, p.text).Width(300).MaxWidthPercent(100).MinWidth(0).SingleLine().Tooltip(relation.Label)
			state := "待检查"
			if relation.Known {
				state = "需要合并"
				if relation.Included {
					state = "无需合并"
				}
			}
			label(c, state, 12, p.muted).NoWrap().Tooltip(relation.Explanation)
			if productionButton(c, "production-relation-diff-"+relation.Key, relation.DiffLabel, p, false, !relation.Known) {
				s.openProductionDiff(v, relation.Label, relation.Source, relation.Target, relation.SourceSHA, relation.TargetSHA, true)
			}
			if !relation.Included {
				if productionButton(c, "production-relation-merge-"+relation.Key, relation.MergeLabel, p, false, blocked || !relation.Known || relation.NeedsMainSync) {
					v.ActiveStep = 5
					if relation.Key == "main" {
						s.confirmProductionMain(v)
					} else {
						s.confirmProductionSync(v, *v.Preview)
					}
				}
			}
		})
		if relation.NeedsMainSync && !relation.Included && relation.Known {
			label(c, "先将生产 Tag 合入 "+b.Target.MainBranch+"，再同步 release。", 11, p.muted).Wrap()
		}
	}
}

func (s *app) renderProductionRecentTag(c *ui.Context, p palette, v *productionView, b production.Batch) {
	if len(b.Tags) == 0 {
		return
	}
	latest := b.Tags[len(b.Tags)-1]
	ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
		label(c, "最近 Tag  "+latest.Name, 12, p.text).Tooltip(latest.Commit)
		compactState(c, "构建 "+productionStage(latest.Status.Build), p)
		if productionButton(c, "production-refresh-build", "刷新构建状态", p, false, v.Busy) {
			s.productionRequestFor(v, application.Action{Kind: "production.refresh-build", Payload: map[string]any{"batchId": b.ID}}, "")
		}
	})
	if latest.Status.FailureReason != nil {
		label(c, *latest.Status.FailureReason, 11, p.amber).Wrap()
	}
	if latest.Status.Build == domain.GenbuFailed {
		label(c, "请到平台重新编译此 Tag，再刷新构建状态。", 11, p.muted).Wrap()
	}
	if len(b.Tags) > 1 {
		productionDisclosure(c, p, "production-tag-history", fmt.Sprintf("之前的 Tag · %d 个", len(b.Tags)-1), &v.TagHistoryExpanded, func() {
			ui.Scroll(c).FillWidth().MaxHeight(160).Gap(8).Children(func() {
				for i := len(b.Tags) - 2; i >= 0; i-- {
					tag := b.Tags[i]
					ui.Row(c).FillWidth().Gap(8).Wrap().AlignItems(ui.Center).Children(func() {
						label(c, tag.Name+" · "+shortProductionSHA(tag.Commit), 12, p.muted).Tooltip(tag.Commit)
						compactState(c, "构建 "+productionStage(tag.Status.Build), p)
					})
					if tag.Status.FailureReason != nil {
						label(c, *tag.Status.FailureReason, 11, p.amber).Wrap()
					}
				}
			})
		})
	}
}

func (s *app) renderProductionHistory(c *ui.Context, p palette, v *productionView) {
	var history []production.Batch
	for _, b := range s.production.batches {
		if b.Target.Selection == v.Selection && b.ID != v.BatchID {
			history = append(history, b)
		}
	}
	if len(history) == 0 {
		return
	}
	ui.Box(c).FillWidth().Height(1).Background(p.line)
	productionDisclosure(c, p, "production-batch-history", fmt.Sprintf("历史批次 · %d 个", len(history)), &v.HistoryExpanded, func() {
		for _, batch := range history {
			ui.Row(c).FillWidth().Gap(10).Wrap().AlignItems(ui.Center).Children(func() {
				label(c, firstNonEmpty(batch.ReleaseBranch, "初始化 · 等待创建 release"), 12, p.text).SingleLine().Tooltip(batch.ReleaseBranch)
				label(c, productionPhase(batch.Phase), 11, p.muted)
				if productionButton(c, "production-history-"+batch.ID, "查看", p, false, v.Busy) {
					v.selectProductionBatch(batch.ID)
				}
			})
		}
	})
}
