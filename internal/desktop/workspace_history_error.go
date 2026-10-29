package desktop

import "github.com/egoist/mygo/ui"

// The diagnostic scrolls inside this height; retry never shares its scroll.
// Picker chrome reserves this same height plus its inter-child gap.
const workspaceHistoryPageErrorHeight float32 = 64

type workspaceHistoryDiagnosticState struct {
	message string
	// Done can build both the closing picker and the page banner in one
	// frame. Each presentation therefore owns its own scroll handle.
	scrolls map[string]*ui.ScrollState
}

func (s *app) renderWorkspaceHistoryPageError(c *ui.Context, p palette, state *workspaceHistoryState, key string) {
	diagnostic := &state.HistoryPageDiagnostic
	if diagnostic.message != state.HistoryPageError {
		diagnostic.message = state.HistoryPageError
		diagnostic.scrolls = nil
	}
	if state.HistoryPageError == "" {
		return
	}
	if diagnostic.scrolls == nil {
		diagnostic.scrolls = make(map[string]*ui.ScrollState)
	}
	scroll := diagnostic.scrolls[key]
	if scroll == nil {
		scroll = &ui.ScrollState{}
		diagnostic.scrolls[key] = scroll
	}
	ui.Row(c).FillWidth().MinWidth(0).Height(workspaceHistoryPageErrorHeight).Shrink(0).
		Gap(8).Padding(8, 10).Radius(8).Background(p.amberBg).AlignItems(ui.Center).Clip().Children(func() {
		ui.Scroll(c.Key(key + "-history-diagnostic")).Grow(1).MinWidth(0).
			Height(workspaceHistoryPageErrorHeight - 16).TrackScroll(scroll).
			Label("提交历史错误详情").Children(func() {
			label(c, "提交历史读取失败："+state.HistoryPageError, 11, p.danger).FillWidth().MinWidth(0).Wrap().Selectable()
		})
		ui.Row(c).Shrink(0).AlignItems(ui.Center).Children(func() {
			if action(c, key+"-history-retry", "refresh", "重试提交历史", p, state.HistoryLoading) {
				s.retryWorkspaceHistoryPage(state)
			}
		})
	})
}
