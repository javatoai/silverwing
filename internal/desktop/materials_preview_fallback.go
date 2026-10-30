package desktop

import (
	"path/filepath"

	"github.com/egoist/mygo/ui"
)

// Shared by unknown formats and failed native previews. The view knows nothing
// about task validation or the platform launcher; its owner supplies the action.
func materialsPreviewFallback(c *ui.Context, p palette, path, reason string, busy bool, open func()) {
	ui.Column(c).Grow(1).FillWidth().MinWidth(0).Center().Gap(12).Padding(24).Children(func() {
		icon(c, "file", 28, p.muted)
		label(c, filepath.Base(path), 14, p.text).FontWeight(600).Wrap()
		label(c, reason, 13, p.muted).Wrap()
		ui.Row(c).Disabled(busy || open == nil).Children(func() {
			if action(c, "materials-preview-open-external", "arrow", "使用系统应用打开", p, false) && !busy && open != nil {
				open()
			}
		})
	})
}

func (s *app) renderMaterialsPreviewFallback(c *ui.Context, p palette, reason string) {
	materialsPreviewFallback(c, p, s.materialsUnit.state.documentPath, reason, s.materialsSystemOpenBusy(nil), func() {
		s.act("document.open", s.taskUnit.currentTask().id, "", "", s.materialsUnit.state.documentPath, "", nil)
	})
}
