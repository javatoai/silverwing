package desktop

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
)

type jsonSearchGutterResult struct {
	Checks, Failures []string
	Boundary         string
}

func checkJSONSearchGutter(ctx context.Context) (report jsonSearchGutterResult, err error) {
	report.Boundary = "Production JSON UI and gutter mapping; Tester keyboard/frames and memory selection, no native window, clipboard or external actions"
	check := func(name string, ok bool) {
		if ok {
			report.Checks = append(report.Checks, name)
		} else {
			report.Failures = append(report.Failures, name)
		}
	}
	fixture := func(source string) (*app, *toolPageState, *ui.Tester) {
		state := newToolPageState(source)
		s := &app{page: "tools", tools: state, configJSON: `{"toolVisibility":{"json":true,"dateTime":false,"textDiff":false,"killPort":false}}`}
		tester := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Children(func() { s.renderDeveloperTools(c) }) }, 1100, 800)
		return s, state, tester
	}
	for _, newline := range []string{"\n", "\r\n", "\r"} {
		if err := ctx.Err(); err != nil {
			return report, err
		}
		source := strings.ReplaceAll("{\n  \"nested\": {\n    \"first\": \"hidden😀\",\n    \"last\": \"second\"\n  },\n  \"tail\": \"visible\"\n}\n", "\n", newline)
		s, state, tester := fixture(source)
		history := len(state.jsonEditor.history)
		s.toggleJSONFold(state, "/nested")
		tester.Frame()
		lineNumbers := jsonEditorSourceLineNumbers(state.jsonSource, state.jsonEditor.projection)
		viewLines := strings.Split(strings.ReplaceAll(state.jsonEditor.projection.text, "\r\n", "\n"), "\n")
		tail := -1
		for i, line := range viewLines {
			if strings.Contains(line, `"tail"`) {
				tail = i
				break
			}
		}
		check(fmt.Sprintf("%q folded gutter retains original tail line six", newline), tail >= 0 && tail < len(lineNumbers) && lineNumbers[tail] == 6)
		_ = tester.Click("JSON 源码输入")
		state.jsonEditor.finding = true
		state.jsonEditor.findQuery = "HIDDEN😀"
		state.jsonEditor.findChanged = true
		tester.Frame()
		check(fmt.Sprintf("%q hidden case-insensitive match reveals folded ancestor", newline), !state.jsonEditor.folded["/nested"] && strings.Contains(state.jsonEditor.viewSource, "hidden😀"))
		check(fmt.Sprintf("%q search selects exact source and preserves history", newline), jsonEditorSelectedSource(state, state.jsonEditor.selectionStart, state.jsonEditor.selectionEnd) == "hidden😀" && state.jsonSource == source && len(state.jsonEditor.history) == history)
		state.cancelWork()
	}
	const source = "{\r\n \"outer\": {\r\n  \"inner\": [\r\n   \"needle😀\", \"needle二\"\r\n  ]\r\n },\r\n \"other\": {\r\n  \"kept\": true\r\n }\r\n}\r\n"
	s, state, tester := fixture(source)
	defer state.cancelWork()
	for _, pointer := range []string{"/outer/inner", "/outer", "/other"} {
		s.toggleJSONFold(state, pointer)
	}
	tester.Frame()
	history := len(state.jsonEditor.history)
	state.jsonEditor.finding = true
	state.jsonEditor.findQuery = "needle"
	state.jsonEditor.findChanged = true
	tester.Frame()
	check("search reveals both folded ancestors but retains unrelated fold", !state.jsonEditor.folded["/outer"] && !state.jsonEditor.folded["/outer/inner"] && state.jsonEditor.folded["/other"])
	state.jsonEditor.findIndex = 1
	state.jsonEditor.findChanged = true
	tester.Frame()
	want := utf8.RuneCountInString(source[:strings.LastIndex(source, "needle")])
	check("next match maps full-source ordinal to projected selection", originalOffset(state.jsonEditor.projection, state.jsonEditor.selectionStart) == want && jsonEditorSelectedSource(state, state.jsonEditor.selectionStart, state.jsonEditor.selectionEnd) == "needle")
	state.jsonEditor.findQuery = "absent"
	state.jsonEditor.findChanged = true
	tester.Frame()
	check("no match preserves unrelated folding", state.jsonEditor.folded["/other"] && state.jsonEditor.findIndex == 0)
	check("nested search preserves source revision and undo history", state.jsonSource == source && len(state.jsonEditor.history) == history && state.jsonRevision == 1)
	tester.SetSize(350, 800)
	check("native wrapped rows keep logical original line mapping", state.jsonEditor.lines.Count() == len(jsonEditorSourceLineNumbers(source, state.jsonEditor.projection)) && slices.Contains(jsonEditorSourceLineNumbers(source, state.jsonEditor.projection), 10))
	if len(report.Checks)+len(report.Failures) != 14 {
		return report, fmt.Errorf("expected14, got%d", len(report.Checks)+len(report.Failures))
	}
	if len(report.Failures) > 0 {
		return report, fmt.Errorf("JSON search/gutter failed%d", len(report.Failures))
	}
	return report, nil
}
