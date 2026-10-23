package desktop

import (
	"strings"

	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
)

type productionCardStatus struct{ Text, State, Tooltip string }

const (
	productionInfoLabelWidth  = 88
	productionInfoColumnGap   = 12
	productionCardStatusWidth = 112
)

// Information rows share one value column; wrapping never moves the label
// away from the first line of its value.
func productionInfoRow(c *ui.Context, p palette, key, name, value, tooltip string) {
	ui.Row(c.Key(key)).FillWidth().MinWidth(0).MinHeight(20).Gap(productionInfoColumnGap).AlignItems(ui.Start).Label(key).Children(func() {
		ui.Box(c).Width(productionInfoLabelWidth).Shrink(0).Label(key + "标签").Children(func() {
			label(c, name, 13, p.muted).NoWrap()
		})
		ui.Box(c).Grow(1).MinWidth(0).Label(key + "值").Children(func() {
			label(c, value, 13, p.text).FillWidth().MinWidth(0).Wrap().Selectable().Tooltip(firstNonEmpty(tooltip, value))
		})
	})
}

func productionCard(c *ui.Context, p palette, key, title string, status productionCardStatus, body, actions func()) {
	ui.Column(c.Key(key)).FillWidth().MinWidth(0).Padding(16).Gap(16).
		Background(p.surface).Border(1, p.line).Radius(12).Label(title + "卡片").Children(func() {
		ui.Row(c).FillWidth().MinWidth(0).Gap(12).AlignItems(ui.Center).Label(title + "标题行").Children(func() {
			label(c, title, 16, p.text).FontWeight(600).Grow(1).MinWidth(0).Wrap()
			color := p.muted
			switch status.State {
			case "complete", "skipped":
				color = p.green
			case "running", "prepared", "ready":
				color = p.blue
			case "warning":
				color = p.amber
			case "failed":
				color = p.danger
			}
			ui.Row(c).Width(productionCardStatusWidth).Shrink(0).Justify(ui.End).Label(title + "状态").Tooltip(status.Tooltip).Children(func() {
				label(c, status.Text, 12, color).NoWrap()
			})
		})
		if body != nil {
			body()
		}
		if actions != nil {
			ui.Row(c).FillWidth().MinWidth(0).Gap(8).Wrap().AlignItems(ui.Center).Justify(ui.End).
				Label(title + "操作栏").Children(actions)
		}
	})
}

// A card describes this operation, never an older branch or Tag's build result.
func (s *app) productionCardActivity(v *productionView, b *production.Batch, steps ...int) (productionCardStatus, bool) {
	for _, step := range steps {
		if v.Busy && v.ActiveStep == step {
			text := "进行中…"
			switch v.ActiveAction {
			case "production.inspect", "production.feature-preview":
				text = "检查中…"
			case "production.publish":
				text = "推送中…"
			case "production.refresh-build":
				text = "刷新中…"
			case "production.abort-merge":
				text = "撤回中…"
			}
			return productionCardStatus{Text: text, State: "running"}, true
		}
		if b != nil && b.Intent != nil && productionIntentStep(b) == step {
			if b.Phase == "ABORTING" {
				return productionCardStatus{Text: "待核对撤回", State: "warning"}, true
			}
			if productionHasConflict(*b) {
				return productionCardStatus{Text: "等待解决冲突", State: "warning"}, true
			}
			return productionCardStatus{Text: "待继续", State: "warning"}, true
		}
		if result := s.productionSection(v, b, step); v.ActiveStep == step {
			if result.State == "failed" || result.State == "warning" {
				return productionCardStatus{Text: "需要处理", State: result.State}, true
			}
		}
	}
	return productionCardStatus{}, false
}

func (s *app) productionInitializationStepStatus(v *productionView, b *production.Batch, step int) productionCardStatus {
	if status, ok := s.productionCardActivity(v, b, step); ok {
		return status
	}
	// Completion is derived from this step's current evidence, not another
	// step's previous success or the batch's overall initialization history.
	if step == 3 {
		if b != nil && b.InitialSHA != "" {
			return productionCardStatus{Text: "已完成", State: "complete"}
		}
		return productionCardStatus{Text: "待执行", State: "pending"}
	}
	if v.Preview == nil || v.Preview.ProductionSHA == "" || v.Preview.MainSHA == "" {
		return productionCardStatus{Text: "待检查", State: "pending"}
	}
	if step == 1 {
		return productionCardStatus{Text: "已完成", State: "complete"}
	}
	if productionMainIncluded(v.Preview) {
		return productionCardStatus{Text: "已完成", State: "skipped", Tooltip: v.Preview.Target.MainBranch + " 已包含生产 Tag " + v.Preview.ProductionTag + "。"}
	}
	return productionCardStatus{Text: "待执行", State: "pending"}
}

func (s *app) productionBaselineStatus(v *productionView, b *production.Batch) productionCardStatus {
	if status, ok := s.productionCardActivity(v, b, 1, 2); ok {
		return status
	}
	status := s.productionInitializationStepStatus(v, b, 2)
	if status.State == "pending" && status.Text == "待执行" {
		status.Text = "待合并"
	}
	return status
}

func (s *app) productionMergeStatus(v *productionView, b *production.Batch) productionCardStatus {
	if status, ok := s.productionCardActivity(v, b, 4); ok {
		return status
	}
	if _, observed := v.Steps[4]; observed {
		switch s.productionSection(v, b, 4).State {
		case "skipped":
			return productionCardStatus{Text: "已完成", State: "skipped", Tooltip: b.ReleaseBranch + " 已包含 " + v.Branch + " 的提交。"}
		case "complete":
			return productionCardStatus{Text: "已完成", State: "complete"}
		}
	}
	if strings.TrimSpace(v.Branch) == "" {
		return productionCardStatus{Text: "待选择分支", State: "pending"}
	}
	return productionCardStatus{Text: "待合并", State: "pending"}
}

func (s *app) productionBuildStatus(v *productionView, b *production.Batch, m productionReleasePresentation) productionCardStatus {
	if status, ok := s.productionCardActivity(v, b, 5, 6); ok {
		return status
	}
	if b.Intent != nil {
		return productionCardStatus{Text: "待处理", State: "warning"}
	}
	if v.Busy {
		return productionCardStatus{Text: "等待当前操作", State: "pending"}
	}
	if m.ShowSync {
		return productionCardStatus{Text: "待同步", State: "warning"}
	}
	if m.Ready {
		return productionCardStatus{Text: "准备就绪", State: "prepared"}
	}
	return productionCardStatus{Text: "可构建", State: "ready"}
}
