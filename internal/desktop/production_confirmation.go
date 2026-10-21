package desktop

import (
	"silverwing-go/internal/application"
	"silverwing-go/internal/production"
)

type productionConfirmationContent struct {
	Title, Message, Source, Destination, Detail string
	Notices                                     []string
}

func productionCommitLabel(ref, sha string) string {
	if sha == "" {
		return ref
	}
	return ref + " · " + shortProductionSHA(sha)
}

// Only presentation uses short SHAs. The caller's original confirmed payload
// and full identity remain captured in its continuation and detailed text.
func (s *app) confirmProduction(v *productionView, target production.Target, content productionConfirmationContent, confirmed func()) {
	if v == nil || s.production == nil || s.productionView() != v {
		return
	}
	if !s.showConfirmationDialog(content.Title, content.Message, content.Detail, []confirmationDialogButton{
		{label: "继续", primary: true}, {label: "取消"},
	}, 1, 1, func(button int) {
		if button == 0 && confirmed != nil {
			confirmed()
		}
	}) {
		return
	}
	dialog := s.confirmationDialog
	dialog.Summary = []confirmationDialogField{{Label: "服务", Value: target.ServiceName}}
	if content.Source != "" {
		dialog.Summary = append(dialog.Summary, confirmationDialogField{Label: "来源", Value: content.Source})
	}
	if content.Destination != "" {
		dialog.Summary = append(dialog.Summary, confirmationDialogField{Label: "目标", Value: content.Destination})
	}
	dialog.Summary = append(dialog.Summary, confirmationDialogField{Label: "仓库", Value: target.Repository}, confirmationDialogField{Label: "远端", Value: target.Remote})
	dialog.Notices = append([]string(nil), content.Notices...)
	dialog.FoldDetail = true
	home, batch, manual, selection, state := s.snapshot.Home, v.BatchID, v.ManualTag, v.Selection, s.production
	checkedConfig, scope, configCurrent := s.configJSON, productionConfigScope(s.configJSON), true
	dialog.Current = func() bool {
		// Configuration parsing happens only when its immutable JSON changes,
		// never on every frame while the dialog is displayed.
		if checkedConfig != s.configJSON {
			checkedConfig = s.configJSON
			configCurrent = productionConfigScope(checkedConfig) == scope
		}
		return s.production == state && s.page == "production" && state.views[state.selected] == v &&
			s.snapshot.Home == home && v.BatchID == batch && v.Selection == selection && v.ManualTag == manual && configCurrent
	}
}

func (s *app) confirmProductionEnd(v *productionView, b production.Batch) {
	id := b.ID
	s.confirmProduction(v, b.Target, productionConfirmationContent{
		Title: "结束生产批次", Message: "确认结束当前批次？",
		Source: productionCommitLabel(firstNonEmpty(b.ReleaseBranch, "初始化批次"), b.ReleaseSHA), Destination: "结束批次",
		Detail:  productionTargetDetail(b.Target) + "\n批次：" + b.ReleaseBranch + "\nrelease 提交：" + b.ReleaseSHA,
		Notices: []string{"保留远端分支、Tag、历史和未完成工作区。结束后可创建新批次。"},
	}, func() {
		s.productionRequestFor(v, application.Action{Kind: "production.end", Payload: map[string]any{"batchId": id, "confirmed": true}}, "")
	})
}

func (s *app) confirmProductionAdopt(v *productionView, b production.Batch) {
	if v.Preview == nil {
		return
	}
	id, sha, diff := b.ID, v.Preview.ReleaseSHA, v.Preview.Diff
	s.confirmProduction(v, b.Target, productionConfirmationContent{
		Title: "采纳远端 release", Message: "确认将远端新增提交记录到当前批次？",
		Source: productionCommitLabel(b.Target.Remote+"/"+b.ReleaseBranch, sha), Destination: productionCommitLabel("当前批次 · "+b.ReleaseBranch, b.ReleaseSHA),
		Detail:  productionTargetDetail(b.Target) + "\n当前批次提交：" + b.ReleaseSHA + "\n远端提交：" + sha + "\n" + diff,
		Notices: []string{"仅允许保留原批次历史的新增提交。"},
	}, func() {
		s.productionRequestFor(v, application.Action{Kind: "production.accept-remote", Payload: map[string]any{"batchId": id, "releaseSha": sha, "confirmed": true}}, "")
	})
}

func (s *app) confirmProductionResume(v *productionView, b production.Batch) {
	if b.Intent == nil {
		return
	}
	id, intent := b.ID, *b.Intent
	source, destination := productionCommitLabel(b.ReleaseBranch, intent.TagCommit), "Tag "+intent.Tag
	notices := []string{"SW 会核对实际提交与远端结果，仅继续本次已确认的操作。"}
	detail := productionTargetDetail(b.Target)
	if merge := intent.Merge; merge != nil {
		source = productionCommitLabel(firstNonEmpty(intent.SourceBranch, "已确认源提交"), merge.SourceSHA)
		destination = productionCommitLabel(b.Target.Remote+"/"+merge.TargetBranch, merge.BeforeSHA)
		notices = append(notices, "请先在 IDE 完成冲突处理和 merge commit；保留原工作区。")
		detail += "\n源提交：" + merge.SourceSHA + "\n目标提交：" + merge.BeforeSHA + "\n工作区：" + merge.Worktree
	} else if intent.Kind == "tag" {
		detail += "\nTag：" + intent.Tag + "\nTag 提交：" + intent.TagCommit + "\nTag 对象：" + intent.TagObject
	} else {
		source, destination = productionCommitLabel(b.Target.Remote+"/"+b.Target.MainBranch, intent.Preview.MainSHA), b.ReleaseBranch
		detail += "\n主分支提交：" + intent.Preview.MainSHA + "\nrelease：" + b.ReleaseBranch
	}
	detail += "\n" + intent.Preview.Commits + "\n" + intent.Preview.Diff
	s.confirmProduction(v, b.Target, productionConfirmationContent{Title: "校验并继续", Message: "确认核对结果并继续当前操作？", Source: source, Destination: destination, Detail: detail, Notices: notices}, func() {
		s.productionRequestFor(v, application.Action{Kind: "production.resume", Payload: map[string]any{"batchId": id, "confirmed": true}}, "")
	})
}

func (s *app) confirmProductionReprepare(v *productionView, b production.Batch) {
	if b.Intent == nil || b.Intent.Kind != "tag" {
		return
	}
	id := b.ID
	s.confirmProduction(v, b.Target, productionConfirmationContent{
		Title: "重新准备 Tag", Message: "先核实原 Tag 尚未推送成功，再重新准备。",
		Source: productionCommitLabel("Tag "+b.Intent.Tag, b.Intent.TagCommit), Destination: "重新检查编号与生产基线",
		Detail:  productionTargetDetail(b.Target) + "\n原 Tag：" + b.Intent.Tag + "\n提交：" + b.Intent.TagCommit + "\nTag 对象：" + b.Intent.TagObject,
		Notices: []string{"已存在的远端 Tag 不会覆盖。"},
	}, func() {
		s.productionRequestFor(v, application.Action{Kind: "production.reprepare-tag", Payload: map[string]any{"batchId": id, "confirmed": true}}, "")
	})
}
