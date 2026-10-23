package desktop

import (
	"fmt"

	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
)

// Only the currently displayed record owns expanded details. A background
// refresh of that record keeps them open; a different identity starts closed.
type productionDetailIdentity struct {
	Selection                           production.Selection
	Target                              production.Target
	Home, Scope, BatchID                string
	MergeID, SourceSHA, BeforeSHA, Path string
	SourceRef, TargetRef, LocalBranch   string
}

type productionDetailDisclosures struct {
	identity     productionDetailIdentity
	conflictOpen bool
}

func (s *app) productionDisclosures(v *productionView, b *production.Batch) *productionDetailDisclosures {
	id := productionDetailIdentity{Selection: v.Selection, Home: s.snapshot.Home, Scope: s.currentProductionScope()}
	if b != nil {
		id.BatchID, id.Target = b.ID, b.Target
		if b.Intent != nil && b.Intent.Merge != nil {
			m := b.Intent.Merge
			id.MergeID, id.SourceSHA, id.BeforeSHA, id.Path = m.ID, m.SourceSHA, m.BeforeSHA, m.Worktree
			id.SourceRef, id.TargetRef = productionConflictRefs(*b)
			id.LocalBranch = m.Branch
		}
	}
	d := &s.ensureProduction().disclosures
	if d.identity != id {
		*d = productionDetailDisclosures{identity: id}
	}
	return d
}

func productionConflictHeading(v *productionView, b production.Batch) (string, string) {
	if v.Busy && v.ActiveAction == "production.abort-merge" {
		return "正在撤回合并…", "正在核对并撤回当前合并。"
	}
	if b.Phase == "ABORTING" {
		return "等待核对撤回结果", "请重试撤回，核对工作区实际状态。"
	}
	return "合并冲突", "请在隔离工作区解决冲突并完成合并提交。"
}

func (s *app) renderProductionConflict(c *ui.Context, p palette, v *productionView, b production.Batch) {
	m := b.Intent.Merge
	d := s.productionDisclosures(v, &b)
	title, instruction := productionConflictHeading(v, b)
	source, target := productionConflictRefs(b)
	ui.Column(c.Key("production-conflict-" + m.ID)).FillWidth().MinWidth(0).Gap(8).Children(func() {
		ui.Row(c).FillWidth().MinWidth(0).Gap(12).Wrap().AlignItems(ui.Center).Children(func() {
			label(c, title, 14, p.amber).FontWeight(600).Wrap()
			if tagRecordAction(c, p, "production-conflict-copy-"+m.ID, "copy", "复制冲突信息", false).Label("复制冲突信息").Clicked() {
				s.copyTextToClipboard(c.Services(), productionConflictCopyText(b), "冲突信息已复制")
			}
		})
		for _, field := range []struct{ name, value string }{{"来源", source}, {"目标", target}} {
			productionInfoRow(c, p, "production-conflict-"+field.name, field.name, field.value, "")
		}
		label(c, instruction, 12, p.muted).Wrap()
		detailTitle := "冲突详情"
		if len(m.Conflicts) > 0 {
			detailTitle = fmt.Sprintf("冲突详情 · %d 个文件", len(m.Conflicts))
		}
		productionDisclosure(c, p, "production-conflict-details-"+m.ID, detailTitle, &d.conflictOpen, func() {
			if len(m.Conflicts) == 0 {
				label(c, "未返回具体冲突文件。", 12, p.muted).Wrap()
			} else {
				ui.Scroll(c.Key("production-conflict-files-" + m.ID)).FillWidth().MinWidth(0).MaxHeight(200).Gap(4).Label("冲突文件列表").Children(func() {
					for _, path := range m.Conflicts {
						label(c, path, 12, p.text).MinWidth(0).Wrap().Selectable()
					}
				})
			}
			label(c, "隔离工作区", 12, p.muted)
			label(c, m.Worktree, 12, p.text).MinWidth(0).Wrap().Selectable()
			if tagRecordAction(c, p, "production-conflict-path-"+m.ID, "copy", "复制工作区路径", false).AlignSelf(ui.Start).Label("复制工作区路径").Clicked() {
				s.copyTextToClipboard(c.Services(), m.Worktree, "工作区路径已复制")
			}
		})
	})
}
