package desktop

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestJSONToolValidationKeepsEditorGeometry(t *testing.T) {
	for _, width := range []int{960, 360} {
		for _, dark := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d-dark-%t", width, dark), func(t *testing.T) {
				s := &app{
					toolsUnit: toolsController{state: toolsState{toolTab: "JSON"}},
					page:      "tools",
					inputs:    map[string]*string{},
				}
				state := s.ensureToolState()
				state.jsonSource = "{\n  \"a\": 2\n}"
				state.validateJSON(state.jsonSource)
				tester := ui.NewTester(func(c *ui.Context) {
					p := setTheme(c, dark)
					ui.Column(c).Fill().Padding(10).Children(func() { s.renderJSONTool(c, p, state) })
				}, width, 480)
				before, ok := tester.Find("JSON 源码输入")
				if !ok {
					t.Fatal("editor missing")
				}
				for _, change := range []func(){
					func() { state.jsonPending, state.jsonError = true, "" },
					func() {
						state.jsonPending, state.jsonError, state.jsonErrorLine, state.jsonErrorColumn = false, "very long invalid JSON description that should not change geometry", 2, 7
					},
					func() { state.jsonPending, state.jsonError, state.jsonErrorLine = false, "", 0 },
				} {
					change()
					tester.Frame()
					after, _ := tester.Find("JSON 源码输入")
					if after != before {
						t.Fatalf("validation moved editor: before=%+v after=%+v", before, after)
					}
				}
			})
		}
	}
}

func TestJSONToolEditingValidationPreservesSourceCaretAndScroll(t *testing.T) {
	s := &app{
		toolsUnit: toolsController{state: toolsState{toolTab: "JSON"}},
		page:      "tools",
		inputs:    map[string]*string{},
	}
	state := s.ensureToolState()
	defer state.cancelWork()
	source := "{\n \"a\": [\n" + strings.Repeat("    \"中文🪴,}\",\n", 60) + " ],\n}"
	state.jsonSource = source
	state.validateJSON(source)
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		ui.Column(c).Fill().Padding(10).Children(func() { s.renderJSONTool(c, p, state) })
	}, 480, 480)
	if err := tester.Click("JSON 源码输入"); err != nil {
		t.Fatal(err)
	}
	tester.Key(ui.Cmd, ui.KeyA)
	tester.Type(source + "@")
	if state.jsonSource != source+"@" {
		t.Fatal("edit rewrote input")
	}
	before, _ := tester.Find("JSON 源码输入")
	tester.Scroll(before.X+before.W/2, before.Y+before.H/2, 0, -160)
	selection, scroll := state.jsonEditor.lastSelection, state.jsonEditor.scroll.Y
	state.validateJSON(state.jsonSource)
	tester.Frame()
	after, _ := tester.Find("JSON 源码输入")
	if before != after || !tester.Focused("JSON 源码输入") || state.jsonEditor.lastSelection != selection || state.jsonEditor.scroll.Y != scroll {
		t.Fatalf("validation moved editor, focus, caret or scroll: %+v %+v %v %v", before, after, scroll, state.jsonEditor.scroll.Y)
	}
	if state.jsonErrorLine == 0 {
		t.Fatal("real error lost location")
	}
	if directory := os.Getenv("SILVERWING_JSON_EVIDENCE_DIR"); directory != "" {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		file, err := os.Create(filepath.Join(directory, "json-error-fixed-header.png"))
		if err != nil {
			t.Fatal(err)
		}
		encodeErr := png.Encode(file, tester.Image())
		closeErr := file.Close()
		if encodeErr != nil || closeErr != nil {
			t.Fatalf("capture: %v %v", encodeErr, closeErr)
		}
	}
	// Undo the raw edit, then validate the original trailing-comma document.
	s.moveJSONEditorHistory(state, false)
	state.validateJSON(state.jsonSource)
	tester.Frame()
	if state.jsonSource != source || !state.jsonValid {
		t.Fatal("undo or tolerant validation changed the original source")
	}
	s.moveJSONEditorHistory(state, true)
	state.validateJSON(state.jsonSource)
	tester.Frame()
	if state.jsonSource != source+"@" || state.jsonValid {
		t.Fatal("redo no longer restores the invalid edit")
	}
}
