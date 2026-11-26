package desktop

import "github.com/egoist/mygo/ui"

func (s *app) renderCodexAIBinding(c *ui.Context, p palette, state *codexAIState) {
	request := state.requests["bind"]
	if request == nil {
		return
	}
	text := "正在保存 Codex 关联…"
	if request.cancelRequested {
		text = "正在取消，请等待清理完成…"
	}
	ui.Row(c.Key("codex-ai-operation-bind")).Gap(8).AlignItems(ui.Center).Children(func() {
		label(c, text, 10, p.muted).Grow(1)
		if codexAIAction(c, "codex-ai-cancel-operation-bind", "close", "取消操作", p, false, request.cancelRequested) &&
			s.codexAIIdentityCurrent(state) && state.requests["bind"] == request {
			request.cancelRequested = true
			request.cancel()
		}
	})
}
