package markdownui

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	md "github.com/egoist/mygo/ui/markdown"
	"github.com/egoist/mygo/ui/markdown/parser"
)

func TestStructuredEditorFillsRowHeightUnlessContentOnly(t *testing.T) {
	for _, contentOnly := range []bool{false, true} {
		state := NewEditorState(Document{ID: "layout", Source: "short"})
		defer state.Close()
		var bounds ui.Rect
		ui.NewTester(func(c *ui.Context) {
			ui.Row(c).Fill().Children(func() {
				MarkdownEditor(c, state, EditorOptions{ReaderOptions: ReaderOptions{ContentOnly: contentOnly}}).Grow(1).Draw(func(_ *ui.Painter, r ui.Rect) { bounds = r })
			})
		}, 700, 400)
		if !contentOnly && bounds.H != 400 || contentOnly && bounds.H >= 400 {
			t.Fatalf("ContentOnly=%v editor bounds=%+v", contentOnly, bounds)
		}
	}
}

func TestStructuredRemovingAddressLabelDoesNotRecreateAutolink(t *testing.T) {
	for _, fixture := range []struct {
		label, visible string
		strong         bool
	}{
		{"https://example.com", "https://example.com", false},
		{"someone@example.com", "someone@example.com", false},
		{"www.example.com", "www.example.com", false},
		{"**someone@example.com**", "someone@example.com", true},
		{"**https://example.com**", "https://example.com", true},
		{"`someone@example.com`", "someone@example.com", false},
		{"someone&#64;example.com", "someone@example.com", false},
		{"https://user@example.com/https://other.example", "https://user@example.com/https://other.example", false},
		{"https://example.com/(www.other.example)", "https://example.com/(www.other.example)", false},
	} {
		t.Run(fixture.label, func(t *testing.T) {
			source := "Before [" + fixture.label + "](https://target.example) after.\r\n\r\nuntouched"
			state := NewEditorState(Document{ID: "remove", Source: source})
			defer state.Close()
			state.native.Focus()
			view := ui.NewTester(func(c *ui.Context) { MarkdownEditor(c, state, EditorOptions{}).Fill() }, 700, 500)
			if err := view.Click(fixture.visible); err != nil {
				t.Fatal(err)
			}
			if err := view.Click("移除链接"); err != nil {
				t.Fatal(err)
			}
			got := state.Snapshot().Source
			d := parser.Parse(got)
			for block := 1; block < d.Len(); block++ {
				for _, in := range d.Inlines(block) {
					if in.Kind == parser.Link {
						t.Fatalf("removed label became another link: source=%q link=%+v", got, in)
					}
				}
			}
			blocks := md.TextBlocks(d)
			if len(blocks) != 2 || blocks[0].Text != "Before "+fixture.visible+" after." || blocks[1].Text != "untouched" {
				t.Fatalf("removal changed visible label: %+v source=%q", blocks, got)
			}
			if fixture.strong && !strings.Contains(d.HTML(), "<strong>") {
				t.Fatalf("removal dropped the label's emphasis: %q", got)
			}
			state.native.Focus()
			view.Frame()
			view.Command("undo")
			if state.Snapshot().Source != source {
				t.Fatalf("removal undo did not restore original link bytes: %q", state.Snapshot().Source)
			}
		})
	}
}

func TestStructuredRemovedAddressInTableCopiesItsVisibleLabel(t *testing.T) {
	const source = "| A | B |\n|---|---|\n| [someone@example.com](https://target.example) | right |"
	state := NewEditorState(Document{ID: "remove-table", Source: source})
	defer state.Close()
	view := ui.NewTester(func(c *ui.Context) { MarkdownEditor(c, state, EditorOptions{}).Fill() }, 700, 500)
	if err := view.Click("someone@example.com"); err != nil {
		t.Fatal(err)
	}
	if err := view.Click("移除链接"); err != nil {
		t.Fatal(err)
	}
	d := parser.Parse(state.native.Buffer().String())
	start, end := -1, -1
	for i := 1; i < d.Len(); i++ {
		if d.Block(i).Kind == parser.TableRow && !d.Block(i).Header {
			start, end = d.Block(i+1).Start, d.Block(d.Block(i).Next-1).End
			break
		}
	}
	if start < 0 {
		t.Fatal("link removal broke the rendered table")
	}
	state.native.SetSelection(start, end)
	state.native.Focus()
	view.Frame()
	view.Command("copy")
	if view.Clipboard() != "someone@example.com\tright" {
		t.Fatalf("table copied hidden unlink markup: %q", view.Clipboard())
	}
}

func TestStructuredPublicLinkDragDoesNotOpenPopoverOrNavigate(t *testing.T) {
	const source = "Before [LinkText](https://example.com) after"
	state := NewEditorState(Document{ID: "drag-public", Source: source})
	defer state.Close()
	navigated := 0
	view := ui.NewTester(func(c *ui.Context) {
		MarkdownEditor(c, state, EditorOptions{ReaderOptions: ReaderOptions{OnNavigate: func(LinkTarget) { navigated++ }}}).Fill()
	}, 700, 500)
	box, ok := view.Find("LinkText")
	if !ok {
		t.Fatal("missing link label")
	}
	x, y := box.X+box.W*.15, box.Y+box.H/2
	view.Press(x, y)
	view.Move(box.X+box.W*.85, y)
	view.Release(box.X+box.W*.85, y)
	a, z := state.native.Selection()
	if a == z || view.HasText("编辑链接") || navigated != 0 || state.Snapshot().Source != source {
		t.Fatalf("public drag activated a link: %d:%d navigated=%d texts=%v", a, z, navigated, view.Texts())
	}
	if err := view.ClickWith(ui.Super, "LinkText"); err != nil {
		t.Fatal(err)
	}
	if navigated != 1 || view.HasText("编辑链接") {
		t.Fatalf("command click failed: navigated=%d texts=%v", navigated, view.Texts())
	}
	if err := view.Click("LinkText"); err != nil {
		t.Fatal(err)
	}
	if !view.HasText("编辑链接") {
		t.Fatal("no-movement click lost the anchored link editor")
	}
}
