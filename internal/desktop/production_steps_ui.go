package desktop

import (
	"fmt"
	"slices"
	"strings"

	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
)

type productionStepView struct {
	State, Message string
	Identity       *productionResultIdentity
}

func productionTagPlanCurrent(v *productionView, b *production.Batch) bool {
	if b == nil || b.TagPlan == nil || v.PreparationInvalid {
		return false
	}
	manual := ""
	if b.TagPlan.Preview.Manual {
		manual = b.TagPlan.Preview.ProductionTag
	}
	if v.ManualTag != manual || v.Preview == nil {
		return false
	}
	p, observed := b.TagPlan, v.Preview
	expectedVersions, actualVersions := slices.Clone(p.Preview.ProductionCandidates), slices.Clone(observed.ProductionCandidates)
	slices.Sort(expectedVersions)
	slices.Sort(actualVersions)
	return p.Preview.Target == observed.Target && p.Preview.ProductionTag == observed.ProductionTag &&
		p.Preview.ProductionSHA == observed.ProductionSHA && p.Preview.RuntimeAvailable == observed.RuntimeAvailable &&
		slices.Equal(expectedVersions, actualVersions) && p.MainSHA == observed.MainSHA && p.Commit == observed.ReleaseSHA
}

func productionIntentStep(b *production.Batch) int {
	if b == nil || b.Intent == nil {
		return 0
	}
	switch b.Intent.Kind {
	case "create":
		if b.Intent.Step == "release-create" {
			return 3
		}
		return 2
	case "main":
		if b.InitialSHA == "" {
			return 2
		}
		return 5
	case "release":
		return 5
	case "feature":
		return 4
	case "sync":
		return 5
	case "tag":
		return 6
	}
	return 0
}

func productionActionStep(kind, follow string, b *production.Batch) int {
	if follow == "build-preview" || follow == "build-confirm" {
		return 6
	}
	switch kind {
	case "production.inspect":
		if follow == "sync-preview" || follow == "check-preview" {
			return 5
		}
		return 1
	case "production.create":
		return 3
	case "production.merge-main":
		if b == nil || b.InitialSHA == "" {
			return 2
		}
		return 5
	case "production.merge-release":
		return 5
	case "production.merge", "production.feature-preview":
		return 4
	case "production.sync", "production.reprepare-tag":
		return 5
	case "production.publish":
		return 6
	case "production.refresh-build", "production.reconcile-ended":
		return 6
	case "production.accept-remote":
		return 4
	case "production.open-ide":
		return productionIntentStep(b)
	case "production.resume", "production.rebase-pending", "production.pending-preview":
		return productionIntentStep(b)
	}
	return 0
}

func productionStep(v *productionView, b *production.Batch, step int) productionStepView {
	if observed, ok := v.Steps[step]; ok && !(step == 2 && observed.State != "running" && observed.State != "failed" && observed.State != "warning") {
		return observed
	}
	result := productionStepView{State: "pending"}
	if step == 1 && v.Preview != nil && v.Preview.ProductionSHA != "" {
		result.State = "complete"
	}
	if step == 2 && productionMainIncluded(v.Preview) {
		result.State = "skipped"
	}
	if b == nil {
		return result
	}
	if step == productionIntentStep(b) {
		result.State, result.Message = "failed", b.Message
		if v.Busy {
			result.State = "running"
		}
		return result
	}
	switch step {
	case 2:
		if productionMainIncluded(v.Preview) {
			result.State = "skipped"
		}
	case 3:
		if b.InitialSHA != "" {
			result.State = "complete"
		}
	case 4:
		if len(b.Branches) > 0 {
			result.State = "complete"
		}
	case 5:
		if productionTagPlanCurrent(v, b) {
			result.State = "complete"
		} else if len(b.Tags) > 0 && b.ReleaseSHA == b.LastTagCommit() {
			result.State = "complete"
		}
	case 6:
		if productionTagPlanCurrent(v, b) {
			result.State = "prepared"
		} else if len(b.Tags) > 0 && b.ReleaseSHA == b.LastTagCommit() {
			result.State = "complete"
		}
	}
	return result
}

func productionStepHeading(c *ui.Context, p palette, v *productionView, b *production.Batch, step int, title string) {
	state := productionStep(v, b, step)
	text := map[string]string{"pending": "待执行", "running": "进行中…", "complete": "已完成", "skipped": "无需合并", "failed": "已暂停", "prepared": "待确认"}[state.State]
	color := p.muted
	if state.State == "complete" || state.State == "skipped" {
		color = p.green
	} else if state.State == "running" || state.State == "prepared" {
		color = p.blue
	} else if state.State == "failed" {
		color = p.amber
	}
	ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
		label(c, fmt.Sprintf("%d. %s", step, title), 14, p.text).FontWeight(600).Wrap()
		label(c, text, 11, color).NoWrap()
	})
}

func productionChoiceLabel(choice productionServiceChoice, grouped bool) string {
	if grouped {
		return choice.GroupName + " · " + choice.Name
	}
	return choice.Name
}

func productionHasMultipleGroups(choices []productionServiceChoice) bool {
	groups := map[string]bool{}
	for _, choice := range choices {
		groups[choice.GroupID] = true
	}
	return len(groups) > 1
}

func (s *app) renderProductionInitialization(c *ui.Context, p palette, v *productionView, b *production.Batch) {
	productionStepHeading(c, p, v, b, 1, "对比生产 Tag 与主分支差异")
	if v.Preview != nil {
		s.renderProductionBaseline(c, p, v)
	} else {
		label(c, "检查线上运行版本及远端 Git Tag，再核对与主分支的差异。", 12, p.muted).Wrap()
	}
	s.renderProductionCheckActions(c, p, v)
	s.renderProductionSectionFeedback(c, p, s.productionSection(v, b, 1))
	productionStepHeading(c, p, v, b, 2, "合并生产 Tag 到主分支")
	s.renderProductionMainMerge(c, p, v, b, "initial")
	s.renderProductionSectionFeedback(c, p, s.productionSection(v, b, 2))
	if productionIntentStep(b) == 2 {
		s.renderProductionRecovery(c, p, v, *b)
	}
	productionStepHeading(c, p, v, b, 3, "创建 release 分支")
	if b == nil || b.InitialSHA == "" {
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			branchInput := &v.Version
			if b != nil && b.Intent != nil && b.Intent.Step == "release-create" {
				value := b.ReleaseBranch
				branchInput = &value
			}
			ui.TextInput(c.Key("production-version"), branchInput).Grow(1).MinWidth(0).Height(36).Placeholder("release/2.3.44").Label("release 分支名").ReadOnly(v.Busy || b != nil && b.Intent != nil)
			if productionButton(c, "production-create", "创建 release", p, true, v.Busy || b != nil && b.Intent != nil || !productionMainIncluded(v.Preview)) {
				v.ActiveStep = 3
				s.confirmProductionCreate(v)
			}
		})
		label(c, "自动计算分支名，可修改版本部分；确认后仅创建并推送该分支。", 12, p.muted).Wrap()
		if productionIntentStep(b) == 3 {
			s.renderProductionRecovery(c, p, v, *b)
		}
	} else {
		label(c, b.ReleaseBranch, 12, p.muted).Wrap()
	}
	s.renderProductionSectionFeedback(c, p, s.productionSection(v, b, 3))
}

func (s *app) renderProductionMainMerge(c *ui.Context, p palette, v *productionView, b *production.Batch, key string) {
	main := "主分支"
	if v.Preview != nil {
		main = v.Preview.Target.MainBranch
	} else if b != nil {
		main = b.Target.MainBranch
	}
	contained := productionMainIncluded(v.Preview)
	text := "合并生产 Tag 到 " + main
	if contained {
		label(c, main+" 已包含生产 Tag "+v.Preview.ProductionTag+"。", 12, p.text).Wrap()
		return
	}
	if productionButton(c, "production-main-"+key, text, p, true, v.Busy || v.Preview == nil || v.Preview.ProductionSHA == "" || v.Preview.MainSHA == "" || b != nil && b.Intent != nil) {
		v.ActiveStep = 2
		s.confirmProductionMain(v)
	}
}

func (s *app) renderProductionCheckActions(c *ui.Context, p palette, v *productionView) {
	known := v.Preview != nil && v.Preview.ProductionSHA != "" && v.Preview.MainSHA != ""
	diffText, diffPalette := "查看差异", p
	if known {
		diffText = "无差异"
		if productionBaselineHasChanges(v.Preview) {
			diffText = "查看差异"
			diffPalette.surface, diffPalette.text = p.amberBg, p.amber
		}
	}
	ui.Row(c).FillWidth().Gap(8).Wrap().Children(func() {
		if productionButton(c, "production-inspect", "重新检查", p, false, v.Busy) {
			s.productionInspect("")
		}
		if productionButton(c, "production-diff-toggle", diffText, diffPalette, false, !known) {
			preview := v.Preview
			s.openProductionDiff(v, "生产 Tag → 主分支", preview.ProductionTag, preview.Target.Remote+"/"+preview.Target.MainBranch, preview.ProductionSHA, preview.MainSHA, true)
		}
	})
}

func productionBaselineHasChanges(preview *production.Preview) bool {
	if preview == nil || productionMainIncluded(preview) || preview.ProductionSHA == preview.MainSHA {
		return false
	}
	// The incoming Git stat is the first section of the existing report.
	// InspectBatch may append a separate remote-release report after a blank line;
	// those changes belong to another comparison, not this button.
	stat, _, _ := strings.Cut(preview.Diff, "\n\n")
	return strings.TrimSpace(stat) != ""
}

func productionMainIncluded(preview *production.Preview) bool {
	return preview != nil && preview.ProductionSHA != "" && preview.MainSHA != "" && preview.ProductionInMain
}

func productionReleaseIncluded(preview *production.Preview) bool {
	return preview != nil && preview.MainSHA != "" && preview.ReleaseSHA != "" && preview.MainInRelease
}
