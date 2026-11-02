package markdownui

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestTypingNewFindMatchKeepsInsertionInsteadOfReplacingTypedText(t *testing.T) {
	for _, mode := range []Mode{Live, Source} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			const source = "before after"
			state := NewEditorState(Document{ID: "new-match", Source: source})
			defer state.Close()
			if err := state.SetMode(mode); err != nil {
				t.Fatal(err)
			}
			view := ui.NewTester(func(c *ui.Context) { MarkdownEditor(c, state, EditorOptions{}).Fill() }, 700, 400)
			view.Key(ui.Cmd, ui.KeyF)
			if err := view.Click("查找正文"); err != nil {
				t.Fatal(err)
			}
			view.Type("needle")
			state.native.SetSelection(0, 0)
			state.native.Focus()
			view.Frame()
			view.Type("needle ")
			if a, z := state.native.Selection(); a != 7 || z != 7 || state.Snapshot().Source != "needle "+source {
				t.Fatalf("new match consumed typed text: %d:%d %q", a, z, state.Snapshot().Source)
			}
			if err := view.Click("下一个"); err != nil {
				t.Fatal(err)
			}
			if a, z := state.native.Selection(); a != 0 || z != 6 {
				t.Fatalf("explicit new-match navigation failed %d:%d", a, z)
			}
		})
	}
}

func TestEditingBeforeSettledFindDoesNotReselectMatch(t *testing.T) {
	for _, mode := range []Mode{Live, Source} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			const source = "before needle after"
			state := NewEditorState(Document{ID: "find-edit", Source: source})
			defer state.Close()
			if err := state.SetMode(mode); err != nil {
				t.Fatal(err)
			}
			view := ui.NewTester(func(c *ui.Context) { MarkdownEditor(c, state, EditorOptions{}).Fill() }, 700, 400)
			view.Key(ui.Cmd, ui.KeyF)
			if err := view.Click("查找正文"); err != nil {
				t.Fatal(err)
			}
			view.Type("needle")
			if err := view.Click("下一个"); err != nil {
				t.Fatal(err)
			}
			view.Frame()
			state.native.SetSelection(0, 0)
			state.native.Focus()
			view.Frame()
			view.Type("z")
			if a, z := state.native.Selection(); a != 1 || z != 1 {
				t.Fatalf("find seized typing caret: %d:%d", a, z)
			}
			if state.Snapshot().Source != "z"+source {
				t.Fatal("typing changed the wrong source range")
			}
			view.Command("undo")
			if a, z := state.native.Selection(); a != 0 || z != 0 || state.Snapshot().Source != source {
				t.Fatalf("find seized undo selection: %d:%d source=%q", a, z, state.Snapshot().Source)
			}
			if err := view.Click("下一个"); err != nil {
				t.Fatal(err)
			}
			if a, z := state.native.Selection(); a != 7 || z != 13 {
				t.Fatalf("explicit next no longer selects match: %d:%d", a, z)
			}
		})
	}
}
