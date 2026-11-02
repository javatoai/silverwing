package desktop

import (
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/workspace"
)

// Matches use byte ranges in an original source line, never a folded display
// row or a lowercased copy. Unicode and inline syntax keep their exact text.
type workspaceDiffRowsMatch struct{ sourceIndex, start, end int }

type workspaceDiffRowsControls struct {
	searchOpen, lineOpen          bool
	focusSearch, focusLine        bool
	returnFocus, revealMatch      bool
	old                           bool
	query, matchedQuery, lineText string
	matchedOld, matchedStrict     bool
	matched                       bool
	matches                       []workspaceDiffRowsMatch
	match                         int
	message                       string
}

func (controls *workspaceDiffRowsControls) refresh(view *workspaceDiffRowsView) {
	if controls.matched && controls.matchedQuery == controls.query && controls.matchedOld == controls.old && controls.matchedStrict == view.changedOnly {
		return
	}
	controls.matched, controls.matchedQuery = true, controls.query
	controls.matchedOld, controls.matchedStrict = controls.old, view.changedOnly
	controls.matches, controls.match = nil, -1
	if controls.query == "" {
		return
	}
	pattern, err := regexp.Compile("(?i)" + regexp.QuoteMeta(controls.query))
	if err != nil {
		return
	}
	for sourceIndex, row := range view.sourceRows {
		if view.changedOnly && !row.changed {
			continue
		}
		cell := row.new
		if controls.old {
			cell = row.old
		}
		if cell.line == nil {
			continue
		}
		for _, match := range pattern.FindAllStringIndex(cell.line.Text, -1) {
			controls.matches = append(controls.matches, workspaceDiffRowsMatch{sourceIndex, match[0], match[1]})
		}
	}
}

func (view *workspaceDiffRowsView) find(query string, direction int) bool {
	controls := &view.controls
	controls.query = query
	controls.refresh(view)
	if len(controls.matches) == 0 {
		return false
	}
	if direction < 0 {
		controls.match--
		if controls.match < 0 {
			controls.match = len(controls.matches) - 1
		}
	} else {
		controls.match = (controls.match + 1) % len(controls.matches)
	}
	match := controls.matches[controls.match]
	folded := view.sourceToDisplay[match.sourceIndex] < 0
	if !view.revealSource(match.sourceIndex) {
		return false
	}
	controls.revealMatch = true
	controls.message = ""
	if folded {
		controls.message = "已展开匹配行附近的上下文"
	}
	return true
}

func (view *workspaceDiffRowsView) goToLine(old bool, number int) bool {
	controls := &view.controls
	lineSources := view.newLineSources
	if old {
		lineSources = view.oldLineSources
	}
	source, found := lineSources[number]
	if !found || number <= 0 {
		controls.message = "该原始行号不在已加载范围内"
		return false
	}
	if view.changedOnly && !view.sourceRows[source].changed {
		controls.message = "该行不在仅看改动范围内"
		return false
	}
	folded := view.sourceToDisplay[source] < 0
	if !view.revealSource(source) {
		return false
	}
	controls.message = ""
	if folded {
		controls.message = "已展开目标行附近的上下文"
	}
	return true
}

func (view *workspaceDiffRowsView) openSearch() {
	view.controls.searchOpen, view.controls.lineOpen = true, false
	view.controls.focusSearch, view.controls.focusLine = true, false
	view.controls.message = ""
}

func (view *workspaceDiffRowsView) openLine() {
	view.controls.searchOpen, view.controls.lineOpen = false, true
	view.controls.focusSearch, view.controls.focusLine = false, true
	view.controls.message = ""
}

func (view *workspaceDiffRowsView) closeControls() {
	view.controls.searchOpen, view.controls.lineOpen = false, false
	view.controls.returnFocus = true
	view.controls.message = ""
}

// Register navigation on the panes, which contain only read-only code. The
// editable find/line controls explicitly opt into their own keys below; a
// different input elsewhere in the review page cannot trigger these bindings.
func (view *workspaceDiffRowsView) bindPaneShortcuts(pane ui.Element, state *workspaceHistoryState, old bool) {
	openSearch := func() { view.controls.old = old; view.openSearch() }
	openLine := func() { view.controls.old = old; view.openLine() }
	pane.OnShortcut(ui.Cmd, ui.KeyF, openSearch)
	pane.OnShortcut(ui.Ctrl, ui.KeyG, openLine)
	moveFind := func(direction int) {
		view.controls.old, view.controls.searchOpen = old, true
		if view.controls.query == "" {
			view.controls.focusSearch = true
			return
		}
		if view.find(view.controls.query, direction) {
			view.controls.returnFocus = true
		}
	}
	pane.OnShortcut(0, ui.KeyF3, func() { moveFind(1) })
	pane.OnShortcut(ui.Shift, ui.KeyF3, func() { moveFind(-1) })
	if runtime.GOOS == "darwin" {
		pane.OnShortcut(ui.Super, ui.KeyG, func() { moveFind(1) })
		pane.OnShortcut(ui.Super|ui.Shift, ui.KeyG, func() { moveFind(-1) })
	}
	if !view.singlePane {
		mods, key := ui.Modifiers(ui.Alt), ui.KeyF5
		if runtime.GOOS == "darwin" {
			mods, key = ui.Super, ui.KeyF8
		}
		pane.OnShortcut(mods, key, func() { view.controls.old = old; view.moveHunk(state, 1) })
		pane.OnShortcut(mods|ui.Shift, key, func() { view.controls.old = old; view.moveHunk(state, -1) })
	}
}

func workspaceDiffRowsToolButton(c *ui.Context, key, text string, p palette, enabled bool) bool {
	button := ui.ButtonBase(c.Key(key)).Height(24).Padding(0, 7).Radius(5).Center().Label(text).Disabled(!enabled)
	bg := p.surface
	if button.Hovered() && enabled {
		bg = p.selected
	}
	button.Background(bg).Children(func() {
		ink := p.text
		if !enabled {
			ink = p.muted
		}
		label(c, text, 10, ink).NoWrap()
	})
	return enabled && button.Clicked()
}

func renderWorkspaceDiffRowsNavigation(c *ui.Context, p palette, state *workspaceHistoryState, view *workspaceDiffRowsView, comparison *workspace.WorkspaceFileComparison) {
	if view.singlePane {
		return
	}
	count := len(view.hunks)
	if state.DiffHunkSelected && state.DiffHunkIndex >= count {
		state.DiffHunkIndex, state.DiffHunkSelected = max(0, count-1), count > 0
	}
	header := workspaceDiffHeader(c, p, state, comparison, firstNonZeroSplitRatio(state.SplitRatio, .5), func(_ float32) {
		workspaceDiffHunkButton(c, "workspace-diff-rows-hunk-previous", "上一处", p, count > 0, func() { view.moveHunk(state, -1) })
		text := "无差异块"
		if count > 0 && state.DiffHunkSelected {
			text = strconv.Itoa(state.DiffHunkIndex+1) + " / " + strconv.Itoa(count)
		} else if count > 0 {
			text = strconv.Itoa(count) + " 处差异"
		}
		label(c, text, 10, p.muted).NoWrap()
		workspaceDiffHunkButton(c, "workspace-diff-rows-hunk-next", "下一处", p, count > 0, func() { view.moveHunk(state, 1) })
		added, removed := workspaceDiffRowsStats(view)
		label(c, "+"+formatWorkspaceDiffCount(added), 10, p.green).NoWrap().Shrink(0)
		label(c, "−"+formatWorkspaceDiffCount(removed), 10, p.danger).NoWrap().Shrink(0)
	}, func() {
		if workspaceDiffRowsToolButton(c, "workspace-diff-rows-find", "查找", p, true) {
			view.openSearch()
		}
		if workspaceDiffRowsToolButton(c, "workspace-diff-rows-line", "跳到行", p, true) {
			view.openLine()
		}
	})
	view.bindPaneShortcuts(header, state, view.controls.old)
}

func renderWorkspaceDiffRowsControls(c *ui.Context, p palette, view *workspaceDiffRowsView) {
	controls := &view.controls
	controls.refresh(view)
	if !controls.searchOpen && !controls.lineOpen {
		return
	}
	bar := ui.Row(c).FillWidth().Height(32).Shrink(0).Padding(2, 6).Gap(4).AlignItems(ui.Center)
	bar.OnShortcut(0, ui.KeyEscape, view.closeControls)
	if controls.searchOpen {
		bar.OnShortcut(0, ui.KeyF3, func() { view.find(controls.query, 1) })
		bar.OnShortcut(ui.Shift, ui.KeyF3, func() { view.find(controls.query, -1) })
	}
	bar.Children(func() {
		if !view.singlePane {
			side := "当前版本"
			if controls.old {
				side = "修改前"
			}
			if workspaceDiffRowsToolButton(c, "workspace-diff-rows-control-side", side, p, true) {
				controls.old = !controls.old
				controls.refresh(view)
			}
		}
		if controls.searchOpen {
			input := ui.TextInput(c.Key("workspace-diff-rows-search-input"), &controls.query).Grow(1).MinWidth(0).FontSize(11).Label("查找差异文本").Placeholder("查找差异文本")
			input.OnChange(func() { controls.refresh(view); controls.message = "" })
			input.OnShortcut(ui.Cmd, ui.KeyF, func() { input.Focus() })
			input.OnShortcut(0, ui.KeyEscape, view.closeControls)
			if runtime.GOOS == "darwin" {
				input.OnShortcut(ui.Super, ui.KeyG, func() { view.find(controls.query, 1) })
				input.OnShortcut(ui.Super|ui.Shift, ui.KeyG, func() { view.find(controls.query, -1) })
			}
			input.HandleInput(func(event ui.InputEvent) bool {
				if event.Kind != ui.InputKeyDown {
					return false
				}
				if event.Key == ui.KeyEscape && event.Mods == 0 {
					view.closeControls()
					return true
				}
				if (event.Key == ui.KeyEnter || event.Key == ui.KeyF3) && (event.Mods == 0 || event.Mods == ui.Shift) {
					direction := 1
					if event.Mods == ui.Shift {
						direction = -1
					}
					view.find(controls.query, direction)
					return true
				}
				return false
			})
			if controls.focusSearch {
				input.Focus()
				controls.focusSearch = false
			}
			label(c, strconv.Itoa(controls.match+1)+" / "+strconv.Itoa(len(controls.matches))+" 处", 10, p.muted).NoWrap()
			if workspaceDiffRowsToolButton(c, "workspace-diff-rows-find-previous", "上一个", p, len(controls.matches) > 0) {
				view.find(controls.query, -1)
			}
			if workspaceDiffRowsToolButton(c, "workspace-diff-rows-find-next", "下一个", p, len(controls.matches) > 0) {
				view.find(controls.query, 1)
			}
		} else {
			input := ui.TextInput(c.Key("workspace-diff-rows-line-input"), &controls.lineText).Width(140).MinWidth(0).FontSize(11).Label("跳到原始行").Placeholder("原始行号")
			input.OnShortcut(0, ui.KeyEscape, view.closeControls)
			input.HandleInput(func(event ui.InputEvent) bool {
				if event.Kind != ui.InputKeyDown || event.Mods != 0 {
					return false
				}
				if event.Key == ui.KeyEscape {
					view.closeControls()
					return true
				}
				if event.Key == ui.KeyEnter {
					view.submitLine()
					return true
				}
				return false
			})
			if controls.focusLine {
				input.Focus()
				controls.focusLine = false
			}
			if workspaceDiffRowsToolButton(c, "workspace-diff-rows-line-go", "跳转", p, true) {
				view.submitLine()
			}
			ui.Spacer(c)
		}
		if workspaceDiffRowsToolButton(c, "workspace-diff-rows-control-close", "关闭", p, true) {
			view.closeControls()
		}
	})
	if view.changedOnly && controls.searchOpen {
		label(c, "仅在改动行中查找", 10, p.muted).Padding(0, 8).NoWrap().Shrink(0)
	}
	if controls.message != "" {
		label(c, controls.message, 10, p.muted).Padding(0, 8).NoWrap().Shrink(0)
	}
}

func (view *workspaceDiffRowsView) submitLine() {
	number, err := strconv.Atoi(strings.TrimSpace(view.controls.lineText))
	if err != nil || number <= 0 {
		view.controls.message = "请输入有效的原始行号"
		return
	}
	if view.goToLine(view.controls.old, number) {
		view.controls.returnFocus = true
	}
}

func (view *workspaceDiffRowsView) currentMatch() (workspaceDiffRowsMatch, bool) {
	controls := &view.controls
	if !controls.searchOpen || controls.match < 0 || controls.match >= len(controls.matches) {
		return workspaceDiffRowsMatch{}, false
	}
	return controls.matches[controls.match], true
}

func (view *workspaceDiffRowsView) revealMatchX(c *ui.Context, p palette) {
	if !view.controls.revealMatch {
		return
	}
	view.controls.revealMatch = false
	match, found := view.currentMatch()
	if !found {
		return
	}
	cell, scroll, width := view.sourceRows[match.sourceIndex].new, &view.newScroll, view.newViewportWidth
	if view.controls.old {
		cell, scroll, width = view.sourceRows[match.sourceIndex].old, &view.oldScroll, view.oldViewportWidth
	}
	if cell.line == nil || width <= 0 {
		return
	}
	left, _ := c.MeasureText(0, workspaceDiffTextSpan(cell.line.Text[:match.start], p.text))
	right, _ := c.MeasureText(0, workspaceDiffTextSpan(cell.line.Text[:match.end], p.text))
	left, right = left+76, right+76
	if left < scroll.X {
		scroll.X = max(0, left-20)
	} else if right > scroll.X+width {
		scroll.X = max(0, right-width+20)
	}
}

func workspaceDiffRowsMatchSpans(spans []ui.Span, match workspaceDiffRowsMatch, p palette) []ui.Span {
	result := make([]ui.Span, 0, len(spans)+2)
	offset := 0
	for _, span := range spans {
		end := offset + len(span.Text)
		start, stop := max(offset, match.start), min(end, match.end)
		if start >= stop {
			result = append(result, span)
		} else {
			if start > offset {
				plain := span
				plain.Text = span.Text[:start-offset]
				result = append(result, plain)
			}
			hit := span
			hit.Text, hit.Color, hit.Background = span.Text[start-offset:stop-offset], p.text, p.amberBg
			result = append(result, hit)
			if stop < end {
				plain := span
				plain.Text = span.Text[stop-offset:]
				result = append(result, plain)
			}
		}
		offset = end
	}
	return result
}
