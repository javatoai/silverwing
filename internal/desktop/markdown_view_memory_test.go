package desktop

import (
	"encoding/json"
	"github.com/egoist/mygo/ui"
	markdownui "silverwing-go/internal/ui/markdown"
	"strings"
	"testing"
)

func TestMarkdownViewMemorySamplesSelectionAndUsesCapturedTaskOnClose(t *testing.T) {
	s := readingObservationFixture(t)
	state := &markdownEditorState{taskID: s.taskUnit.state.tasks[0].id, path: s.materialsUnit.state.documentPath, componentEditor: markdownui.NewEditorState(markdownui.Document{ID: "doc", Source: "abcdefghij"})}
	s.materialsUnit.state.markdown = state
	s.bindMarkdownViewMemory(state)
	state.viewRestored = true
	state.componentEditor.NativeState().SetSelection(2, 5)
	s.observeReadingSession()
	position, _ := readingPositionForBinding(s.readingSession, *state.viewBinding, false)
	if position.Editor == nil || position.Editor.Anchor != 2 || position.Editor.Caret != 5 {
		t.Fatal("selection-only change not sampled")
	}
	state.componentEditor.NativeState().SetSelection(3, 7)
	s.taskUnit.state.selected = 1
	s.closeMarkdownEditor()
	position, _ = readingPositionForBinding(s.readingSession, *state.viewBinding, false)
	if position.Editor == nil || position.Editor.Anchor != 3 || position.Editor.Caret != 7 {
		t.Fatal("close captured wrong task or lost selection")
	}
}

func TestMarkdownViewCacheOptionalDTOBoundsAndIndependentCopy(t *testing.T) {
	p := DefaultReadingPositionSnapshot()
	p.Editor = &markdownui.EditorViewState{DocumentID: "doc", SourceHash: strings.Repeat("a", 64), Anchor: 3, Caret: 8, ScrollY: 99, Mode: markdownui.Live}
	copy := p.bounded()
	copy.Editor.Anchor = 9
	if p.Editor.Anchor != 3 {
		t.Fatal("cache pointer shared with async writer")
	}
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var loaded ReadingPositionSnapshot
	if err = json.Unmarshal(data, &loaded); err != nil || loaded.Editor == nil || *loaded.Editor != *p.Editor {
		t.Fatalf("editor state did not roundtrip: %s %v", data, err)
	}
	var legacy ReadingPositionSnapshot
	if err = json.Unmarshal([]byte(`{"mode":"RENDERED","zoom":100}`), &legacy); err != nil || legacy.Editor != nil {
		t.Fatal("optional cache field broke existing data")
	}
}

func TestMarkdownViewMemorySamplesLatestSelectionOnFinalSessionClose(t *testing.T) {
	s := readingObservationFixture(t)
	state := &markdownEditorState{taskID: s.taskUnit.state.tasks[0].id, path: s.materialsUnit.state.documentPath, componentEditor: markdownui.NewEditorState(markdownui.Document{ID: "doc", Source: "abcdefghij"})}
	s.materialsUnit.state.markdown = state
	s.bindMarkdownViewMemory(state)
	state.viewRestored = true
	state.componentEditor.NativeState().SetSelection(1, 2)
	s.observeReadingSession()
	state.componentEditor.NativeState().SetSelection(4, 8)
	if err := s.closeReadingSession(); err != nil {
		t.Fatal(err)
	}
	position, _ := readingPositionForBinding(s.readingSession, *state.viewBinding, false)
	if position.Editor == nil || position.Editor.Anchor != 4 || position.Editor.Caret != 8 {
		t.Fatal("final exit missed the last caret/selection")
	}
}

func TestMarkdownLateViewCacheDoesNotOverridePublicShortcutIntent(t *testing.T) {
	for _, kind := range []string{"link", "inline-code"} {
		t.Run(kind, func(t *testing.T) {
			s := readingObservationFixture(t)
			state := &markdownEditorState{taskID: s.taskUnit.state.tasks[0].id, path: s.materialsUnit.state.documentPath, componentEditor: markdownui.NewEditorState(markdownui.Document{ID: "doc", Source: ""})}
			s.materialsUnit.state.markdown = state
			s.bindMarkdownViewMemory(state)
			position, save := readingPositionForBinding(s.readingSession, *state.viewBinding, true)
			view := state.componentEditor.GetViewState()
			position.Editor = &view
			save(position)
			s.readingSession.loaded = false
			v := ui.NewTester(func(c *ui.Context) {
				markdownui.MarkdownEditor(c, state.componentEditor, markdownui.EditorOptions{}).Fill()
			}, 800, 500)
			state.componentEditor.NativeState().Focus()
			v.Frame()
			key := ui.KeyK
			if kind == "inline-code" {
				key = ui.KeyBackquote
			}
			v.Key(ui.Cmd, key)
			s.readingSession.loaded = true
			s.restoreMarkdownViewMemory(state)
			v.Frame()
			if kind == "link" {
				if !v.HasText("插入链接") {
					t.Fatal("late cache closed user's insert dialog")
				}
			} else {
				v.Type("x")
				if state.componentEditor.Snapshot().Source != "`x`" {
					t.Fatal("late cache cleared user's future style")
				}
			}
		})
	}
}

func TestMarkdownLateViewCacheDoesNotOverrideWheelOrScrollbar(t *testing.T) {
	for _, kind := range []string{"wheel", "track", "thumb"} {
		t.Run(kind, func(t *testing.T) {
			s := readingObservationFixture(t)
			state := &markdownEditorState{taskID: s.taskUnit.state.tasks[0].id, path: s.materialsUnit.state.documentPath, componentEditor: markdownui.NewEditorState(markdownui.Document{ID: "doc", Source: strings.Repeat("paragraph ordinary text\n\n", 100)})}
			s.materialsUnit.state.markdown = state
			s.bindMarkdownViewMemory(state)
			position, save := readingPositionForBinding(s.readingSession, *state.viewBinding, true)
			view := state.componentEditor.GetViewState()
			view.ScrollY = 1400
			position.Editor = &view
			save(position)
			s.readingSession.loaded = false
			v := ui.NewTester(func(c *ui.Context) {
				markdownui.MarkdownEditor(c, state.componentEditor, markdownui.EditorOptions{}).Fill()
			}, 800, 500)
			switch kind {
			case "wheel":
				v.Scroll(100, 180, 0, 260)
			case "track":
				v.Move(797, 170)
				v.Press(797, 170)
				v.Release(797, 170)
			case "thumb":
				v.Move(797, 12)
				v.Press(797, 12)
			}
			before := state.componentEditor.GetViewState()
			if kind != "thumb" && before.ScrollY == 0 {
				t.Fatal("fixture did not scroll")
			}
			s.readingSession.loaded = true
			s.restoreMarkdownViewMemory(state)
			v.Frame()
			after := state.componentEditor.GetViewState()
			if after.ScrollY != before.ScrollY {
				t.Fatalf("late cache overrode %s: %v => %v", kind, before.ScrollY, after.ScrollY)
			}
			if kind == "thumb" {
				v.Move(797, 140)
				v.Release(797, 140)
				if state.componentEditor.GetViewState().ScrollY == 0 {
					t.Fatal("fixture did not drag thumb")
				}
			}
		})
	}
}

func TestMarkdownLateViewCacheDoesNotOverrideNestedTableScrollbar(t *testing.T) {
	s := readingObservationFixture(t)
	source := "| A | B | C | D | E | F | G | H | I | J |\n|---|---|---|---|---|---|---|---|---|---|\n| a | b | c | d | e | f | g | h | i | j |\n\n" + strings.Repeat("tail paragraph\n\n", 100)
	state := &markdownEditorState{taskID: s.taskUnit.state.tasks[0].id, path: s.materialsUnit.state.documentPath, componentEditor: markdownui.NewEditorState(markdownui.Document{ID: "wide", Source: source})}
	s.materialsUnit.state.markdown = state
	s.bindMarkdownViewMemory(state)
	position, save := readingPositionForBinding(s.readingSession, *state.viewBinding, true)
	view := state.componentEditor.GetViewState()
	view.ScrollY = 1400
	position.Editor = &view
	save(position)
	s.readingSession.loaded = false
	v := ui.NewTester(func(c *ui.Context) {
		markdownui.MarkdownEditor(c, state.componentEditor, markdownui.EditorOptions{}).Fill()
	}, 800, 500)
	v.Move(30, 72)
	v.Press(30, 72)
	if state.componentEditor.NativeState().InteractionVersion() == 0 {
		t.Fatal("nested table thumb did not record intent")
	}
	s.readingSession.loaded = true
	s.restoreMarkdownViewMemory(state)
	v.Frame()
	if state.componentEditor.GetViewState().ScrollY != 0 {
		t.Fatal("late root cache overrode nested table interaction")
	}
	v.Move(130, 72)
	v.Release(130, 72)
}
