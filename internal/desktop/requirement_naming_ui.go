package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

func (s *app) renderRequirementNamingPreparation(c *ui.Context, p palette) {
	state := s.requirementUnit.state.requirementNaming
	if state == nil {
		return
	}
	status := state.queue.snapshot()
	if status.Total == 0 {
		return
	}
	preparing := status.Completed < status.Total && (status.Running || status.Paused)
	if !preparing && len(status.Failures) == 0 && !state.failuresOpen {
		return
	}
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if preparing {
				text := fmt.Sprintf("命名已准备 %d/%d", status.Completed, status.Total)
				if status.Paused {
					text = "命名预热已暂停 · " + text
				}
				if len(status.Failures) > 0 {
					text += fmt.Sprintf(" · 失败 %d", len(status.Failures))
				}
				label(c, text, 11, p.muted).Wrap()
				caption := "暂停命名预热"
				if status.Paused {
					caption = "继续命名预热"
				}
				if action(c, "requirement-naming-pause", "refresh", caption, p, false) {
					state.queue.setPaused(!status.Paused)
				}
			}
			if len(status.Failures) > 0 && action(c, "requirement-naming-failures", "warning", fmt.Sprintf("查看失败 (%d)", len(status.Failures)), p, false) {
				state.failuresOpen = !state.failuresOpen
			}
		})
		if !state.failuresOpen {
			return
		}
		ui.Column(c.Key("requirement-naming-failures-panel")).FillWidth().Padding(10).Gap(8).Background(p.soft).Border(1, p.line).Radius(10).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				label(c, "命名预热失败", 12, p.text).FontWeight(600).Grow(1)
				if len(status.Failures) > 0 && len(status.Retrying) == 0 && action(c, "requirement-naming-retry-all", "refresh", "重试全部失败项", p, false) {
					state.queue.retry("", true)
				}
				if iconButton(c, "requirement-naming-close-failures", "close", "关闭失败列表", p).Clicked() {
					state.failuresOpen = false
				}
			})
			if len(status.Failures) == 0 {
				label(c, "失败项已全部完成", 11, p.muted)
				return
			}
			ui.Scroll(c).FillWidth().MaxHeight(220).Gap(8).Children(func() {
				for _, failure := range status.Failures {
					ui.Row(c.Key("requirement-naming-failure-" + failure.Key)).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
							label(c, failure.Title, 12, p.text).Wrap()
							label(c, failure.Message, 11, p.danger).Wrap()
						})
						if status.Retrying[failure.Key] {
							label(c, "重试中…", 11, p.muted)
						} else if action(c, "requirement-naming-retry-"+failure.Key, "refresh", "重试", p, false) {
							state.queue.retry(failure.Key, false)
						}
					})
				}
			})
		})
	})
}
