package desktop

import (
	"strings"

	"silverwing-go/internal/production"

	"github.com/egoist/mygo/ui"
)

type productionBaselineDialog struct {
	Open                 bool
	Tag, OriginalTag     string
	Home, Scope, BatchID string
	View                 *productionView
	Preview              *production.Preview
}

func (s *app) openProductionBaseline(v *productionView) {
	if v == nil || v.Busy || s.page != "production" {
		return
	}
	s.ensureProduction().baselineDialog = &productionBaselineDialog{Open: true, Tag: v.ManualTag, OriginalTag: v.ManualTag,
		Home: s.snapshot.Home, Scope: productionConfigScope(s.settingsUnit.state.configJSON), BatchID: v.BatchID, View: v, Preview: v.Preview}
}

func (s *app) productionBaselineCurrent(d *productionBaselineDialog) bool {
	return d != nil && s.page == "production" && s.productionView() == d.View && s.snapshot.Home == d.Home &&
		productionConfigScope(s.settingsUnit.state.configJSON) == d.Scope && d.View.BatchID == d.BatchID && d.View.Preview == d.Preview && d.View.ManualTag == d.OriginalTag
}

func (s *app) applyProductionBaseline(d *productionBaselineDialog, automatic bool) {
	if !s.productionBaselineCurrent(d) || d.View.Busy {
		return
	}
	tag := strings.TrimSpace(d.Tag)
	if automatic {
		tag = ""
	}
	d.Open = false
	v := d.View
	v.ManualTag, v.Preview = tag, nil
	v.PreparationInvalid = true
	v.BuildChecked = false
	v.Steps = nil
	s.productionInspect("")
}

func (s *app) renderProductionBaselineDialog(c *ui.Context, p palette) {
	if s.releaseUnit.state.production == nil || s.releaseUnit.state.production.baselineDialog == nil {
		return
	}
	d := s.releaseUnit.state.production.baselineDialog
	if !d.Open || !s.productionBaselineCurrent(d) {
		s.releaseUnit.state.production.baselineDialog = nil
		return
	}
	dialog := ui.DialogBase(c.Key("production-baseline-dialog"), &d.Open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .38))
		width, height := c.Size()
		panel.Width(min(float32(580), width-32)).MaxHeight(height-32).Padding(20).Gap(14).Background(p.surface).Border(1, p.line).Radius(16)
		panel.OnShortcut(0, ui.KeyEscape, func() { d.Open = false })
		panel.Children(func() {
			label(c, "手动确认生产 Tag", 18, p.text).FontWeight(600)
			label(c, "仅在无法唯一识别线上版本时使用；应用后重新检查远端 Tag 和生产版本。", 12, p.muted).Wrap()
			combo := ui.ComboboxBase(c.Key("production-baseline"), &d.Tag)
			combo.Input.FillWidth().Height(36).Padding(0, 30, 0, 10).Placeholder("搜索或填写生产 Tag").Label("生产基线 Tag").ReadOnly(d.View.Busy).AutoFocus()
			drawTaskComboboxChevron(combo.Input, p)
			combo.Popup(func(panel ui.Element) {
				panel.Padding(4).MaxHeight(240).Background(p.surface).Border(1, p.line).Radius(9)
				ui.Scroll(c).FillWidth().MaxHeight(232).Children(func() {
					if d.Preview == nil {
						return
					}
					for _, tag := range d.Preview.Tags {
						if combo.Filtering() && !strings.Contains(strings.ToLower(tag), strings.ToLower(d.Tag)) {
							continue
						}
						item := combo.Item(tag).FillWidth().Height(30).Padding(0, 10).Radius(6)
						if item.Highlighted() {
							item.Background(p.selected)
						}
						item.Children(func() { label(c, tag, 12, p.text).SingleLine() })
					}
				})
			})
			if tag, chosen := combo.Chosen(); chosen {
				d.Tag = tag
			}
			label(c, "按创建时间从新到旧排列；轻量 Tag 使用目标提交时间。", 11, p.muted).Wrap()
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				if productionButton(c, "production-baseline-auto", "恢复自动识别", p, false, d.View.Busy) {
					s.applyProductionBaseline(d, true)
				}
				ui.Box(c).Grow(1)
				if productionButton(c, "production-baseline-cancel", "取消", p, false, false) {
					d.Open = false
				}
				if productionButton(c, "production-baseline-apply", "应用并重新检查", p, true, d.View.Busy || strings.TrimSpace(d.Tag) == "") {
					s.applyProductionBaseline(d, false)
				}
			})
		})
	})
	if dialog.Dismissed() {
		d.Open = false
	}
}
