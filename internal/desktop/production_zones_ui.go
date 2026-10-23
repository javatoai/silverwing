package desktop

import (
	"fmt"

	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
)

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
		ui.Column(c.Key("production-relation-" + relation.Key)).FillWidth().MinWidth(0).Gap(8).Label("同步关系 " + relation.Key).Children(func() {
			ui.Row(c).FillWidth().MinWidth(0).Gap(12).AlignItems(ui.Start).Children(func() {
				label(c, relation.Label, 12, p.text).Grow(1).MinWidth(0).Wrap().Tooltip(relation.Label)
				label(c, "需要合并", 12, p.muted).Shrink(0).NoWrap().Tooltip(relation.Explanation)
			})
			if relation.NeedsMainSync {
				label(c, "先将生产 Tag 合入 "+b.Target.MainBranch+"，再同步 release。", 11, p.muted).Wrap()
			}
			ui.Row(c).FillWidth().MinWidth(0).Gap(8).Wrap().AlignItems(ui.Center).Justify(ui.End).Label("同步操作 " + relation.Key).Children(func() {
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
			ui.Row(c).FillWidth().MinWidth(0).Gap(12).AlignItems(ui.Center).Children(func() {
				label(c, firstNonEmpty(batch.ReleaseBranch, "初始化 · 等待创建 release"), 12, p.text).Grow(1).MinWidth(0).Wrap().Tooltip(batch.ReleaseBranch)
				label(c, productionPhase(batch.Phase), 12, p.muted).Width(96).Shrink(0).NoWrap()
				if productionButton(c, "production-history-"+batch.ID, "查看", p, false, v.Busy) {
					v.selectProductionBatch(batch.ID)
				}
			})
		}
	})
}
