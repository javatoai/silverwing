package desktop

import (
	"fmt"
	"slices"

	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
)

type productionWorktreesDialog struct {
	Open        bool
	View        *productionView
	Batch       production.Batch
	Home, Scope string
}

func (s *app) openProductionWorktrees(v *productionView, b production.Batch) {
	if !s.productionDisplayedBatchCurrent(v, b) || len(b.RetainedWorktrees) == 0 {
		return
	}
	b.RetainedWorktrees = slices.Clone(b.RetainedWorktrees)
	s.releaseUnit.state.production.worktreesDialog = &productionWorktreesDialog{Open: true, View: v, Batch: b, Home: s.snapshot.Home, Scope: s.currentProductionScope()}
}

func (s *app) productionWorktreesCurrent(d *productionWorktreesDialog) bool {
	return d != nil && d.Home == s.snapshot.Home && d.Scope == s.currentProductionScope() && s.productionDisplayedBatchCurrent(d.View, d.Batch)
}

func (s *app) renderProductionWorktreesDialog(c *ui.Context, p palette) {
	if s.releaseUnit.state.production == nil || s.releaseUnit.state.production.worktreesDialog == nil {
		return
	}
	d := s.releaseUnit.state.production.worktreesDialog
	if !d.Open || !s.productionWorktreesCurrent(d) {
		s.releaseUnit.state.production.worktreesDialog = nil
		return
	}
	dialog := ui.DialogBase(c.Key("production-worktrees-dialog"), &d.Open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .38))
		width, height := c.Size()
		panel.Width(min(float32(720), width-32)).MaxHeight(height-32).Padding(20).Gap(16).Background(p.surface).Border(1, p.line).Radius(16).Label("保留的工作区弹窗")
		panel.OnShortcut(0, ui.KeyEscape, func() { d.Open = false })
		panel.Children(func() {
			label(c, fmt.Sprintf("保留的工作区 · %d 个", len(d.Batch.RetainedWorktrees)), 18, p.text).FontWeight(600)
			label(c, "合并或恢复操作留下的隔离 Git 工作区，可能包含人工修改或未跟踪文件。", 12, p.muted).Wrap()
			ui.Scroll(c).FillWidth().MinWidth(0).MinHeight(0).MaxHeight(max(float32(80), height-230)).Gap(16).Label("保留工作区路径列表").Children(func() {
				for index, path := range d.Batch.RetainedWorktrees {
					ui.Row(c.Key(fmt.Sprintf("production-retained-%d", index))).FillWidth().MinWidth(0).Gap(12).AlignItems(ui.Start).Children(func() {
						label(c, path, 12, p.text).Grow(1).MinWidth(0).Wrap().Selectable()
						if tagRecordAction(c, p, fmt.Sprintf("production-retained-copy-%d", index), "copy", "复制路径", false).Label(fmt.Sprintf("复制保留工作区路径 %d", index+1)).Clicked() && s.productionWorktreesCurrent(d) {
							s.copyTextToClipboard(c.Services(), path, "工作区路径已复制")
						}
					})
				}
			})
			ui.Row(c).FillWidth().Justify(ui.End).Shrink(0).Children(func() {
				if productionButton(c, "production-worktrees-close", "关闭", p, false, false) {
					d.Open = false
				}
			})
		})
	})
	if dialog.Dismissed() {
		d.Open = false
	}
}
