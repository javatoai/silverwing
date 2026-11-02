package markdownui

import (
	"context"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown/parser"
	"strings"
	"testing"
)

func TestDailyGeneratedLinksAndResourcesKeepLiteralEntities(t *testing.T) {
	for _, kind := range []string{"link", "image", "resource"} {
		t.Run(kind, func(t *testing.T) {
			s := NewEditorState(Document{ID: "literal-entity", Path: "D:/synthetic/doc.md"})
			opts := EditorOptions{OnImportResources: func(_ context.Context, _ ImportRequest, complete func(ImportResult, error)) {
				complete(ImportResult{Resources: []ImportedResource{{Target: "doc.assets/a&copy;.png", Text: "&copy;", Image: true}}}, nil)
			}}
			v := ui.NewTester(func(c *ui.Context) { MarkdownEditor(c, s, opts).Fill() }, 800, 500)
			s.native.Focus()
			v.Frame()
			target := "https://example.com/a&copy;"
			isImage := kind != "link"
			if kind == "resource" {
				target = "doc.assets/a&copy;.png"
				s.importResources(ImportRequest{Files: []string{"D:/synthetic/a&copy;.png"}})
			} else {
				command := InsertLink
				if isImage {
					command = InsertImage
				}
				if err := s.Execute(EditorCommand{Kind: command, Text: "&copy;", Target: target}); err != nil {
					t.Fatal(err)
				}
			}
			d := parser.Parse(s.Snapshot().Source)
			found, label := false, false
			for b := 1; b < d.Len(); b++ {
				for _, in := range d.Inlines(b) {
					if in.Kind == parser.Text && in.Text == "&copy;" {
						label = true
					}
					if (in.Kind == parser.Link && !isImage || in.Kind == parser.Image && isImage) && in.Dest == target {
						found = true
					}
				}
			}
			if !found || !label {
				t.Fatalf("generated link entity changed: %q", s.Snapshot().Source)
			}
			v.Command("undo")
			if s.Snapshot().Source != "" {
				t.Fatal("generated link did not undo once")
			}
		})
	}
}

func TestDailyLinkSelectionDoesNotReencodeExistingMarkdown(t *testing.T) {
	for _, source := range []string{"a &amp; b", `a\*b`, "a]b", "a[b", "**bold**", "`a]b`", `C:\`, `a\`} {
		for _, kind := range []string{"command", "url-paste", "dialog"} {
			t.Run(source+kind, func(t *testing.T) {
				s := NewEditorState(Document{ID: "selected-raw", Source: source})
				v := ui.NewTester(func(c *ui.Context) { MarkdownEditor(c, s, EditorOptions{}).Fill() }, 800, 500)
				s.native.SetSelection(0, len(source))
				s.native.Focus()
				v.Frame()
				switch kind {
				case "command":
					if err := s.Execute(EditorCommand{Kind: InsertLink, Target: "https://example.com"}); err != nil {
						t.Fatal(err)
					}
				case "url-paste":
					v.SetClipboard("https://example.com")
					v.Key(ui.Cmd, ui.KeyV)
				case "dialog":
					v.Key(ui.Cmd, ui.KeyK)
					if source == "a &amp; b" && s.insert.text != "a & b" {
						t.Fatal("dialog showed encoded source instead of display text")
					}
					v.Type("https://example.com")
					button, ok := v.Find("插入")
					if !ok {
						t.Fatal("insert dialog missing")
					}
					v.Press(button.X+button.W/2, button.Y+button.H/2)
					v.Release(button.X+button.W/2, button.Y+button.H/2)
				}
				plain := func(source string) string {
					d := parser.Parse(source)
					var text strings.Builder
					for b := 1; b < d.Len(); b++ {
						text.WriteString(parser.PlainText(d.Inlines(b)))
					}
					return text.String()
				}
				d := parser.Parse(s.Snapshot().Source)
				found := false
				for b := 1; b < d.Len(); b++ {
					for _, in := range d.Inlines(b) {
						found = found || in.Kind == parser.Link && in.Dest == "https://example.com"
					}
				}
				if !found || plain(s.Snapshot().Source) != plain(source) {
					t.Fatalf("selected source changed rendered text: %q => %q", source, s.Snapshot().Source)
				}
				v.Command("undo")
				if s.Snapshot().Source != source {
					t.Fatal("selected link did not undo once")
				}
			})
		}
	}
}

func TestDailyLocalImageTypedPathKeepsDefaultVisibleDescription(t *testing.T) {
	const source = "a &amp; b"
	s := NewEditorState(Document{ID: "default-image-description", Source: source, Path: "D:/synthetic/doc.md"})
	var description string
	opts := EditorOptions{OnImportResources: func(_ context.Context, r ImportRequest, complete func(ImportResult, error)) {
		description = r.Text
		complete(ImportResult{Resources: []ImportedResource{{Target: "doc.assets/photo.png", Text: "photo.png", Image: true}}}, nil)
	}}
	v := ui.NewTester(func(c *ui.Context) { MarkdownEditor(c, s, opts).Fill() }, 800, 500)
	s.native.SetSelection(0, len(source))
	s.native.Focus()
	v.Frame()
	if err := s.Execute(EditorCommand{Kind: InsertImage}); err != nil {
		t.Fatal(err)
	}
	v.Frame()
	v.Type("D:/synthetic/photo.png")
	button, ok := v.Find("插入")
	if !ok {
		t.Fatal("image panel missing")
	}
	v.Press(button.X+button.W/2, button.Y+button.H/2)
	v.Release(button.X+button.W/2, button.Y+button.H/2)
	if description != "a & b" {
		t.Fatalf("local image default description lost: %q", description)
	}
	d := parser.Parse(s.Snapshot().Source)
	var text strings.Builder
	for b := 1; b < d.Len(); b++ {
		text.WriteString(parser.PlainText(d.Inlines(b)))
	}
	if text.String() != "a & b" {
		t.Fatal("image description was reencoded")
	}
	v.Command("undo")
	if s.Snapshot().Source != source {
		t.Fatal("local image not one undo")
	}
}

func TestDailyResourceAndHTMLCannotDamageUnselectedStyle(t *testing.T) {
	for _, kind := range []string{"resource", "html"} {
		t.Run(kind, func(t *testing.T) {
			const source = "*italic* tail"
			s := NewEditorState(Document{ID: "cross-resource", Source: source, Path: "D:/synthetic/doc.md"})
			var reported error
			opts := EditorOptions{OnError: func(err error) { reported = err }, OnImportResources: func(_ context.Context, _ ImportRequest, complete func(ImportResult, error)) {
				complete(ImportResult{Resources: []ImportedResource{{Target: "doc.assets/photo.png", Text: "photo.png", Image: true}}}, nil)
			}, ReadClipboard: func(context.Context) (ClipboardContent, error) {
				return ClipboardContent{Text: "heading", HTML: "<h2>heading</h2>"}, nil
			}}
			v := ui.NewTester(func(c *ui.Context) { MarkdownEditor(c, s, opts).Fill() }, 800, 500)
			s.native.SetSelection(3, 11)
			s.native.Focus()
			v.Frame()
			version := s.Snapshot().Version
			if kind == "resource" {
				s.importResources(ImportRequest{Files: []string{"D:/synthetic/photo.png"}})
			} else {
				v.SetClipboard("heading")
				v.Key(ui.Cmd, ui.KeyV)
			}
			if s.Snapshot().Source != source || s.Snapshot().Version != version || reported == nil {
				t.Fatalf("crossed %s damaged source: %q error=%v", kind, s.Snapshot().Source, reported)
			}
		})
	}
}

func TestDailyHTMLPasteMergesAnIdenticalInheritedStyle(t *testing.T) {
	for _, row := range []struct {
		source, html, want string
		at                 int
	}{{"**outer**", "<strong>mid</strong>", "**oumidter**", 4}, {"__outer__", "<strong>mid</strong>", "__oumidter__", 4}, {"*outer*", "<em>mid</em>", "*oumidter*", 3}, {"~~outer~~", "<s>mid</s>", "~~oumidter~~", 4}, {"***outer***", "<strong><em>mid</em></strong>", "***oumidter***", 5}} {
		t.Run(row.source, func(t *testing.T) {
			s := NewEditorState(Document{ID: "inherited", Source: row.source})
			opts := EditorOptions{ReadClipboard: func(context.Context) (ClipboardContent, error) {
				return ClipboardContent{Text: "mid", HTML: row.html}, nil
			}}
			v := ui.NewTester(func(c *ui.Context) { MarkdownEditor(c, s, opts).Fill() }, 800, 500)
			s.native.SetSelection(row.at, row.at)
			s.native.Focus()
			v.Frame()
			v.SetClipboard("mid")
			v.Key(ui.Cmd, ui.KeyV)
			if s.Snapshot().Source != row.want {
				t.Fatalf("inherited style was toggled away: %q", s.Snapshot().Source)
			}
			v.Command("undo")
			if s.Snapshot().Source != row.source {
				t.Fatal("merged paste not one undo")
			}
		})
	}
}
