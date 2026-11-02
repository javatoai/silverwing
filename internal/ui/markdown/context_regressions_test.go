package markdownui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func contextFixtureLink(t *testing.T, source string) parser.Inline {
	t.Helper()
	d := parser.Parse(source)
	for block := 1; block < d.Len(); block++ {
		for _, in := range d.Inlines(block) {
			if in.Kind == parser.Link && !in.Auto {
				return in
			}
		}
	}
	t.Fatalf("fixture has no link: %q", source)
	return parser.Inline{}
}

func TestPlainLinkLabelsRetainInlineBlockPunctuation(t *testing.T) {
	for _, fixture := range []struct{ raw, visible string }{
		{"## Heading", "## Heading"}, {"## **Heading**", "## Heading"},
		{"- item", "- item"}, {"1. item", "1. item"}, {"> quote", "> quote"},
		{"---", "---"}, {"A\n===", "A ==="}, {"<i>styled</i>", "styled"},
	} {
		t.Run(fixture.raw, func(t *testing.T) {
			source := "[" + fixture.raw + "](https://example.com)"
			if got := PlainLinkLabel(source, contextFixtureLink(t, source)); got != fixture.visible {
				t.Fatalf("popup label=%q want %q", got, fixture.visible)
			}
		})
	}
}

func TestRemovingLinkKeepsOriginalBlockAndInlinePresentation(t *testing.T) {
	for _, fixture := range []struct{ prefix, label, suffix string }{
		{"", "## **Heading**", ""}, {"", "- item", ""}, {"", "1. item", ""},
		{"", "> quote", ""}, {"", "---", ""}, {"", "```", ""},
		{"", "A\n===", ""}, {"head\n", "===", ""},
		{"> ", "A\n> ===", ""}, {"- ", "A\n  ===", ""},
		{"", "`## Heading`", ""}, {"", "<div>", ""},
		{"", "[ref]: /local", "\nkept\n\n[ref]"},
		{"*", "x*", ""}, {"_", "x_", ""}, {"~~", "x~~", ""},
		{"*", "**bold***", ""}, {"*", "x&#42;", ""},
		{"[", "x", "](/other)"}, {"[[", "x", "](/other)](/third)"},
		{"", "\\[ref\\]", "\n\n[ref]: /other"},
		{"before ", "## Heading", " after"}, {"- ", "## Heading", ""},
		{"> ", "## Heading", ""}, {"# ", "## Heading", ""},
		{"| A | B |\n|---|---|\n| ", "## Heading", " | right |"},
	} {
		t.Run(fixture.prefix+fixture.label, func(t *testing.T) {
			source := fixture.prefix + "[" + fixture.label + "](https://example.com)" + fixture.suffix + "\r\n\r\nuntouched\r\n"
			in := contextFixtureLink(t, source)
			if in.Start != len(fixture.prefix) || in.End != len(fixture.prefix)+len("["+fixture.label+"](https://example.com)") {
				t.Fatal("fixture surrounding syntax changed which link is active")
			}
			state := NewEditorState(Document{ID: "unlink-context", Source: source})
			defer state.Close()
			view := ui.NewTester(func(c *ui.Context) { MarkdownEditor(c, state, EditorOptions{}).Fill() }, 700, 400)
			dialog := &linkEdit{open: true, version: state.Snapshot().Version, start: in.Start, end: in.End, rawLabel: fixture.label}
			state.applyLink(dialog, true)
			if dialog.errorText != "" {
				t.Fatal(dialog.errorText)
			}
			wantHTML := strings.NewReplacer("<a href=\"https://example.com\">", "", "</a>", "").Replace(parser.Parse(source).HTML())
			got := state.Snapshot().Source
			gotHTML := strings.ReplaceAll(parser.Parse(got).HTML(), "<i></i>", "")
			if gotHTML != wantHTML {
				t.Fatalf("unlink changed presentation:\n got %q\nwant %q\nsource %q", gotHTML, wantHTML, got)
			}
			if state.Snapshot().Version != 1 || !strings.HasSuffix(got, "\r\n\r\nuntouched\r\n") {
				t.Fatalf("unlink did not stay atomic/preserve unrelated bytes: %+v", state.Snapshot())
			}
			state.native.Focus()
			view.Frame()
			view.Command("undo")
			if state.Snapshot().Source != source {
				t.Fatal("unlink undo lost original bytes")
			}
		})
	}
}

func TestEditorPreviewRoundtripDoesNotReseizeSettledFindSelection(t *testing.T) {
	for _, mode := range []Mode{Live, Source} {
		for _, query := range []string{"needle", "hidden-destination"} {
			t.Run(fmt.Sprint(mode)+query, func(t *testing.T) {
				source := "needle [label](https://example.com/hidden-destination)\n\ntail"
				state := NewEditorState(Document{ID: "mode-find", Source: source})
				defer state.Close()
				if err := state.SetMode(mode); err != nil {
					t.Fatal(err)
				}
				state.native.Focus()
				view := ui.NewTester(func(c *ui.Context) { MarkdownEditor(c, state, EditorOptions{}).Fill() }, 700, 400)
				view.Key(ui.Cmd, ui.KeyF)
				if err := view.Click("查找正文"); err != nil {
					t.Fatal(err)
				}
				view.Type(query)
				if err := view.Click("下一个"); err != nil {
					t.Fatal(err)
				}
				view.Frame()
				state.native.SetSelection(len(source), len(source))
				for _, next := range []Mode{Preview, mode} {
					if err := state.SetMode(next); err != nil {
						t.Fatal(err)
					}
					view.Frame()
					view.Frame()
				}
				if a, z := state.native.Selection(); a != len(source) || z != len(source) {
					t.Fatalf("mode roundtrip pulled back selection %d:%d", a, z)
				}
				if err := view.Click("下一个"); err != nil {
					t.Fatal(err)
				}
				want := strings.Index(source, query)
				if a, z := state.native.Selection(); a != want || z != want+len(query) {
					t.Fatalf("explicit find stopped navigating: %d:%d", a, z)
				}
				if state.Snapshot().Source != source || state.Snapshot().Version != 0 || state.Mode() != mode {
					t.Fatal("mode/find roundtrip changed document/history")
				}
			})
		}
	}
}

func TestEditorPreviewContextMenuEntersSharedSourceEditor(t *testing.T) {
	state := NewEditorState(Document{ID: "preview-menu", Source: "body"})
	defer state.Close()
	if err := state.SetMode(Preview); err != nil {
		t.Fatal(err)
	}
	state.reader.find.Query, state.reader.find.Open = "body", true
	view := ui.NewTester(func(c *ui.Context) { MarkdownEditor(c, state, EditorOptions{}).Fill() }, 700, 400)
	view.Frame()
	state.native.SetSelection(4, 4)
	if err := view.RightClick("body"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("查看 Markdown 源码"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if state.Mode() != Source {
		t.Fatalf("preview source menu did not enter editor: %v", state.Mode())
	}
	if a, z := state.native.Selection(); a != 4 || z != 4 {
		t.Fatalf("source menu reseized settled find selection: %d:%d", a, z)
	}
	state.native.SetSelection(4, 4)
	state.native.Focus()
	view.Frame()
	view.Type(" added")
	if state.Snapshot().Source != "body added" {
		t.Fatal("preview source menu created a disconnected or readonly source view")
	}
	view.Command("undo")
	if state.Snapshot().Source != "body" {
		t.Fatal("source menu lost shared undo history")
	}
	if err := view.RightClick("body"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("实时编辑"); err != nil {
		t.Fatal(err)
	}
	if state.Mode() != Live || state.Snapshot().Source != "body" {
		t.Fatal("source menu did not return to shared live editor")
	}
}
