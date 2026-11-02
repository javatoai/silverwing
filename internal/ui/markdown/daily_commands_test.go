package markdownui

import (
	"github.com/egoist/mygo/ui"
	"strings"
	"testing"
)

func dailyPublicEditor(source string) (*EditorState, *ui.Tester) {
	state := NewEditorState(Document{ID: "daily", Source: source, Path: "D:/synthetic/daily.md"})
	view := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { MarkdownEditor(c, state, EditorOptions{}).Fill() })
	}, 900, 600)
	state.native.Focus()
	view.Frame()
	return state, view
}

func TestDailyPublicFormattingCommandsAndFutureTypingUseOneBuffer(t *testing.T) {
	const source = "\ufeffbefore\r\n\r\n中文😀 hello\r\n\r\n<!-- preserve -->"
	s, v := dailyPublicEditor(source)
	buffer := s.native.Buffer()
	a := strings.Index(buffer.String(), "中文")
	z := a + len("中文😀")
	s.native.SetSelection(a, z)
	v.Frame()
	v.Key(ui.Cmd, ui.KeyB)
	if want := strings.Replace(source, "中文😀", "**中文😀**", 1); buffer.OriginalText() != want {
		t.Fatalf("CtrlB failed: %q", buffer.OriginalText())
	}
	if !s.CommandState(EditorCommand{Kind: Bold}).Active {
		t.Fatal("toolbar state missing")
	}
	v.Key(ui.Cmd, ui.KeyB)
	if buffer.OriginalText() != source {
		t.Fatal("toggle failed")
	}
	v.Command("undo")
	v.Command("undo")
	if buffer.OriginalText() != source || s.native.Buffer() != buffer {
		t.Fatal("one source history not preserved")
	}
	s, v = dailyPublicEditor("")
	v.Key(ui.Cmd, ui.KeyB)
	if s.Snapshot().Source != "" {
		t.Fatal("empty format wrote markers")
	}
	v.Type("中文")
	v.Type("😀")
	v.Key(ui.Cmd, ui.KeyB)
	v.Type(" plain")
	if s.Snapshot().Source != "**中文😀** plain" {
		t.Fatalf("future format not maintained/closed: %q", s.Snapshot().Source)
	}
}

func TestDailyPublicBlockCommandsAndUndoKeepUntouchedRanges(t *testing.T) {
	for _, kind := range []CommandKind{Heading, Quote, BulletList, OrderedList, TaskList, InsertCode, InsertTable} {
		s, v := dailyPublicEditor("before\r\n\r\ntext\r\n\r\n<!-- after -->")
		at := strings.Index(s.native.Buffer().String(), "text")
		s.native.SetSelection(at, at+4)
		v.Frame()
		before := s.Snapshot().Source
		if err := s.Execute(EditorCommand{Kind: kind, Level: 2}); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if !strings.HasPrefix(s.Snapshot().Source, "before\r\n\r\n") || !strings.HasSuffix(s.Snapshot().Source, "\r\n\r\n<!-- after -->") {
			t.Fatalf("%s changed surrounding bytes: %q", kind, s.Snapshot().Source)
		}
		v.Command("undo")
		if s.Snapshot().Source != before {
			t.Fatalf("%s not one undo", kind)
		}
	}
}

func TestDailyFormattingAcrossParagraphsTogglesWithoutBreakingBlocks(t *testing.T) {
	const source = "one\r\n\r\ntwo😀"
	s, v := dailyPublicEditor(source)
	s.native.SetSelection(0, len(s.native.Buffer().String()))
	v.Frame()
	v.Key(ui.Cmd, ui.KeyB)
	if s.Snapshot().Source != "**one**\r\n\r\n**two😀**" {
		t.Fatalf("cross paragraph format invalid: %q", s.Snapshot().Source)
	}
	v.Key(ui.Cmd, ui.KeyB)
	if s.Snapshot().Source != source {
		t.Fatalf("cross paragraph toggle invalid: %q", s.Snapshot().Source)
	}
	v.Command("undo")
	v.Command("undo")
	if s.Snapshot().Source != source {
		t.Fatal("format crossed document history")
	}
}
