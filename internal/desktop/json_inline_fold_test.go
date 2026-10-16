package desktop

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"silverwing-go/internal/tools"

	"github.com/egoist/mygo/ui"
)

type jsonInlineFoldResult struct {
	Checks       []string
	Observations map[string]any
}

// Production tool page, native pointer/keyboard events, and the tester's
// memory clipboard only. No window, backend dispatch or filesystem is used.
func checkJSONInlineFold(ctx context.Context) (result jsonInlineFoldResult, err error) {
	result.Observations = make(map[string]any)
	if _, ok := ctx.Deadline(); !ok {
		return result, fmt.Errorf("JSON inline fold check requires a hard deadline")
	}
	const source = "{\r\n  \"label\": \"中文😀\",\r\n  \"nested\": {\r\n    \"items\": [\r\n      \"first\",\r\n      \"hidden second\"\r\n    ]\r\n  },\r\n  \"tail\": \"done🪴\"\r\n}\r\n"
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
				"error": state.jsonError, "historyIndex": state.jsonEditor.historyIndex, "folds": state.jsonEditor.folded,
				"selectionStart": state.jsonEditor.selectionStart, "selectionEnd": state.jsonEditor.selectionEnd,
				"lastSelection": state.jsonEditor.lastSelection, "texts": tester.Texts(), "focused": tester.Focused("JSON 源码输入")}
			return fmt.Errorf("JSON inline fold check FAIL: %s", name)
		}
		result.Checks = append(result.Checks, name)
		return nil
	}
	// The real debounced validator returns through Window.Update. A headless
	// surface has no window; deliver its exact production parse result here.
	deliverValidation := func() {
		if state.jsonValidationCancel != nil {
			state.jsonValidationCancel()
			state.jsonValidationCancel = nil
		}
		state.jsonPending = false
		state.validateJSON(state.jsonSource)
		tester.Frame()
	}
	findEditor := func() (ui.Rect, error) {
		bounds, found := tester.Find("JSON 源码输入")
		if !found || bounds.W <= 0 || bounds.H <= 0 {
			return bounds, fmt.Errorf("production JSON editor is not visible: %+v", bounds)
		}
		return bounds, nil
	}
	gutterClick := func(pointer string) error {
		bounds, err := findEditor()
		if err != nil {
			return err
		}
		for line, node := range jsonFoldNodesByVisibleLine(state) {
			if node.Pointer != pointer {
				continue
			}
			gutterWidth := float32(max(72, len(strconv.Itoa(max(1, jsonSourceLineCount(state.jsonSource))))*9+34))
			x := bounds.X - gutterWidth + 13
			y := bounds.Y + state.jsonEditor.lines.Top(line) - state.jsonEditor.scroll.Y + 9
			if y < bounds.Y || y >= bounds.Y+bounds.H {
				return fmt.Errorf("fold %q is outside actual viewport", pointer)
			}
			tester.ClickAt(x, y)
			return nil
		}
		return fmt.Errorf("production gutter has no visible fold %q", pointer)
	}
	copyAll := func() error {
		if err := tester.Click("JSON 源码输入"); err != nil {
			return err
		}
		tester.Key(ui.Cmd, ui.KeyA)
		tester.Key(ui.Cmd, ui.KeyC)
		return nil
	}
	wide, err := findEditor()
	if err != nil {
		return result, err
	}
	result.Observations["wideEditorBounds"] = wide
	if err := check("production tool page has no independent JSON node tree or selection panel",
		!tester.HasText("节点树") && !tester.HasText("搜索路径或值") && !tester.HasText("选中节点") && !tester.HasText("复制 Pointer")); err != nil {
		return result, err
	}
	if err := check("original source editor occupies available wide page width", wide.W >= 1100-150); err != nil {
		return result, err
	}
	tester.SetSize(680, 800)
	narrow, err := findEditor()
	if err != nil {
		return result, err
	}
	result.Observations["narrowEditorBounds"] = narrow
	if err := check("original source editor remains full width on narrow page", narrow.W >= 680-150 && narrow.X+narrow.W <= 681); err != nil {
		return result, err
	}
	tester.SetSize(1100, 800)
	if err := copyAll(); err != nil {
		return result, err
	}
	if err := check("native editor memory clipboard copies complete unmodified CRLF Unicode source", tester.Clipboard() == source && state.jsonSource == source); err != nil {
		return result, err
	}
	originalHistory := len(state.jsonEditor.history)
	if err := gutterClick("/nested"); err != nil {
		return result, err
	}
	if err := check("actual gutter pointer click folds nested source inside original editor",
		state.jsonEditor.folded["/nested"] && strings.Contains(state.jsonEditor.viewSource, " … ") && !strings.Contains(state.jsonEditor.viewSource, "hidden second")); err != nil {
		return result, err
	}
	if err := check("fold changes only projection and preserves original source bytes and history", state.jsonSource == source && len(state.jsonEditor.history) == originalHistory); err != nil {
		return result, err
	}
	if err := gutterClick("/nested"); err != nil {
		return result, err
	}
	if err := check("actual gutter unfold restores exact CRLF Unicode source", state.jsonEditor.viewSource == source && state.jsonSource == source && !state.jsonEditor.folded["/nested"]); err != nil {
		return result, err
	}
	if err := gutterClick("/nested"); err != nil {
		return result, err
	}
	start := utf8.RuneCountInString(source[:strings.Index(source, "done🪴")])
	end := start + utf8.RuneCountInString("done🪴")
	state.jsonEditor.setPendingSourceSelection(start, end)
	tester.Frame()
	tester.Type("finished😀")
	tester.Frame()
	expectedEdited := strings.Replace(source, "done🪴", "finished😀", 1)
	if err := check("native Unicode typing while folded edits only selected original source range", state.jsonSource == expectedEdited); err != nil {
		return result, err
	}
	if err := check("ordinary folded-source edit preserves hidden bytes and expands existing projection",
		strings.Contains(state.jsonSource, "\"hidden second\"") && len(state.jsonEditor.folded) == 0 && state.jsonEditor.viewSource == expectedEdited); err != nil {
		return result, err
	}
	deliverValidation()
	if err := tester.Click("撤销"); err != nil {
		return result, err
	}
	deliverValidation()
	if err := check("production undo button restores exact pre-edit source", state.jsonSource == source && state.jsonEditor.viewSource == source); err != nil {
		return result, err
	}
	if err := tester.Click("重做"); err != nil {
		return result, err
	}
	deliverValidation()
	if err := check("production redo button restores exact edited source", state.jsonSource == expectedEdited && state.jsonEditor.viewSource == expectedEdited); err != nil {
		return result, err
	}
	if err := tester.Click("JSON 源码输入"); err != nil {
		return result, err
	}
	tester.Key(ui.Cmd, ui.KeyZ)
	deliverValidation()
	if err := check("native undo shortcut uses application source history", state.jsonSource == source); err != nil {
		return result, err
	}
	tester.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	deliverValidation()
	if err := check("native redo shortcut restores original range edit", state.jsonSource == expectedEdited); err != nil {
		return result, err
	}
	if err := tester.Click("查找"); err != nil {
		return result, err
	}
	tester.Type("finished")
	tester.Frame()
	if err := check("native find query keeps input focus and matches visible source", state.jsonEditor.finding && state.jsonEditor.findQuery == "finished" && tester.Focused("Find") && len(jsonEditorMatches(state.jsonEditor.projection.text, "finished")) == 1); err != nil {
		return result, err
	}
	tester.Type("😀")
	tester.Frame()
	if err := check("find typing remains in find field and preserves original source", state.jsonEditor.findQuery == "finished😀" && state.jsonSource == expectedEdited && tester.Focused("Find")); err != nil {
		return result, err
	}
	matchStart := utf8.RuneCountInString(expectedEdited[:strings.Index(expectedEdited, "finished😀")])
	if err := check("find maps exact Unicode native editor selection", state.jsonEditor.selectionStart == matchStart && state.jsonEditor.selectionEnd == matchStart+utf8.RuneCountInString("finished😀")); err != nil {
		return result, err
	}
	tester.Key(0, ui.KeyEscape)
	// Keep the find-applied range unchanged and focus it through the existing
	// production pending-selection contract, so native Copy observes that range.
	state.jsonEditor.pendingSelection, state.jsonEditor.pendingFocus = true, true
	tester.Frame()
	tester.Key(ui.Cmd, ui.KeyC)
	if err := check("native copy confirms the find-applied Unicode source selection", tester.Clipboard() == "finished😀"); err != nil {
		return result, err
	}
	const invalid = "{\r\n  \"emoji\": \"😀\",\r\n  \"broken\": ?\r\n}\r\n"
	if err := tester.Click("JSON 源码输入"); err != nil {
		return result, err
	}
	tester.Key(ui.Cmd, ui.KeyA)
	tester.SetClipboard(invalid)
	tester.Key(ui.Cmd, ui.KeyV)
	deliverValidation()
	if err := check("invalid native paste preserves source and reports parser location", state.jsonSource == invalid && !state.jsonValid && state.jsonErrorLine == 3 && state.jsonErrorColumn > 0); err != nil {
		return result, err
	}
	if err := tester.Click("定位错误"); err != nil {
		return result, err
	}
	tester.Key(ui.Cmd, ui.KeyC)
	if err := check("production locate-error action focuses and copies exact invalid character", tester.Focused("JSON 源码输入") && tester.Clipboard() == "?"); err != nil {
		return result, err
	}
	historyBeforeLimit, sourceBeforeLimit := len(state.jsonEditor.history), state.jsonSource
	if err := tester.Click("JSON 源码输入"); err != nil {
		return result, err
	}
	tester.Key(ui.Cmd, ui.KeyA)
	tester.SetClipboard(strings.Repeat("界", tools.MaxTextBytes/3+1))
	tester.Key(ui.Cmd, ui.KeyV)
	tester.Frame()
	if err := check("native oversized paste retains full source and application history", state.jsonSource == sourceBeforeLimit && len(state.jsonEditor.history) == historyBeforeLimit && state.jsonError == developerToolInputSizeError); err != nil {
		return result, err
	}
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Key(ui.Cmd, ui.KeyC)
	if err := check("memory clipboard confirms rejected paste left original editor bytes intact", tester.Clipboard() == sourceBeforeLimit); err != nil {
		return result, err
	}
	result.Observations["physicalInputVerified"] = false
	result.Observations["systemClipboardUsed"] = false
	result.Observations["fullSourceSerializationUsed"] = false
	result.Observations["headlessValidationDelivery"] = true
	return result, nil
}
