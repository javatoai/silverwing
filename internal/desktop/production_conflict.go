package desktop

import (
	"fmt"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/production"
)

func productionConflictRefs(b production.Batch) (source, target string) {
	if b.Intent == nil || b.Intent.Merge == nil {
		return "", ""
	}
	i := b.Intent
	target = i.Merge.TargetBranch
	switch i.Step {
	case "main":
		source = "生产 Tag " + firstNonEmpty(i.Preview.ProductionTag, b.ProductionTag, "（已确认）")
	case "release-sync":
		source = b.Target.MainBranch
	default:
		source = firstNonEmpty(i.SourceBranch, "已确认来源")
	}
	return source, target
}

func productionConflictSummary(b production.Batch) string {
	source, target := productionConflictRefs(b)
	if source == "" || target == "" {
		return ""
	}
	return fmt.Sprintf("将 %s 合并到 %s 时发生冲突，请解决。", source, target)
}

func productionConflictCopyText(b production.Batch) string {
	if b.Intent == nil || b.Intent.Merge == nil {
		return ""
	}
	source, target := productionConflictRefs(b)
	m := b.Intent.Merge
	files := "未返回具体文件"
	if len(m.Conflicts) > 0 {
		files = strings.Join(m.Conflicts, "\n")
	}
	return fmt.Sprintf("服务：%s\n%s\n来源：%s\n目标：%s/%s\n隔离工作区：%s\n冲突文件：\n%s\n\n请在上述隔离工作区解决冲突并完成本次合并提交，不切换分支或推送。完成后我会回到 SW 点击“校验并继续”。",
		b.Target.ServiceName, productionConflictSummary(b), source, b.Target.Remote, target, m.Worktree, files)
}

func productionHasConflict(b production.Batch) bool {
	return b.Intent != nil && b.Intent.Merge != nil && (b.Phase == "CONFLICT" || b.Phase == "ABORTING" || len(b.Intent.Merge.Conflicts) > 0)
}

func productionCanAbort(b production.Batch) bool {
	return !b.Ended && b.Intent != nil && b.Intent.Merge != nil && !b.Intent.ResultConfirmed &&
		b.Intent.Merge.AfterSHA == "" && (b.Phase == "CONFLICT" || b.Phase == "ABORTING")
}

func (s *app) productionAbortResultCurrent(v *productionView, action application.Action, result *production.Batch) bool {
	expected, ok := action.Payload["merge"].(production.Merge)
	target, targetOK := action.Payload["target"].(production.Target)
	current := s.productionBatch(v)
	if !ok || !targetOK || current == nil || current.Ended || current.Target != target || current.Intent == nil || current.Intent.ResultConfirmed || current.Intent.Merge == nil {
		return false
	}
	if result != nil && (result.ID != v.BatchID || result.Target != target) {
		return false
	}
	m := current.Intent.Merge
	return m.ID == expected.ID && m.SourceSHA == expected.SourceSHA && m.BeforeSHA == expected.BeforeSHA &&
		m.Worktree == expected.Worktree && m.TargetBranch == expected.TargetBranch && m.Branch == expected.Branch && m.AfterSHA == expected.AfterSHA
}

func (s *app) confirmProductionAbort(v *productionView, b production.Batch) {
	if !productionCanAbort(b) {
		return
	}
	id, target, merge := b.ID, b.Target, *b.Intent.Merge
	merge.Conflicts = append([]string(nil), merge.Conflicts...)
	source, destination := productionConflictRefs(b)
	previous := s.confirmationDialog
	s.confirmProduction(v, target, productionConfirmationContent{
		Title: "撤回合并", Source: source, Destination: target.Remote + "/" + destination,
		ConfirmLabel: "放弃修改并撤回",
		Notices:      []string{"将放弃本次未提交的合并及冲突处理修改，恢复到合并前状态。", "此前成功的合并和远端分支、Tag 保持不变。"},
		Detail:       productionTargetDetail(target) + "\n合并标识：" + merge.ID + "\n源提交：" + merge.SourceSHA + "\n合并前提交：" + merge.BeforeSHA + "\n隔离工作区：" + merge.Worktree,
	}, func() {
		s.productionRequestFor(v, application.Action{Kind: "production.abort-merge", Payload: map[string]any{
			"batchId": id, "target": target, "merge": merge, "confirmed": true,
		}}, "")
	})
	if d := s.confirmationDialog; d != nil && d != previous {
		current := d.Current
		d.Current = func() bool {
			if current == nil || !current() || v.Busy {
				return false
			}
			latest := s.productionBatch(v)
			if latest == nil || !productionCanAbort(*latest) || latest.Target != target {
				return false
			}
			m := latest.Intent.Merge
			return m.ID == merge.ID && m.SourceSHA == merge.SourceSHA && m.BeforeSHA == merge.BeforeSHA &&
				m.Worktree == merge.Worktree && m.Branch == merge.Branch && m.TargetBranch == merge.TargetBranch
		}
	}
}
