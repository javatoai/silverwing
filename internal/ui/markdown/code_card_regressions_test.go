package markdownui

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestPublicCodeCardClickLanguageAndUndoShareHostDraftBuffer(t *testing.T) {
	const source = "\ufeffbefore\r\n\r\n```sql  title=\"query\"\r\nSELECT 1;\r\n```\r\n\r\nafter\r\n"
	state := NewEditorState(Document{ID: "code:draft", Path: "D:\\drafts\\query.md", Source: source})
	t.Cleanup(state.Close)
	buffer := state.native.Buffer()
	var drafts []Snapshot
	view := ui.NewTester(func(c *ui.Context) {
		MarkdownEditor(c, state, EditorOptions{OnChange: func(snapshot Snapshot) { drafts = append(drafts, snapshot) }}).Fill()
	}, 700, 500)
	if err := view.Click("SELECT 1;"); err != nil {
		t.Fatal(err)
	}
	view.Key(0, ui.KeyEnd)
	view.Type(" -- 中文😀")
	if got := state.Snapshot().Source; got != strings.Replace(source, "SELECT 1;", "SELECT 1; -- 中文😀", 1) {
		t.Fatalf("public default code card did not edit in place: %q", got)
	}
	if len(drafts) != 1 || drafts[0].Source != state.Snapshot().Source || drafts[0].DocumentID != "code:draft" {
		t.Fatalf("host draft did not receive one committed native edit: %+v", drafts)
	}
	if view.HasText("```sql") || !view.HasText("代码语言") {
		t.Fatalf("public active code card exposed the fence: %q", view.Texts())
	}
	if err := view.Click("代码语言"); err != nil {
		t.Fatal(err)
	}
	if err := view.Click("Python"); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if len(drafts) != 2 || !strings.Contains(drafts[1].Source, "```python  title=\"query\"") || state.native.Buffer() != buffer {
		t.Fatalf("language change bypassed the host draft or replaced its buffer: %+v", drafts)
	}
	if state.document.Path != "D:\\drafts\\query.md" {
		t.Fatal("Markdown language selection changed the document filename")
	}
	view.Command("undo")
	if !strings.Contains(state.Snapshot().Source, "```sql  title=\"query\"") || !strings.Contains(state.Snapshot().Source, "中文😀") {
		t.Fatal("language change was not its own undo step")
	}
	view.Command("undo")
	if state.Snapshot().Source != source {
		t.Fatal("body undo did not restore the full original draft bytes")
	}
	if err := state.SetMode(Source); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if !view.HasText("```sql") || view.HasText("代码语言") {
		t.Fatal("public source mode did not expose the complete Markdown source")
	}
	if err := state.SetMode(Live); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Command("redo")
	if !strings.Contains(state.Snapshot().Source, "中文😀") || state.native.Buffer() != buffer {
		t.Fatal("public display modes did not retain shared code history")
	}
}

func TestPublicCodeCardIMEReadOnlyPreviewAndClose(t *testing.T) {
	const source = "```sql\r\nSELECT 甲😀乙;\r\n```\r\n"
	state := NewEditorState(Document{ID: "code:lifecycle", Source: source})
	t.Cleanup(state.Close)
	readOnly := false
	changes, composing := 0, false
	view := ui.NewTester(func(c *ui.Context) {
		MarkdownEditor(c, state, EditorOptions{ReadOnly: readOnly, OnChange: func(Snapshot) { changes++ }, OnCompositionChanged: func(active bool) { composing = active }}).Fill()
	}, 700, 500)
	native := state.native
	normalized := native.Buffer().String()
	a, z := strings.Index(normalized, "甲"), strings.Index(normalized, "乙")
	native.SetSelection(a, z)
	native.Focus()
	view.Frame()
	view.Compose("候选😀", 4)
	if !composing || changes != 0 || state.Snapshot().Source != source || !view.HasText("候选😀") || view.HasText("```sql") {
		t.Fatalf("public code preedit left the card or published an uncommitted draft: %q", view.Texts())
	}
	view.Type("提交")
	if composing || changes != 1 || !strings.Contains(state.Snapshot().Source, "SELECT 提交乙;") {
		t.Fatal("public code composition did not publish one committed draft")
	}
	view.Command("undo")
	if state.Snapshot().Source != source {
		t.Fatal("public IME undo did not restore original source")
	}
	readOnly = true
	view.Frame()
	version := state.Snapshot().Version
	view.Click("代码语言")
	view.Type("blocked")
	view.SetClipboard("blocked")
	view.Command("paste")
	if state.Snapshot().Version != version || state.Snapshot().Source != source {
		t.Fatal("public read-only code card allowed a mutation")
	}
	if err := state.SetMode(Preview); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	view.Type("blocked")
	if view.HasText("代码语言") || state.Snapshot().Source != source {
		t.Fatal("preview retained an editable code-language control")
	}
	state.Close()
	view.Frame()
	view.Type("blocked")
	if state.Snapshot().Source != source || !view.HasText("Markdown 编辑暂不可用。") {
		t.Fatal("closed public component still accepted code input")
	}
}
