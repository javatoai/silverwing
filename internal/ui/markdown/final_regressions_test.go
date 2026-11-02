package markdownui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestEditorFindRevealsBufferMatchesInBothEditingModes(t *testing.T) {
	for _, mode := range []Mode{Live, Source} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			text := "begin " + strings.Repeat("long active paragraph 中文 content ", 250) + "needle-source-end\n\n[label](https://example.com/hidden-destination)\n"
			state := NewEditorState(Document{ID: "editor-find", Source: text})
			defer state.Close()
			if err := state.SetMode(mode); err != nil {
				t.Fatal(err)
			}
			state.native.SetSelection(0, 0)
			state.native.Focus()
			changes := 0
			view := ui.NewTester(func(c *ui.Context) {
				MarkdownEditor(c, state, EditorOptions{OnChange: func(Snapshot) { changes++ }}).Fill()
			}, 620, 320)
			version := state.Snapshot().Version
			view.Key(ui.Cmd, ui.KeyF)
			if err := view.Click("查找正文"); err != nil {
				t.Fatal(err)
			}
			view.Type("needle-source-end")
			if !view.Focused("查找正文") {
				t.Fatal("find typing lost query focus")
			}
			if err := view.Click("下一个"); err != nil {
				t.Fatal(err)
			}
			a, z := state.native.Selection()
			want := strings.Index(text, "needle-source-end")
			if a != want || z != want+len("needle-source-end") {
				t.Fatalf("find did not reveal native source %d:%d want %d", a, z, want)
			}
			// A settled find request must not keep seizing an editor selection.
			view.Frame()
			state.native.SetSelection(len(text), len(text))
			view.Frame()
			if a, z := state.native.Selection(); a != len(text) || z != len(text) {
				t.Fatal("settled find pulled selection back during rebuild")
			}
			if err := view.Click("下一个"); err != nil {
				t.Fatal(err)
			}
			state.native.Focus()
			view.Frame()
			box, ok := view.TextCaret()
			if !ok || box.Y < 0 || box.Y+box.H > 320 {
				t.Fatalf("offscreen source result was not revealed: %+v %v", box, ok)
			}
			view.Key(ui.Cmd, ui.KeyF)
			if err := view.Click("查找正文"); err != nil {
				t.Fatal(err)
			}
			view.Command("selectAll")
			view.Type("hidden-destination")
			if err := view.Click("下一个"); err != nil {
				t.Fatal(err)
			}
			a, z = state.native.Selection()
			want = strings.Index(text, "hidden-destination")
			if a != want || z != want+len("hidden-destination") {
				t.Fatalf("source-only destination omitted %d:%d", a, z)
			}
			if state.Mode() != mode || state.Snapshot().Version != version || state.Snapshot().Source != text || changes != 0 {
				t.Fatal("find changed document, mode or version")
			}
			state.reader.find.Current = 100
			view.Frame()
			if err := view.Click("上一个"); err != nil {
				t.Fatal(err)
			}
			if state.reader.find.MatchCount() != 1 {
				t.Fatal("source match counter drifted")
			}
		})
	}
}

func TestLinkCodeBracketsKeepExactLabelOnURLChangeRemoveAndUndo(t *testing.T) {
	for _, label := range []string{"`A]B`", "``A`]B``", "**A [B]**", "A\\]B"} {
		t.Run(label, func(t *testing.T) {
			source := "Before [" + label + "](https://example.com) after"
			state := NewEditorState(Document{ID: "label-code", Source: source})
			defer state.Close()
			state.native.Focus()
			view := ui.NewTester(func(c *ui.Context) { MarkdownEditor(c, state, EditorOptions{}).Fill() }, 700, 400)
			// Resolve the actual rendered label through the shared parser helper.
			link := state.native.Buffer().Snapshot().String()
			plain := strings.NewReplacer("`", "", "**", "", "\\]", "]").Replace(label)
			if label == "``A`]B``" {
				plain = "A`]B"
			}
			if err := view.Click(plain); err != nil {
				t.Fatalf("%s: %v (texts=%v)", link, err, view.Texts())
			}
			if err := view.Click("保存"); err != nil {
				t.Fatal(err)
			}
			if state.Snapshot().Source != source {
				t.Fatal("no-op changed code label")
			}
			if err := view.Click(plain); err != nil {
				t.Fatal(err)
			}
			if err := view.Click("链接地址"); err != nil {
				t.Fatal(err)
			}
			view.Command("selectAll")
			view.Type("https://changed.example/next")
			if err := view.Click("保存"); err != nil {
				t.Fatal(err)
			}
			want := "Before [" + label + "](<https://changed.example/next>) after"
			if state.Snapshot().Source != want {
				t.Fatalf("URL save lost label: %q want %q", state.Snapshot().Source, want)
			}
			state.native.Focus()
			view.Frame()
			view.Command("undo")
			if state.Snapshot().Source != source {
				t.Fatal("URL edit undo did not restore label")
			}
			if err := view.Click(plain); err != nil {
				t.Fatal(err)
			}
			if err := view.Click("移除链接"); err != nil {
				t.Fatal(err)
			}
			if state.Snapshot().Source != "Before "+label+" after" {
				t.Fatalf("remove lost markup: %q", state.Snapshot().Source)
			}
			state.native.Focus()
			view.Frame()
			view.Command("undo")
			if state.Snapshot().Source != source {
				t.Fatal("remove undo did not restore exact source")
			}
		})
	}
}
