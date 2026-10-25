package desktop

import (
	"encoding/hex"
	"strings"

	"silverwing-go/internal/production"
)

// Presentation results live only in this process. Complete identities remain
// available for staleness checks; display strings are never execution inputs.
type productionResultIdentity struct {
	Selection                             production.Selection
	BatchID, Branch, SourceSHA, TargetSHA string
	Home, ConfigScope, ManualTag          string
	Generation                            uint64
}

type productionSectionPresentation struct {
	State, Message, Activity string
}

type productionReleasePresentation struct {
	Merge, Build       productionSectionPresentation
	Ready              bool
	BuildReason        string
	CanBuild, ShowSync bool
}

type productionRelationPresentation struct {
	Key, Label, Explanation, DiffLabel, MergeLabel string
	Source, Target, SourceSHA, TargetSHA           string
	Known, Included, NeedsMainSync                 bool
}

func productionRelations(p *production.Preview, b production.Batch) []productionRelationPresentation {
	main := b.Target.MainBranch
	prod := "生产 Tag"
	if p != nil && p.ProductionTag != "" {
		prod += " " + p.ProductionTag
	}
	rows := []productionRelationPresentation{
		{Key: "main", Label: prod + " → " + main, Explanation: main + " 已包含生产 Tag 的提交", DiffLabel: "查看生产 Tag 差异", MergeLabel: "合并生产 Tag 到 " + main, Source: prod, Target: b.Target.Remote + "/" + main},
		{Key: "release", Label: main + " → " + b.ReleaseBranch, Explanation: b.ReleaseBranch + " 已包含主分支的提交", DiffLabel: "查看主分支差异", MergeLabel: "合并主分支到 release", Source: b.Target.Remote + "/" + main, Target: b.Target.Remote + "/" + b.ReleaseBranch},
	}
	if p != nil {
		rows[0].SourceSHA, rows[0].TargetSHA = p.ProductionSHA, p.MainSHA
		rows[0].Known, rows[0].Included = p.ProductionSHA != "" && p.MainSHA != "", productionMainIncluded(p)
		rows[1].SourceSHA, rows[1].TargetSHA = p.MainSHA, p.ReleaseSHA
		rows[1].Known, rows[1].Included = p.MainSHA != "" && p.ReleaseSHA != "", productionReleaseIncluded(p)
		rows[1].NeedsMainSync = !productionMainIncluded(p)
	}
	for index := range rows {
		if !rows[index].Known {
			rows[index].Explanation = "构建时将自动读取完整的远端提交。"
		} else if !rows[index].Included {
			rows[index].Explanation = "来源仍有未合入的提交。"
		}
	}
	return rows
}

func (s *app) productionSection(v *productionView, b *production.Batch, step int) productionSectionPresentation {
	view := *v
	if observed, exists := v.Steps[step]; exists && observed.Identity != nil {
		i := observed.Identity
		if i.Selection != v.Selection || i.BatchID != v.BatchID || i.Home != s.snapshot.Home || i.ConfigScope != s.currentProductionScope() || i.ManualTag != v.ManualTag || i.Generation > v.Generation || (step == 4 && i.Branch != v.Branch) {
			if step == 4 && productionIntentStep(b) != 4 {
				return productionSectionPresentation{}
			}
			view.Steps = nil
		}
	}
	state := productionStep(&view, b, step)
	result := productionSectionPresentation{State: state.State, Message: state.Message}
	if step == 1 && state.State == "complete" {
		// The heading already communicates success; keep activity and errors below.
		result.Message = ""
	}
	if state.State == "skipped" && result.Message == "已包含，无需合并" {
		// The card's completed state and inclusion tooltip replace this echo.
		result.Message = ""
	}
	if v.Busy && v.ActiveStep == step {
		result.Activity = productionActivity(v.ActiveAction)
		if state.State == "running" {
			// A single, precise activity line replaces generic and progress echoes.
			result.Message = ""
			if state.Message != "" {
				result.Activity = state.Message
			}
		}
	}
	if !v.Busy && v.Error != "" && v.ActiveStep == step && result.Message == "" {
		result.State, result.Message = "failed", v.Error
	}
	if b != nil && productionHasConflict(*b) && productionIntentStep(b) == step {
		switch result.Message {
		case "合并发生冲突，请在 IDE 中处理并提交", "请在 IDE 中解决冲突并完成 merge commit，再校验并继续":
			// The recovery panel names both refs once. Preserve other errors,
			// including failed aborts, IDE launches and changed repositories.
			result.Message = ""
		}
	}
	return result
}

func (s *app) productionReleaseModel(v *productionView, b *production.Batch) productionReleasePresentation {
	m := productionReleasePresentation{Merge: s.productionSection(v, b, 4), Build: s.productionSection(v, b, 6), Ready: productionTagPlanCurrent(v, b)}
	if v.ActiveStep == 5 || productionIntentStep(b) == 5 {
		m.Build = s.productionSection(v, b, 5)
	}
	// Builds do not require feature history or a changed commit. The action
	// always reads again; unresolved operations still block Tag preparation.
	m.CanBuild = b.InitialSHA != "" && !b.Ended && b.Intent == nil && !v.Busy
	m.ShowSync = v.BuildChecked && v.Preview != nil && (!productionMainIncluded(v.Preview) || !productionReleaseIncluded(v.Preview))
	if !m.Ready {
		m.BuildReason = "构建时自动检查线上版本和提交，通过后推送 Tag。"
		if v.Busy && v.ActiveAction == "production.abort-merge" {
			m.BuildReason = "请等待当前合并撤回完成。"
		} else if b.Phase == "ABORTING" && b.Intent != nil {
			m.BuildReason = "请先完成当前合并的撤回核对。"
		} else if productionHasConflict(*b) {
			m.BuildReason = "请先解决或撤回当前合并。"
		} else if b.Intent != nil {
			m.BuildReason = "请先完成当前未结束的操作。"
		} else if m.ShowSync {
			m.BuildReason = "请完成下方需要的同步，再点击构建 Tag。"
		}
	}
	return m
}

func productionActivity(kind string) string {
	switch kind {
	case "production.feature-preview":
		return "正在检查所选分支的提交…"
	case "production.merge":
		return "正在合并分支并推送…"
	case "production.abort-merge":
		return "正在撤回本次合并…"
	case "production.merge-main":
		return "正在同步生产 Tag 到主分支…"
	case "production.merge-release":
		return "正在同步主分支到 release…"
	case "production.create":
		return "正在创建并推送 release…"
	case "production.publish":
		return "正在创建并推送生产 Tag…"
	case "production.prepare-tag":
		return "正在读取远端版本并准备生产 Tag…"
	case "production.refresh-build":
		return "正在读取 Tag 构建状态…"
	case "production.list":
		return "正在读取发布批次…"
	case "production.end":
		return "正在结束当前批次…"
	case "production.delete":
		return "正在删除本地批次记录…"
	case "production.accept-remote":
		return "正在校验并采纳远端 release…"
	case "production.reconcile-ended":
		return "正在核对远端结果…"
	case "production.open-ide":
		return "正在打开冲突工作区…"
	case "production.resume", "production.rebase-pending":
		return "正在校验并继续当前操作…"
	default:
		return "正在检查线上版本及远端提交…"
	}
}

func productionBranchWrite(kind string) bool {
	switch kind {
	case "production.create", "production.merge-main", "production.merge-release", "production.merge", "production.resume", "production.rebase-pending":
		return true
	}
	return false
}

func productionFullSHA(sha string) bool {
	if len(sha) != 40 && len(sha) != 64 {
		return false
	}
	_, err := hex.DecodeString(sha)
	return err == nil
}

func productionFeatureIncluded(b production.Batch, p production.Preview, branch string) bool {
	return branch != "" && branch != b.ReleaseBranch && branch != b.Target.MainBranch && p.Target == b.Target && productionFullSHA(p.ReleaseSHA) && productionFullSHA(p.BranchHeads[branch]) && strings.TrimSpace(p.Commits) == ""
}

func (v *productionView) productionBranchChanged() {
	delete(v.Steps, 4)
	if v.ActiveStep == 4 && !v.Busy {
		v.Error, v.Message = "", ""
	}
}

func (v *productionView) invalidateProductionBranchResult(p *production.Preview) {
	result := v.Steps[4]
	if i := result.Identity; i != nil && ((i.TargetSHA != "" && p.ReleaseSHA != i.TargetSHA) || (i.SourceSHA != "" && p.BranchHeads[i.Branch] != i.SourceSHA)) {
		delete(v.Steps, 4)
	}
}

func (v *productionView) selectProductionBatch(id string) {
	v.BatchID, v.Preview, v.Steps = id, nil, nil
	v.Error, v.Message, v.Branch = "", "", ""
	v.PreparationInvalid = true
	v.BuildChecked = false
	v.ActiveStep, v.ActiveAction = 0, ""
}
