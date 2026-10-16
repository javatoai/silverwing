package desktop

import (
	"fmt"
	"math"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func (s *app) renderDiffTool(c *ui.Context, p palette, state *toolPageState) {
	s.pullDiffInput(state, &state.diffLeft, &state.diffInputHistory[0])
	s.pullDiffInput(state, &state.diffRight, &state.diffInputHistory[1])
	ui.Column(c).Grow(1).MinHeight(0).Gap(9).Children(func() {
		state.diffInputHistory[0].sync(state.diffLeft)
		state.diffInputHistory[1].sync(state.diffRight)
		state.syncDiffEditableDecorations(p)
		s.renderDiffToolToolbar(c, p, state)
		// Keep the splitter ratio in the tool state so resizing survives tab
		// switches and rebuilding either editor pane. Scroll only vertically;
		// each side keeps its own horizontal reading position.
		s.syncDiffEditableScroll(state)
		ui.SplitRatio(c.Key("tool-diff-editable-split"), &state.diffSplitRatio,
			func() { s.diffEditor(c, p, state, "左侧文本", "tool-diff-left", &state.diffLeft) },
			func() { s.diffEditor(c, p, state, "右侧文本", "tool-diff-right", &state.diffRight) },
		).Grow(1).MinHeight(0).FillWidth()
		s.recordDiffEditableScroll(state)
	})
}

func (s *app) renderDiffToolToolbar(c *ui.Context, p palette, state *toolPageState) {
	hunkCount := len(state.diffResult.Hunks)
	if state.diffActiveHunk >= hunkCount {
		state.diffActiveHunk = 0
	}
	toolbar := ui.Row(c.Key("tool-diff-toolbar")).FillWidth().MinWidth(0).Height(34).Shrink(0).Gap(6).AlignItems(ui.Center).Label("文本对比操作栏")
	width := toolbar.Bounds().W
	if width <= 0 {
		width, _ = c.Size()
		c.Invalidate()
	}
	inlineRules, inlineAutomatic, inlineStatistics := width >= 1280, width >= 760, width >= 480
	statistics := fmt.Sprintf("新增 %d 行，删除 %d 行，修改 %d 行", state.diffResult.AddedLines, state.diffResult.RemovedLines, state.diffResult.ChangedLines)
	optionsLabel := "比较选项"
	var enabledRules []string
	for _, rule := range []struct {
		enabled bool
		name    string
	}{{state.diffOptions.IgnoreCase, "忽略大小写"}, {state.diffOptions.IgnoreLineWhitespace, "忽略行首尾空白"}, {state.diffOptions.IgnoreEmptyLines, "忽略空行"}} {
		if rule.enabled {
			enabledRules = append(enabledRules, rule.name)
		}
	}
	if len(enabledRules) > 0 {
		optionsLabel += " · 已启用：" + strings.Join(enabledRules, "、")
	}
	toolbar.Children(func() {
		if toolButtonWithEnabled(c, "diff-compare", "比较", p, true, s.toolPrimaryActionEnabled(state, "文本对比")) {
			s.runDeveloperToolPrimaryAction(state, "文本对比")
		}
		ui.Row(c).Shrink(0).Gap(2).AlignItems(ui.Center).Children(func() {
			canNavigate := state.diffHasData && hunkCount > 0
			if iconButtonDisabled(c, "diff-prev-hunk", "chevron-left", "上一处", p, !canNavigate).Width(30).Height(30).Clicked() {
				state.moveDiffHunk(nil, -1, hunkCount)
			}
			position := "—"
			if state.diffHasData {
				position = fmt.Sprintf("%d / %d", min(state.diffActiveHunk+1, hunkCount), hunkCount)
			}
			ui.Box(c).Shrink(0).Label("差异位置").Tooltip("差异位置：" + position + "\n" + statistics).Children(func() {
				label(c, position, 11, p.muted).NoWrap()
			})
			if iconButtonDisabled(c, "diff-next-hunk", "chevron-right", "下一处", p, !canNavigate).Width(30).Height(30).Clicked() {
				state.moveDiffHunk(nil, 1, hunkCount)
			}
		})
		if iconButtonDisabled(c, "diff-copy-differences", "copy", "复制差异", p, !state.diffHasData).Width(30).Height(30).Clicked() {
			s.copyTextToClipboard(c.Services(), state.diffResult.DifferencesText(), "")
		}
		ui.Row(c).Grow(1).MinWidth(0).Gap(5).AlignItems(ui.Center).Children(func() {
			switch {
			case state.diffError != "":
				label(c, state.diffError, 11, p.amber).MinWidth(0).SingleLine().Tooltip(state.diffError)
			case state.diffPending || s.developerToolJobBusy("tool.diff"):
				label(c, "正在比较…", 11, p.muted).MinWidth(0).SingleLine()
			case state.diffHasData && inlineStatistics:
				ui.Row(c).Shrink(0).Gap(7).Label("差异统计").Tooltip(statistics).Children(func() {
					label(c, fmt.Sprintf("+%d", state.diffResult.AddedLines), 11, p.green).NoWrap()
					label(c, fmt.Sprintf("−%d", state.diffResult.RemovedLines), 11, p.danger).NoWrap()
					label(c, fmt.Sprintf("~%d", state.diffResult.ChangedLines), 11, p.blue).NoWrap()
				})
			}
		})
		if inlineAutomatic {
			if toolToggle(c, "diff-wrap", "自动换行", state.diffWrap, p) {
				state.diffWrap = !state.diffWrap
			}
			if toolToggle(c, "diff-auto", "自动比较", state.diffAuto, p) {
				state.diffAuto = !state.diffAuto
				s.queueDiff()
			}
		}
		if inlineRules {
			for _, rule := range []struct {
				key, text string
				checked   bool
			}{{"ignore-case", "忽略大小写", state.diffOptions.IgnoreCase}, {"ignore-whitespace", "忽略行首尾空白", state.diffOptions.IgnoreLineWhitespace}, {"ignore-empty", "忽略空行", state.diffOptions.IgnoreEmptyLines}, {"collapse-equal", "折叠相同行", state.diffCollapseEqual}} {
				if toolToggle(c, "diff-"+rule.key, rule.text, rule.checked, p) {
					s.toggleDiffCompareOption(state, rule.key)
				}
			}
		}
		s.overflowMenu(c, p, "diff-options-more", optionsLabel, false, func(m *overflowMenuBuilder) {
			if m.Item("swap", "交换左右").Icon("arrow").Chosen() {
				state.diffLeft, state.diffRight = state.diffRight, state.diffLeft
				s.queueDiff()
			}
			if !inlineAutomatic {
				if m.Item("wrap", "自动换行").Checked(state.diffWrap).Chosen() {
					state.diffWrap = !state.diffWrap
				}
				if m.Item("automatic", "自动比较").Checked(state.diffAuto).Chosen() {
					state.diffAuto = !state.diffAuto
					s.queueDiff()
				}
			}
			if !inlineRules {
				m.Separator()
				for _, rule := range []struct {
					key, text string
					checked   bool
				}{{"ignore-case", "忽略大小写", state.diffOptions.IgnoreCase}, {"ignore-whitespace", "忽略行首尾空白", state.diffOptions.IgnoreLineWhitespace}, {"ignore-empty", "忽略空行", state.diffOptions.IgnoreEmptyLines}, {"collapse-equal", "折叠相同行", state.diffCollapseEqual}} {
					if m.Item(rule.key, rule.text).Checked(rule.checked).Chosen() {
						s.toggleDiffCompareOption(state, rule.key)
					}
				}
			}
			if state.diffHasData && !inlineStatistics {
				m.Separator()
				m.Item("statistics", statistics).Disabled(true)
			}
		})
	})
}

func (s *app) syncDiffEditableScroll(state *toolPageState) {
	positions := [2]editor.ScrollPosition{
		state.diffInputHistory[0].view.ScrollPosition(),
		state.diffInputHistory[1].view.ScrollPosition(),
	}
	active := state.diffSharedScrollSide
	if !state.diffObservedScrollReady {
		if positions[0].Y == 0 && positions[1].Y != 0 {
			active = 1
		}
	} else {
		leftMoved := math.Abs(positions[0].Y-state.diffObservedScrollY[0]) > 0.5
		rightMoved := math.Abs(positions[1].Y-state.diffObservedScrollY[1]) > 0.5
		switch {
		case leftMoved && !rightMoved:
			active = 0
		case rightMoved && !leftMoved:
			active = 1
		case leftMoved && rightMoved && state.diffInputHistory[0].view.Focused() && !state.diffInputHistory[1].view.Focused():
			active = 0
		case leftMoved && rightMoved && state.diffInputHistory[1].view.Focused() && !state.diffInputHistory[0].view.Focused():
			active = 1
		}
	}

	target := 1 - active
	if math.Abs(positions[target].Y-positions[active].Y) > 0.5 {
		positions[target].Y = positions[active].Y
		state.diffInputHistory[target].view.SetScrollPosition(positions[target])
	}
	state.diffSharedScrollSide = active
}

func (s *app) recordDiffEditableScroll(state *toolPageState) {
	state.diffObservedScrollY = [2]float64{
		state.diffInputHistory[0].view.ScrollPosition().Y,
		state.diffInputHistory[1].view.ScrollPosition().Y,
	}
	state.diffObservedScrollReady = true
}

func (s *app) toggleDiffCompareOption(state *toolPageState, option string) {
	switch option {
	case "ignore-case":
		state.diffOptions.IgnoreCase = !state.diffOptions.IgnoreCase
	case "ignore-whitespace":
		state.diffOptions.IgnoreLineWhitespace = !state.diffOptions.IgnoreLineWhitespace
	case "ignore-empty":
		state.diffOptions.IgnoreEmptyLines = !state.diffOptions.IgnoreEmptyLines
	case "collapse-equal":
		state.diffCollapseEqual = !state.diffCollapseEqual
		if state.diffCollapseEqual {
			clear(state.diffExpandedEqual)
		}
		return
	default:
		return
	}
	s.queueDiff()
}

func (state *toolPageState) moveDiffHunk(native *diffNativeViewState, step, count int) {
	if count == 0 || count != len(state.diffResult.Hunks) {
		return
	}
	state.diffActiveHunk = ((state.diffActiveHunk+step)%count + count) % count
	if native != nil {
		native.goToComparisonHunk(state.diffResult.Hunks[state.diffActiveHunk])
		return
	}
	state.revealDiffEditableHunk(state.diffResult.Hunks[state.diffActiveHunk])
}

func (s *app) diffEditor(c *ui.Context, p palette, state *toolPageState, title, key string, value *string) {
	side := 0
	if value == &state.diffRight {
		side = 1
	}
	history := &state.diffInputHistory[side]
	history.sync(*value)
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Background(p.surface).Border(1, p.line).Radius(10).Children(func() {
		ui.Row(c).Height(32).Shrink(0).Padding(0, 8).Gap(4).AlignItems(ui.Center).Children(func() {
			label(c, title, 11, p.muted).FontWeight(600)
			if side == 0 {
				label(c, "− 删除", 10, p.danger)
			} else {
				label(c, "+ 新增", 10, p.green)
			}
			ui.Spacer(c)
			if toolButtonWithEnabled(c, key+"-undo", "撤销", p, false, history.canMove(-1)) {
				s.moveDiffInputHistory(value, history, -1)
			}
			if toolButtonWithEnabled(c, key+"-redo", "重做", p, false, history.canMove(1)) {
				s.moveDiffInputHistory(value, history, 1)
			}
			if toolToggle(c, key+"-find", "查找", history.search.open, p) {
				history.search.toggle()
			}
		})
		ui.Divider(c)
		s.renderDiffInputSearch(c, p, history, *value, key)
		state.syncDiffEditableDecorations(p)
		history.view.Label = title + "输入"
		history.view.LineNumbers = editor.LineNumbersOn
		history.view.NoAutoClose = true
		history.view.NoFolding, history.view.NoScrollPastEnd = history.decorations == nil, true
		history.view.SoftWrap = state.diffWrap
		history.view.OnLimitExceeded = func() { s.rejectDiffToolInput(state) }
		theme := textEditorTheme(p)
		clipboardServices := c.Services()
		history.view.ClipboardWrite = func(text string) bool {
			return s.copyTextToClipboard(clipboardServices, text, "")
		}
		markColor := p.danger
		if side == 1 {
			markColor = p.green
		}
		theme.Tokens[editor.TokenStrong] = editor.TokenStyle{Color: markColor, Bold: true}
		history.view.ContextMenu = func(menu *ui.Menu) {
			menu.Separator()
			if menu.Item("撤销").Disabled(!history.canMove(-1)).Chosen() {
				s.moveDiffInputHistory(value, history, -1)
			}
			if menu.Item("重做").Disabled(!history.canMove(1)).Chosen() {
				s.moveDiffInputHistory(value, history, 1)
			}
		}
		input := TextEditor(c.Key(key), p, &history.view, theme).Grow(1).FillWidth().MinHeight(0)
		s.bindDiffInputHistory(state, value, history, input)
		history.search.applySelection(input, &history.view, *value, state)
	})
}

func (s *app) dispatchDiff() {
	state := s.ensureToolState()
	s.cancelDeveloperToolJob("tool.diff")
	if state.diffCancel != nil {
		state.diffCancel()
		state.diffCancel = nil
	}
	state.diffPending = false
	state.diffRevision++
	state.diffError = ""
	state.diffHasData = false
	s.act("tool.diff", "", "", "", "", "", map[string]any{
		"left": state.diffLeft, "right": state.diffRight, "options": state.diffOptions, "revision": state.diffRevision,
	})
}
