package desktop

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

type jsonSourceCommandsResult struct {
	Checks       []string
	Observations map[string]any
}

// Production JSON UI, actual keys/gutter/context-menu events, memory clipboard.
// The final system-command assertions deliberately retain the SDK boundary.
func checkJSONSourceCommands(ctx context.Context) (result jsonSourceCommandsResult, err error) {
	result.Observations = map[string]any{"physicalInputVerified": false, "systemClipboardUsed": false, "externalWrites": false}
	if _, ok := ctx.Deadline(); !ok {
		return result, fmt.Errorf("JSON source commands check requires a deadline")
	}
	const source = "{\r\n  \"label\": \"中文😀\",\r\n  \"nested\": {\r\n    \"items\": [\r\n      \"hidden🪴\",\r\n      \"second\"\r\n    ]\r\n  },\r\n  \"tail\": true\r\n}\r\n"
	state := newToolPageState(source)
	defer state.cancelWork()
	s := &app{page: "tools", tools: state, configJSON: "{\"toolVisibility\":{\"json\":true,\"dateTime\":false,\"textDiff\":false,\"killPort\":false}}"}
	tester := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { s.renderDeveloperTools(c) })
	}, 1100, 800)
	check := func(name string, ok bool) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !ok {
			result.Observations["failureState"] = map[string]any{"source": state.jsonSource, "view": state.jsonEditor.viewSource,
				"clipboard": tester.Clipboard(), "historyIndex": state.jsonEditor.historyIndex, "historyLength": len(state.jsonEditor.history),
				"selectionStart": state.jsonEditor.selectionStart, "selectionEnd": state.jsonEditor.selectionEnd,
				"folds": state.jsonEditor.folded, "focused": tester.Focused("JSON 源码输入"), "menu": tester.Menu()}
			return fmt.Errorf("JSON source commands check FAIL: %s", name)
		}
		result.Checks = append(result.Checks, name)
		return nil
	}
	validate := func() {
		if state.jsonValidationCancel != nil {
			state.jsonValidationCancel()
			state.jsonValidationCancel = nil
		}
		state.jsonPending = false
		state.validateJSON(state.jsonSource)
		tester.Frame()
	}
	fold := func(pointer string) error {
		bounds, found := tester.Find("JSON 源码输入")
		if !found {
			return fmt.Errorf("production JSON source input is absent")
		}
		for line, node := range jsonFoldNodesByVisibleLine(state) {
			if node.Pointer == pointer {
				gutterWidth := float32(max(72, len(strconv.Itoa(max(1, jsonSourceLineCount(state.jsonSource))))*9+34))
				x := bounds.X - gutterWidth + 13
				y := bounds.Y + state.jsonEditor.lines.Top(line) - state.jsonEditor.scroll.Y + 9
				if y < bounds.Y || y >= bounds.Y+bounds.H {
					return fmt.Errorf("fold %q is outside the production viewport", pointer)
				}
				tester.ClickAt(x, y)
				return nil
			}
		}
		return fmt.Errorf("production gutter has no fold %q", pointer)
	}
	menu := func(item string) error {
		tester.Key(0, ui.KeyContextMenu)
		return tester.ChooseMenuItem(item)
	}
	validate()
	node, found := jsonNodeByPointer(state.jsonDoc.Nodes, "/nested")
	if !found {
		return result, fmt.Errorf("fixture lacks the nested node")
	}
	start := utf16OffsetToRune(source, node.StartOffset)
	end := utf16OffsetToRune(source, node.EndOffset)
	fragment := string([]rune(source)[start:end])
	removed := replaceRuneRange(source, start, end, "")
	selectNested := func() {
		state.jsonEditor.setPendingSourceSelection(start, end)
		tester.Frame()
	}
	if err := fold("/nested"); err != nil {
		return result, err
	}
	if err := tester.Click("JSON 源码输入"); err != nil {
		return result, err
	}
	history := len(state.jsonEditor.history)
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(ui.Cmd, ui.KeyC)
	if err := check("folded native select-all copy retains every hidden CRLF Unicode byte", tester.Clipboard() == source && state.jsonSource == source && state.jsonEditor.folded["/nested"] && len(state.jsonEditor.history) == history); err != nil {
		return result, err
	}
	selectNested()
	tester.Key(ui.Cmd, ui.KeyC)
	if err := check("folded partial native copy uses exact original node range", tester.Clipboard() == fragment && strings.Contains(tester.Clipboard(), "hidden🪴") && state.jsonEditor.folded["/nested"]); err != nil {
		return result, err
	}
	tester.Key(ui.Cmd, ui.KeyX)
	validate()
	if err := check("folded native cut copies hidden source and removes only its original range", tester.Clipboard() == fragment && state.jsonSource == removed && len(state.jsonEditor.history) == history+1 && len(state.jsonEditor.folded) == 0); err != nil {
		return result, err
	}
	tester.Key(ui.Cmd, ui.KeyZ)
	validate()
	if err := check("native undo restores the complete folded-cut transaction", state.jsonSource == source); err != nil {
		return result, err
	}
	tester.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	validate()
	if err := check("native redo repeats the exact original range cut", state.jsonSource == removed); err != nil {
		return result, err
	}
	if err := menu("撤销"); err != nil {
		return result, err
	}
	validate()
	if err := check("production context undo restores full source history", state.jsonSource == source); err != nil {
		return result, err
	}
	if err := menu("重做"); err != nil {
		return result, err
	}
	validate()
	if err := check("production context redo uses the same cut history", state.jsonSource == removed); err != nil {
		return result, err
	}
	if err := menu("撤销"); err != nil {
		return result, err
	}
	validate()
	if err := fold("/nested"); err != nil {
		return result, err
	}
	if err := tester.Click("JSON 源码输入"); err != nil {
		return result, err
	}
	if err := menu("复制完整 JSON"); err != nil {
		return result, err
	}
	if err := check("production full-source context copy ignores folded projection", tester.Clipboard() == source && state.jsonEditor.folded["/nested"]); err != nil {
		return result, err
	}
	selectNested()
	if err := menu("复制"); err != nil {
		return result, err
	}
	if err := check("production context selection copy includes hidden original source", tester.Clipboard() == fragment && state.jsonSource == source); err != nil {
		return result, err
	}
	if err := menu("剪切"); err != nil {
		return result, err
	}
	validate()
	if err := check("production context cut retains hidden clipboard bytes and untouched ranges", tester.Clipboard() == fragment && state.jsonSource == removed); err != nil {
		return result, err
	}
	if err := menu("粘贴"); err != nil {
		return result, err
	}
	validate()
	if err := check("production context paste restores exact CRLF Unicode range and caret", state.jsonSource == source && state.jsonEditor.lastSelection.selectionStart == start+utf8.RuneCountInString(fragment) && state.jsonEditor.lastSelection.selectionEnd == start+utf8.RuneCountInString(fragment)); err != nil {
		return result, err
	}
	// Actual native system-edit dispatch, not the application's context menu.
	// Do not waive these assertions while TextArea bypasses InputCommand.
	tester.Command("undo")
	validate()
	if err := check("system Edit undo follows full source application history", state.jsonSource == removed); err != nil {
		return result, err
	}
	tester.Command("redo")
	validate()
	if err := check("system Edit redo restores the exact source paste transaction", state.jsonSource == source); err != nil {
		return result, err
	}
	return result, nil
}
