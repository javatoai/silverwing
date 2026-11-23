package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/tools"
)

func (s *app) renderDiffModes(c *ui.Context, p palette, state *toolPageState) {
	ui.Row(c.Key("tool-diff-modes")).Height(30).Shrink(0).AlignSelf(ui.Start).Padding(2).Gap(2).
		Background(p.soft).Radius(8).Label("对比模式").Children(func() {
		for _, mode := range []struct {
			name, label string
			json        bool
		}{{"文本", "文本模式", false}, {"JSON", "JSON 模式", true}} {
			selected := state.diffJSONMode == mode.json
			button := ui.ButtonBase(c.Key("diff-mode-" + mode.name)).Height(26).Width(54).
				Radius(6).Center().Label(mode.label).Checked(selected)
			fg := p.muted
			if selected {
				fg = p.blue
				button.Background(p.selected)
			} else if button.Hovered() {
				button.Background(p.surface)
			}
			if button.Focused() {
				button.Border(1, p.blue)
			}
			button.Children(func() { label(c, mode.name, 12, fg).FontWeight(600).NoWrap() })
			if button.Clicked() && !selected {
				state.diffJSONMode = mode.json
				state.diffObservedScrollReady = false
				s.queueDiff()
			}
		}
	})
}

func (s *app) renderJSONDiffTool(c *ui.Context, p palette, state *toolPageState) {
	ui.Row(c.Key("tool-json-diff-toolbar")).FillWidth().Height(34).Shrink(0).Gap(6).AlignItems(ui.Center).
		Label("JSON 对比操作栏").Children(func() {
		if toolButtonWithEnabled(c, "diff-json-compare", "比较", p, true, s.toolPrimaryActionEnabled(state, "文本对比")) {
			s.runDeveloperToolPrimaryAction(state, "文本对比")
		}
		if toolButtonWithEnabled(c, "diff-json-copy", "复制差异", p, false, state.diffHasData && len(state.diffJSONResult.Entries) > 0) {
			s.copyTextToClipboard(c.Services(), state.diffJSONResult.DifferencesText(), "")
		}
		ui.Spacer(c)
		if toolToggle(c, "diff-json-auto", "自动比较", state.diffAuto, p) {
			state.diffAuto = !state.diffAuto
			s.queueDiff()
		}
	})
	const ignoreExample = "例如 traceId, $.data.timestamp（逗号分隔）"
	ui.Row(c).FillWidth().MinWidth(0).Height(34).Shrink(0).Gap(8).AlignItems(ui.Center).Children(func() {
		label(c, "忽略字段", 12, p.muted).FontWeight(600).NoWrap()
		ui.TextInput(c.Key("diff-json-ignore"), &state.diffJSONIgnore).Grow(1).MinWidth(0).Height(32).
			Label("忽略字段输入").Placeholder(ignoreExample).Tooltip("字段名匹配各层同名字段，$ 路径精确匹配指定位置。" + ignoreExample).
			OnChange(func() { s.queueDiff() })
	})
	for side := range state.diffInputHistory {
		history := &state.diffInputHistory[side]
		history.clearDiffDecorations()
		history.view.MinHorizontalExtent = 0
	}
	ui.SplitRatio(c.Key("tool-diff-editable-split"), &state.diffSplitRatio,
		func() { s.diffEditor(c, p, state, "左侧 JSON", "tool-diff-left", &state.diffLeft) },
		func() { s.diffEditor(c, p, state, "右侧 JSON", "tool-diff-right", &state.diffRight) },
	).Grow(3).MinHeight(0).FillWidth()
	s.renderJSONDiffResults(c, p, state)
}

func (s *app) renderJSONDiffResults(c *ui.Context, p palette, state *toolPageState) {
	result := state.diffJSONResult
	ui.Column(c.Key("tool-json-diff-results")).FillWidth().Grow(2).MinHeight(0).MinWidth(0).
		Background(p.surface).Border(1, p.line).Radius(10).Clip().Label("JSON 差异").Children(func() {
		ui.Row(c).Height(32).Shrink(0).Padding(0, 10).Gap(7).AlignItems(ui.Center).Children(func() {
			label(c, "JSON 差异", 12, p.text).FontWeight(600).NoWrap()
			switch {
			case state.diffError != "":
				label(c, state.diffError, 11, p.amber).Grow(1).MinWidth(0).SingleLine().Tooltip(state.diffError)
			case state.diffPending || s.developerToolJobBusy("tool.diff"):
				label(c, "正在比较…", 11, p.muted).NoWrap()
			case len(result.Entries) > 0 && state.diffHasData:
				label(c, fmt.Sprintf("新增 %d · 删除 %d · 修改 %d", result.Added, result.Removed, result.Changed), 11, p.muted).
					Grow(1).MinWidth(0).SingleLine()
			}
		})
		ui.Divider(c)
		if !state.diffHasData || len(result.Entries) == 0 {
			if state.diffHasData {
				label(c, "没有结构差异", 12, p.muted).Padding(10).NoWrap()
			} else if state.diffError == "" && !state.diffPending && !s.developerToolJobBusy("tool.diff") {
				label(c, "在左右两侧输入 JSON 后比较。", 12, p.muted).Padding(10).Wrap()
			}
			return
		}
		ui.Row(c).Height(28).Shrink(0).Padding(0, 10).Gap(8).AlignItems(ui.Center).Background(p.soft).Children(func() {
			label(c, "变更", 11, p.muted).Width(42).Shrink(0).NoWrap()
			label(c, "路径", 11, p.muted).Basis(0).Grow(1.3).MinWidth(0).SingleLine()
			label(c, "旧值", 11, p.muted).Basis(0).Grow(1).MinWidth(0).SingleLine()
			label(c, "新值", 11, p.muted).Basis(0).Grow(1).MinWidth(0).SingleLine()
		})
		ui.List(c.Key(state.diffOutputRevision), nil, len(result.Entries)).FillWidth().Grow(1).MinHeight(0).Gap(0).Rows(func(row ui.ListRow) {
			renderJSONDiffEntry(row, p, result.Entries[row.Index])
		})
	})
}

func renderJSONDiffEntry(row ui.ListRow, p palette, entry tools.JSONDiffEntry) {
	name, color := "修改", p.blue
	switch entry.Kind {
	case tools.JSONDiffAdded:
		name, color = "新增", p.green
	case tools.JSONDiffRemoved:
		name, color = "删除", p.danger
	}
	c := row.Context
	line := ui.Row(c).FillWidth().Height(31).Padding(0, 10).Gap(8).AlignItems(ui.Center)
	if row.Index%2 != 0 {
		line.Background(p.soft.Mix(p.surface, .5))
	}
	line.Children(func() {
		label(c, name, 11, color).Width(42).Shrink(0).NoWrap()
		label(c, entry.Path, 12, p.text).Font("monospace").Basis(0).Grow(1.3).MinWidth(0).SingleLine().TruncateStart().Tooltip(entry.Path)
		label(c, firstNonEmpty(entry.Left, "—"), 12, p.text).Font("monospace").Basis(0).Grow(1).MinWidth(0).SingleLine().Tooltip(entry.Left)
		label(c, firstNonEmpty(entry.Right, "—"), 12, p.text).Font("monospace").Basis(0).Grow(1).MinWidth(0).SingleLine().Tooltip(entry.Right)
	})
}
