package markdownui

import (
	"context"
	"github.com/egoist/mygo/ui"
	"strings"
	"testing"
)

func TestDailyClipboardContextURLAndPlainPaste(t *testing.T) {
	for _, kind := range []string{"html", "plain", "code", "table", "url"} {
		source := "before\n\ntext\n\nafter"
		if kind == "code" {
			source = "```sql\nx\n```"
		}
		if kind == "table" {
			source = "| A | B |\n|---|---|\n| x | y |"
		}
		s := NewEditorState(Document{ID: kind, Source: source})
		options := EditorOptions{ReadClipboard: func(context.Context) (ClipboardContent, error) {
			return ClipboardContent{Text: "plain", HTML: "<strong>bold</strong>"}, nil
		}}
		v := ui.NewTester(func(c *ui.Context) { ui.Column(c).Fill().Children(func() { MarkdownEditor(c, s, options).Fill() }) }, 900, 600)
		at := strings.Index(source, "text")
		if kind == "code" || kind == "table" {
			at = strings.Index(source, "x")
		}
		end := at + 1
		if kind == "url" {
			at = strings.Index(source, "text")
			end = at + 4
			options.ReadClipboard = nil
		}
		s.native.SetSelection(at, end)
		s.native.Focus()
		v.Frame()
		v.SetClipboard("plain")
		if kind == "url" {
			v.SetClipboard("https://example.com")
		}
		mods := ui.Cmd
		if kind == "plain" {
			mods |= ui.Shift
		}
		v.Key(mods, ui.KeyV)
		got := s.Snapshot().Source
		if kind == "html" && !strings.Contains(got, "**bold**") {
			t.Fatalf("HTML not converted: %q", got)
		}
		if (kind == "plain" || kind == "code" || kind == "table") && (strings.Contains(got, "**bold**") || !strings.Contains(got, "plain")) {
			t.Fatalf("context paste wrong: %s %q", kind, got)
		}
		if kind == "url" && !strings.Contains(got, "[text](<https://example.com>)") {
			t.Fatalf("URL did not wrap selection: %q", got)
		}
		v.Command("undo")
		if s.Snapshot().Source != source {
			t.Fatalf("%s not one undo", kind)
		}
	}
}

func TestDailyResourceCompletionRejectsStaleClosedAndReadOnlyEditors(t *testing.T) {
	for _, kind := range []string{"ok", "edited", "closed", "readonly", "preview"} {
		s, v := dailyPublicEditor("before")
		s.native.SetSelection(6, 6)
		v.Frame()
		var done func(ImportResult, error)
		s.options.OnImportResources = func(_ context.Context, request ImportRequest, complete func(ImportResult, error)) {
			if request.DocumentID != "daily" || request.Version != s.Snapshot().Version {
				t.Fatal("identity not captured")
			}
			done = complete
		}
		s.importResources(ImportRequest{Files: []string{"synthetic.png"}})
		if done == nil {
			t.Fatal("no callback")
		}
		switch kind {
		case "edited":
			v.Type("new")
		case "closed":
			s.Close()
		case "readonly":
			s.options.ReadOnly = true
		case "preview":
			s.SetMode(Preview)
		}
		before := s.Snapshot().Source
		done(ImportResult{Resources: []ImportedResource{{Target: "./doc.assets/合成图片.png", Text: "图片", Image: true}}}, nil)
		if kind == "ok" {
			if !strings.Contains(s.Snapshot().Source, "![图片](<./doc.assets/合成图片.png>)") {
				t.Fatal("resource reference missing")
			}
			v.Command("undo")
			if s.Snapshot().Source != "before" {
				t.Fatal("resource insertion not one undo")
			}
		} else if s.Snapshot().Source != before {
			t.Fatalf("late %s result changed source", kind)
		}
	}
}
