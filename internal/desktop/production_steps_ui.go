package desktop

import (
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
	if follow == "build-preview" || follow == "build-publish" {
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
	case "production.resume", "production.rebase-pending", "production.pending-preview", "production.abort-merge":
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
	pending := productionIntentStep(b)
	productionCard(c, p, "production-initialization-baseline", "生产 Tag → 主分支", s.productionBaselineStatus(v, b), func() {
		if v.Preview != nil {
			s.renderProductionBaseline(c, p, v)
		} else {
			label(c, "检查线上运行版本及远端 Git Tag，再核对与主分支的差异。", 12, p.muted).Wrap()
		}
		feedbackStep := 1
		if v.ActiveStep == 2 || pending == 2 && v.ActiveStep != 1 {
			feedbackStep = 2
		}
		s.renderProductionSectionFeedback(c, p, s.productionSection(v, b, feedbackStep))
		if pending == 2 {
			s.renderProductionRecoveryDetails(c, p, v, *b)
		}
	}, func() {
		s.renderProductionCheckButtons(c, p, v)
		if pending == 2 {
			s.renderProductionRecoveryActions(c, p, v, *b)
		} else if pending != 3 || productionMainIncluded(v.Preview) {
			s.renderProductionMainMerge(c, p, v, b, "initial")
		}
	})

	var releaseActions func()
	if pending == 3 {
		releaseActions = func() { s.renderProductionRecoveryActions(c, p, v, *b) }
	} else if b == nil || b.InitialSHA == "" {
		releaseActions = func() {
			if productionButton(c, "production-create", "创建 release", p, true, v.Busy || b != nil && b.Intent != nil || !productionMainIncluded(v.Preview)) {
				v.ActiveStep = 3
				s.confirmProductionCreate(v)
			}
		}
	}
	productionCard(c, p, "production-initialization-release", "创建 release 分支", s.productionInitializationStepStatus(v, b, 3), func() {
		if b == nil || b.InitialSHA == "" {
			branchInput := &v.Version
			if b != nil && b.Intent != nil && b.Intent.Step == "release-create" {
				value := b.ReleaseBranch
				branchInput = &value
			}
			ui.TextInput(c.Key("production-version"), branchInput).Width(480).MaxWidthPercent(100).MinWidth(0).Height(36).Placeholder("release/2.3.44").Label("release 分支名").Tooltip("可修改版本部分").ReadOnly(v.Busy || b != nil && b.Intent != nil)
			if pending == 3 {
				s.renderProductionRecoveryDetails(c, p, v, *b)
			}
		} else {
			label(c, b.ReleaseBranch, 13, p.text).FillWidth().MinWidth(0).Wrap().Selectable().Tooltip(b.ReleaseBranch).Label("production-release-result值")
		}
		s.renderProductionSectionFeedback(c, p, s.productionSection(v, b, 3))
	}, releaseActions)
}

func (s *app) renderProductionMainMerge(c *ui.Context, p palette, v *productionView, b *production.Batch, key string) {
	if b != nil && productionHasConflict(*b) && productionIntentStep(b) == 2 {
		return
	}
	main := "主分支"
	if v.Preview != nil {
		main = v.Preview.Target.MainBranch
	} else if b != nil {
		main = b.Target.MainBranch
	}
	contained := productionMainIncluded(v.Preview)
	text := "合并生产 Tag 到 " + main
	if contained {
		productionButton(c, "production-main-"+key, "无需合并", p, false, true)
		return
	}
	if productionButton(c, "production-main-"+key, text, p, true, v.Busy || v.Preview == nil || v.Preview.ProductionSHA == "" || v.Preview.MainSHA == "" || b != nil && b.Intent != nil) {
		v.ActiveStep = 2
		s.confirmProductionMain(v)
	}
}

func (s *app) renderProductionCheckActions(c *ui.Context, p palette, v *productionView) {
	ui.Row(c).FillWidth().Gap(8).Wrap().Children(func() { s.renderProductionCheckButtons(c, p, v) })
}

func (s *app) renderProductionCheckButtons(c *ui.Context, p palette, v *productionView) {
	known := v.Preview != nil && v.Preview.ProductionSHA != "" && v.Preview.MainSHA != ""
	if v.ActiveStep == 1 && s.productionSection(v, s.productionBatch(v), 1).State == "failed" {
		known = false
	}
	changed := known && productionBaselineHasChanges(v.Preview)
	diffText, diffPalette := "查看差异", p
	if known {
		diffText = "无差异"
		if changed {
			diffText = "查看差异"
			diffPalette.surface, diffPalette.text = p.amberBg, p.amber
		}
	}
	if productionButton(c, "production-inspect", "重新检查", p, false, v.Busy) {
		s.productionInspect("")
	}
	if productionButton(c, "production-diff-toggle", diffText, diffPalette, false, !changed) {
		preview := v.Preview
		s.openProductionDiff(v, "生产 Tag → 主分支", preview.ProductionTag, preview.Target.Remote+"/"+preview.Target.MainBranch, preview.ProductionSHA, preview.MainSHA, true)
	}
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
