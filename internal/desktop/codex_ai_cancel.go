package desktop

import "github.com/egoist/mygo/ui"

func (s *app) renderCodexAIOperations(c *ui.Context, p palette, state *codexAIState) {
	for _, key := range []string{"mcp-mutate", "mcp-auth", "mcp-check", "bind"} {
		s.renderCodexAIOperation(c, p, state, key)
	}
}

func (s *app) renderCodexAIOperation(c *ui.Context, p palette, state *codexAIState, key string) {
	if state == nil || state.requests[key] == nil {
		return
	}
	text := map[string]string{
		"mcp-mutate": "正在保存 MCP…",
		"mcp-auth":   "正在处理 MCP 认证…",
		"mcp-check":  "正在检测 MCP 连接…",
		"bind":       "正在保存 Codex 关联…",
	}[key]
	if text == "" {
		return
	}
	request := state.requests[key]
	if request.cancelRequested {
		text = "正在取消，请等待清理完成…"
	}
	ui.Row(c.Key("codex-ai-operation-" + key)).Gap(8).AlignItems(ui.Center).Children(func() {
		label(c, text, 10, p.muted).Grow(1)
		if codexAIAction(c, "codex-ai-cancel-operation-"+key, "close", "取消操作", p, false, request.cancelRequested) {
			s.cancelCodexAIOperation(state, key)
		}
	})
}

func (s *app) cancelCodexAIOperation(state *codexAIState, key string) {
	if state == nil || s.codexAI != state || !s.codexAIIdentityCurrent(state) || state.requests[key] == nil {
		return
	}
	request := state.requests[key]
	if request.cancelRequested {
		return
	}
	request.cancelRequested = true
	request.cancel()
}
