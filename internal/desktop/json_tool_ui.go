package desktop

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"silverwing-go/internal/tools"

	"github.com/egoist/mygo/ui"
	codeeditor "github.com/egoist/mygo/ui/editor"
)

func (s *app) renderJSONTool(c *ui.Context, p palette, state *toolPageState) {
	s.pullJSONNativeInput(state)
	state.syncJSONEditor()
	c.OnShortcut(ui.Cmd, ui.KeyF, func() {
		state.jsonEditor.finding = true
		state.jsonEditor.findChanged = true
	})
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Gap(8).Children(func() {
		ui.Row(c).Height(32).Shrink(0).Gap(6).Children(func() {
			for _, operation := range []struct{ id, label string }{
				{"format", "格式化"}, {"compact", "压缩"}, {"escape", "转义"}, {"unescape", "反转义"},
			} {
				if operation.id == "format" {
					enabled := s.toolPrimaryActionEnabled(state, "JSON")
					if toolButtonWithEnabled(c, "json-op-"+operation.id, operation.label, p, true, enabled) {
						s.runDeveloperToolPrimaryAction(state, "JSON")
					}
				} else if toolButtonWithEnabled(c, "json-op-"+operation.id, operation.label, p, false, s.toolOperationEnabled(state, "tool.json")) {
					s.dispatchJSON(operation.id)
				}
			}
			ui.Spacer(c)
		})
		if state.jsonEditor.findQuery != state.jsonEditor.lastFindQuery {
			state.jsonEditor.lastFindQuery = state.jsonEditor.findQuery
			state.jsonEditor.findIndex = 0
			state.jsonEditor.findChanged = true
		}
		matches := jsonEditorMatches(state.jsonSource, state.jsonEditor.findQuery)
		if len(matches) == 0 {
			state.jsonEditor.findIndex = 0
		} else {
			state.jsonEditor.findIndex = bounded(state.jsonEditor.findIndex, 0, len(matches)-1)
		}
		renderEditor := func() {
			if state.jsonEditor.findChanged {
				matches = jsonEditorMatches(state.jsonSource, state.jsonEditor.findQuery)
				if len(matches) == 0 {
					state.jsonEditor.findIndex = 0
				} else if state.jsonEditor.finding {
					state.jsonEditor.findIndex = bounded(state.jsonEditor.findIndex, 0, len(matches)-1)
					match := matches[state.jsonEditor.findIndex]
					revealJSONProjectionRange(state, match.start, match.end)
					state.jsonEditor.setPendingSelectionWithoutFocus(projectedOffset(state.jsonEditor.projection, match.start), projectedOffset(state.jsonEditor.projection, match.end))
				}
				state.jsonEditor.findChanged = false
			}
			if state.jsonErrorLocatePending && !state.jsonValid {
				start := jsonErrorRuneOffset(state.jsonSource, state.jsonErrorLine, state.jsonErrorColumn)
				revealJSONProjectionRange(state, start, start+1)
				state.jsonEditor.setPendingSourceSelection(start, min(start+1, utf8RuneCount(state.jsonSource)))
				state.jsonErrorLocatePending = false
			}
			ui.Column(c.Key("json-tool-editor")).Grow(1).MinWidth(0).MinHeight(0).Background(p.surface).Border(1, p.line).Radius(12).Clip().Children(func() {
				lineCount := jsonSourceLineCount(state.jsonSource)
				ui.Row(c).Height(32).Shrink(0).Padding(0, 8).Gap(0).Background(p.soft).Children(func() {
					label(c, "JSON", 12, p.text).FontWeight(500).Padding(0, 12, 0, 4)
					width, _ := c.Size()
					if width >= 560 {
						label(c, fmt.Sprintf("%d 字符 · %d 行", tools.UTF16Length(state.jsonSource), lineCount), 11, p.muted).FontWeight(500).Margin(0, 12, 0, 0)
					}
					status, color := "", p.muted
					if state.jsonPending && strings.TrimSpace(state.jsonSource) != "" {
						status = "校验中…"
					} else if state.jsonError != "" {
						status, color = "输入有误", p.amber
						if state.jsonErrorLine > 0 {
							status = fmt.Sprintf("第 %d 行有误", state.jsonErrorLine)
						}
					} else if state.jsonValid {
						status = "格式正确"
					}
					label(c, status, 11, color).Grow(1).MinWidth(0).SingleLine().Tooltip(state.jsonError)
					ui.Row(c.Key("json-error-locate-slot")).Width(28).Height(28).Shrink(0).Children(func() {
						if state.jsonError != "" && state.jsonErrorLine > 0 && !state.jsonPending {
							if jsonEditorIconButton(c, "json-locate-error", "定位错误", "search", p, true, false) {
								state.jsonErrorLocatePending = true
							}
						}
					})
					if jsonEditorIconButton(c, "json-editor-undo", "撤销", "undo", p, state.jsonEditor.historyIndex > 0, false) {
						s.moveJSONEditorHistory(state, false)
					}
					if jsonEditorIconButton(c, "json-editor-redo", "重做", "redo", p, state.jsonEditor.historyIndex+1 < len(state.jsonEditor.history), false) {
						s.moveJSONEditorHistory(state, true)
					}
					if jsonEditorIconButton(c, "json-editor-find", "查找", "search", p, true, state.jsonEditor.finding) {
						state.jsonEditor.finding = !state.jsonEditor.finding
						state.jsonEditor.findChanged = state.jsonEditor.finding
					}
				})
				if state.jsonEditor.finding {
					findBar := ui.FindBar(c, &state.jsonEditor.finding, &state.jsonEditor.findQuery, len(matches), &state.jsonEditor.findIndex)
					if findBar.Changed() {
						state.jsonEditor.findChanged = true
					}
				}
				ui.Divider(c)
				ui.Row(c.Key("json-tool-editor-body")).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
					state.jsonEditor.syncNative()
					gutterWidth := float32(max(72, len(strconv.Itoa(max(1, lineCount)))*9+34))
					gutter := ui.Box(c.Key("tool-json-gutter")).Width(gutterWidth).FillHeight().Background(p.soft.Mix(p.surface, 0.55))
					lineNumbers := jsonEditorGutterLines(gutter, state)
					gutter.Draw(func(painter *ui.Painter, r ui.Rect) {
						const topPadding = float32(0)
						top := r.Y + topPadding - state.jsonEditor.scroll.Y
						painter.Clip(r, 0, func() {
							first := state.jsonEditor.lines.At(state.jsonEditor.scroll.Y)
							for i := first; i < state.jsonEditor.lines.Count() && top+state.jsonEditor.lines.Top(i) < r.Y+r.H; i++ {
								line := i + 1
								if i < len(lineNumbers) {
									line = lineNumbers[i]
								}
								color := state.jsonEditor.native.Theme.LineNumber
								if i == state.jsonEditor.native.CursorPos().Line {
									color = state.jsonEditor.native.Theme.ActiveLineNumber
								}
								span := ui.Span{Text: strconv.Itoa(line), Font: "monospace", Size: textEditorFontSize, Color: color}
								width, _ := painter.MeasureText(0, span)
								painter.RichText(r.X+r.W-8-width, top+state.jsonEditor.lines.Top(i), 0, span)
							}
						})
					})
					foldsByLine := jsonFoldNodesByVisibleLine(state)
					gutter.Children(func() {
						bounds := gutter.Bounds()
						if bounds.H <= 0 || state.jsonEditor.lines.Count() == 0 {
							return
						}
						first := state.jsonEditor.lines.At(state.jsonEditor.scroll.Y)
						last := state.jsonEditor.lines.At(state.jsonEditor.scroll.Y + bounds.H)
						for line := first; line <= last; line++ {
							node, ok := foldsByLine[line]
							if !ok {
								continue
							}
							y := state.jsonEditor.lines.Top(line) - state.jsonEditor.scroll.Y
							if y < -18 || y > bounds.H {
								continue
							}
							mark := "⌄"
							if state.jsonEditor.folded[node.Pointer] {
								mark = "›"
							}
							button := ui.ButtonBase(c.Key("json-gutter-fold-" + node.Pointer)).Absolute().Top(y).Left(4).Width(18).Height(18).Center().Label(mark)
							button.Background(p.soft).Children(func() { label(c, mark, 11, p.muted) })
							if button.Clicked() {
								s.toggleJSONFold(state, node.Pointer)
							}
						}
					})
					findQuery := ""
					if state.jsonEditor.finding {
						findQuery = state.jsonEditor.findQuery
					}
					state.jsonEditor.native.OnInput = s.jsonNativeInput(c, state)
					state.jsonEditor.native.OnLimitExceeded = func() { s.rejectJSONToolInput(state) }
					state.jsonEditor.native.FindHighlights = nil
					currentMatch := jsonEditorProjectedFindIndex(state, matches)
					for index, match := range jsonEditorMatches(state.jsonEditor.projection.text, findQuery) {
						state.jsonEditor.native.FindHighlights = append(state.jsonEditor.native.FindHighlights, codeeditor.FindHighlight{
							Start:   textEditorProjectedByteOffset(state.jsonEditor.projection.text, match.start),
							End:     textEditorProjectedByteOffset(state.jsonEditor.projection.text, match.end),
							Current: index == currentMatch,
						})
					}
					s.bindJSONEditorCommands(c, state)
					editor := TextEditor(c.Key("tool-json-source"), p, &state.jsonEditor.native).Grow(1).FillWidth().MinHeight(0)
					if editor.Focused() && !state.jsonEditor.pendingSelection {
						start, end := state.jsonEditor.nativeSelection()
						state.jsonEditor.lastSelection = jsonEditorSnapshot{
							text: state.jsonSource, selectionStart: originalOffset(state.jsonEditor.projection, start), selectionEnd: originalOffset(state.jsonEditor.projection, end),
						}
					}
					if state.jsonEditor.pendingSelection {
						if state.jsonEditor.pendingFocus {
							editor.Focus()
						}
						state.jsonEditor.selectNative(state.jsonEditor.selectionStart, state.jsonEditor.selectionEnd)
						state.jsonEditor.lastSelection = jsonEditorSnapshot{
							text:           state.jsonSource,
							selectionStart: originalOffset(state.jsonEditor.projection, state.jsonEditor.selectionStart),
							selectionEnd:   originalOffset(state.jsonEditor.projection, state.jsonEditor.selectionEnd),
						}
						state.jsonEditor.pendingSelection = false
						state.jsonEditor.pendingFocus = false
					}
				})
			})
		}
		renderEditor()
	})
}

func jsonNodeByPointer(nodes []tools.JSONSourceNode, pointer string) (tools.JSONSourceNode, bool) {
	for _, node := range nodes {
		if node.Pointer == pointer {
			return node, true
		}
	}
	return tools.JSONSourceNode{}, false
}

func (s *app) dispatchJSON(operation string) {
	state := s.ensureToolState()
	if operation == "validate" {
		state.validateJSON(state.jsonSource)
		return
	}
	if state.jsonValidationCancel != nil {
		state.jsonValidationCancel()
		state.jsonValidationCancel = nil
	}
	state.jsonPending = false
	s.act("tool.json", "", "", "", "", state.jsonSource, map[string]any{"operation": operation, "revision": state.jsonRevision})
}

func utf8RuneCount(source string) int { return utf8.RuneCountInString(source) }

func jsonErrorRuneOffset(source string, line, column int) int {
	if line < 1 {
		return 0
	}
	runes := []rune(source)
	currentLine, offset := 1, 0
	for offset < len(runes) && currentLine < line {
		if runes[offset] == '\r' {
			offset++
			if offset < len(runes) && runes[offset] == '\n' {
				offset++
			}
			currentLine++
		} else if runes[offset] == '\n' {
			offset++
			currentLine++
		} else {
			offset++
		}
	}
	units := max(0, column-1)
	for offset < len(runes) && units > 0 {
		width := utf16.RuneLen(runes[offset])
		if width < 0 {
			width = 1
		}
		if units < width {
			break
		}
		units -= width
		offset++
	}
	return bounded(offset, 0, len(runes))
}

func utf16OffsetToByte(source string, offset int) int {
	if offset <= 0 {
		return 0
	}
	units := 0
	for index, value := range source {
		if units >= offset {
			return index
		}
		width := utf16.RuneLen(value)
		if width < 0 {
			width = 1
		}
		if units+width > offset {
			return index
		}
		units += width
	}
	return len(source)
}

func utf16OffsetToRune(source string, offset int) int {
	if offset <= 0 {
		return 0
	}
	units, runes := 0, 0
	for _, value := range source {
		if units >= offset {
			return runes
		}
		width := utf16.RuneLen(value)
		if width < 0 {
			width = 1
		}
		if units+width > offset {
			return runes
		}
		units += width
		runes++
	}
	return runes
}

func jsonOperationLabel(operation string) string {
	switch operation {
	case "compact":
		return "压缩"
	case "escape":
		return "转义"
	case "unescape":
		return "反转义"
	case "validate":
		return "校验"
	default:
		return "格式化"
	}
}
