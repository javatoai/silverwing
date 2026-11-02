package desktop

import (
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/workspace"
)

// Keep version identity aligned with the actual split. Navigation belongs to
// the old half and reading actions to the new half when both have enough room.
// Narrow/extreme splits retain a separate action row instead of clipping labels.
func workspaceDiffHeader(c *ui.Context, p palette, state *workspaceHistoryState, comparison *workspace.WorkspaceFileComparison, ratio float32, navigation func(float32), reading func()) ui.Element {
	header := ui.Column(c.Key("workspace-diff-header")).FillWidth().MinWidth(0).Shrink(0).Gap(0).Background(p.soft).Label("差异阅读操作栏")
	width := header.Bounds().W
	if width <= 0 {
		width, _ = c.Size()
		c.Invalidate()
	}
	compact := min(width*ratio, width*(1-ratio)) >= 280
	oldTitle, newTitle := workspaceDiffSideLabels(state, comparison)
	header.Children(func() {
		height := float32(22)
		if compact {
			height = 30
		}
		ui.Row(c).FillWidth().MinWidth(0).Height(height).Shrink(0).AlignItems(ui.Center).Children(func() {
			ui.Row(c).WidthPercent(ratio*100).MinWidth(0).Shrink(0).Padding(0, 6).Gap(5).AlignItems(ui.Center).Children(func() {
				label(c, oldTitle, 10, p.muted).Grow(1).MinWidth(0).SingleLine().Clip().Tooltip(workspaceDiffSideTooltip(state, comparison, true))
				if compact {
					navigation(width)
				}
			})
			ui.Box(c).Width(1).Shrink(0).Background(p.line)
			ui.Row(c).Grow(1).Basis(0).MinWidth(0).Padding(0, 6).Gap(5).AlignItems(ui.Center).Children(func() {
				label(c, newTitle, 10, p.muted).Grow(1).MinWidth(0).SingleLine().Clip().Tooltip(workspaceDiffSideTooltip(state, comparison, false))
				if compact {
					reading()
				}
			})
		})
		if !compact {
			ui.Row(c).FillWidth().MinWidth(0).Height(30).Shrink(0).Padding(0, 6).Gap(5).AlignItems(ui.Center).Children(func() {
				navigation(width)
				ui.Spacer(c)
				reading()
			})
		}
	})
	return header
}
