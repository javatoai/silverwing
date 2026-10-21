package desktop

import (
	"fmt"
	"github.com/egoist/mygo/ui"
)

func (s *app) renderTagHistoryDeleteDialog(c *ui.Context, p palette, state *tagHistoryViewState) {
	if !state.DeleteOpen {
		return
	}
	if !s.tagHistoryStateCurrent(state) {
		state.DeleteOpen = false
		return
	}
	ui.DialogBase(c.Key("tag-history-delete-dialog"), &state.DeleteOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.34))
		width, _ := c.Size()
		panel.Width(min(float32(520), max(float32(0), width-32))).Padding(20).Gap(14).Background(p.surface).Radius(14)
		panel.Children(func() {
			title := fmt.Sprintf("删除 %d 批 Tag 历史", len(state.DeleteGroupIDs))
			if state.DeleteAll {
				title = "清空该任务的本地 Tag 历史"
			}
			label(c, title, 17, p.text).FontWeight(600).Wrap()
			label(c, "仅删除当前任务所选完整批次的本地构建记录和历史汇总。Git Tag、代码、任务及资料目录会保留。", 12, p.muted).Wrap()
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if action(c, "tag-history-delete-cancel", "close", "取消", p, false) {
					state.DeleteOpen = false
				}
				ui.Row(c).Disabled(s.busy || len(state.DeleteIDs) == 0).Children(func() {
					if action(c, "tag-history-delete-confirm", "delete", "确认删除", p, true) && !s.busy && s.tagHistoryStateCurrent(state) {
						kind := "tag.history.delete"
						if state.DeleteAll {
							kind = "tag.history.clear"
						}
						state.DeleteOpen = false
						payload := tagHistoryScopePayload(state)
						payload["operationIds"] = append([]string(nil), state.DeleteIDs...)
						s.act(kind, state.TaskID, "", "", "", "", payload)
					}
				})
			})
		})
	})
}
