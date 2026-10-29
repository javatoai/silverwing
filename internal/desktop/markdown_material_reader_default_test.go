package desktop

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"
	"silverwing-go/internal/editor"

	"github.com/egoist/mygo/ui"
)

func TestMarkdownMaterialReaderHasNoEditingModeAndPreservesDrafts(t *testing.T) {
	const source = "正文 **保留**。\r\n\r\n| 名称 | 内容 |\r\n| --- | --- |\r\n| 表格 | 不变 |\r\n\r\n```go\r\nkeep()  \r\nnext()\n```\r\n"
	s, backend, files := materialTextEditorFixture(t, map[string]string{"reading.md": source})
	s.readingSession = newMemoryReadingSession(s.snapshot.Home)
	content := files["reading.md"]
	s.materialsUnit.state.documents = []application.Document{{Path: content.Path, Name: filepath.Base(content.Path), Kind: "markdown"}}
	s.materialsUnit.state.documentPath, s.materialsUnit.state.documentReadPending = content.Path, true
	identity, err := documents.NewMarkdownDraftIdentity("task", backend.taskPath, backend.root, content.Path)
	if err != nil {
		t.Fatal(err)
	}
	originalDraft := documents.MarkdownDraft{Identity: identity, Content: "kept unsaved draft\r\n", BaselineHash: content.Hash}
	if err := backend.documents.WriteMarkdownDraft(context.Background(), originalDraft); err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(backend.documents.CacheRoot, "materials-markdown-drafts.json")
	cacheBefore, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatal(err)
	}
	// A stale writable payload and recovered draft cannot reactivate editing.
	content.ReadOnly = false
	content.Draft, content.DraftError = &originalDraft, "old draft error must not enter reading"
	s.materialsUnit.state.documentDrafts[content.Path] = "old in-memory draft"
	s.applyMaterialsActionResult(application.Action{Kind: "document.read", TaskID: "task", Path: content.Path},
		application.ActionResult{Path: content.Path, Text: content.Text, Data: content})
	if s.materialsUnit.state.documentEditing || !s.materialsUnit.state.documentReadOnly || s.materialsUnit.state.documentContent != source {
		t.Fatal("Markdown did not bind the disk source as read-only")
	}
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		s.beginOverflowMenuFrame()
		ui.Column(c).Fill().Children(func() { s.materials(c, p) })
		s.endOverflowMenuFrame()
	}, 1100, 900)
	state := s.materialsUnit.state.markdown
	if state == nil || !state.readOnly || state.frameworkMarkdown == nil || state.frameworkMarkdown.original != source ||
		state.frameworkMarkdown.options.CodeBlock != nil || !tester.HasText("Code, go") {
		t.Fatal("Markdown did not use the native reader and default code block")
	}
	if err := tester.Click("正文 保留。"); err != nil {
		t.Fatal(err)
	}
	tester.Type("blocked")
	tester.Key(ui.Ctrl, ui.KeyS)
	if err := tester.Click("更多资料操作"); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"编辑 Markdown", "阅读预览", "立即保存", "Markdown 编辑器", "文本块", "添加段落", "富文本编辑区", "修改待自动保存", "old draft error"} {
		if tester.HasText(forbidden) {
			t.Fatalf("readonly materials exposed editing or draft state: %s", forbidden)
		}
	}
	if err := tester.Click("查看 Markdown 原文"); err != nil {
		t.Fatal(err)
	}
	view := s.currentMaterialsTextPreview()
	if view == nil || !view.ReadOnly || view.Buffer.OriginalText() != source {
		t.Fatal("original-source preview is not read-only")
	}
	tester.ClickAt(500, 150)
	tester.Command("selectAll")
	tester.Command("copy")
	if tester.Clipboard() != source {
		t.Fatal("read-only source copy changed original line endings")
	}
	tester.Type("blocked source edit")
	if view.Buffer.OriginalText() != source {
		t.Fatal("source preview accepted an edit")
	}
	s.applyMarkdownEdit(state, func(doc *editor.Document) error { return doc.ReplaceSource("blocked model edit", false) })
	s.editMarkdownAutosave(content.Path, "blocked late save")
	if err := s.writeMarkdownDraft(context.Background(), s.documentAutosaveKey(content.Path), "blocked draft", false); err != editor.ErrDraftUnsupported {
		t.Fatal("readonly Markdown still has a draft writer", err)
	}
	if failures := flushMaterialTextTest(t, s); len(failures) != 0 {
		t.Fatal(failures)
	}
	if state.doc.Source() != source || s.materialsUnit.state.markdownAutosave.Status(s.documentAutosaveKey(content.Path)).Dirty {
		t.Fatal("a stale mutation changed the readonly document")
	}
	if disk, err := os.ReadFile(content.Path); err != nil || string(disk) != source {
		t.Fatal("reading or typing changed the source file", err)
	}
	if cacheAfter, err := os.ReadFile(cachePath); err != nil || string(cacheAfter) != string(cacheBefore) {
		t.Fatal("reading deleted or rewrote existing Markdown drafts", err)
	}
}

func TestMarkdownMaterialReaderKeepsPlainTextEditors(t *testing.T) {
	for _, name := range []string{"notes.txt", "query.sql", "config.json", "table.csv", "code.go"} {
		t.Run(name, func(t *testing.T) {
			s, _, files := materialTextEditorFixture(t, map[string]string{name: "original\r\n"})
			content := files[name]
			s.materialsUnit.state.documentPath, s.materialsUnit.state.documentReadPending = content.Path, true
			s.applyMaterialsActionResult(application.Action{Kind: "document.read", TaskID: "task", Path: content.Path},
				application.ActionResult{Path: content.Path, Text: content.Text, Data: content})
			if s.materialsUnit.state.documentReadOnly || s.currentMaterialTextEditor() == nil || s.materialsUnit.state.documentContent != content.Text {
				t.Fatal("Markdown readonly policy changed ordinary text binding")
			}
			if name != "config.json" && name != "table.csv" && !s.materialsUnit.state.documentEditing {
				t.Fatal("ordinary source no longer opens for editing")
			}
		})
	}
}

func TestMarkdownMaterialReaderIgnoresPendingEditAndSaveResults(t *testing.T) {
	s, _, files := materialTextEditorFixture(t, map[string]string{"new.md": "# New document\n"})
	content := files["new.md"]
	s.materialsUnit.state.documentPath, s.materialsUnit.state.documentReadPending = content.Path, true
	s.materialsUnit.state.materialsFileManagement = &materialsFileManagementState{taskID: "task", pendingEditPath: content.Path}
	s.applyMaterialsActionResult(application.Action{Kind: "document.read", TaskID: "task", Path: content.Path},
		application.ActionResult{Path: content.Path, Text: content.Text, Data: content})
	if s.materialsUnit.state.documentEditing || !s.materialsUnit.state.documentReadOnly || s.materialsUnit.state.materialsFileManagement.pendingEditPath != "" {
		t.Fatal("an old pending edit activated Markdown editing")
	}
	s.materialsUnit.state.documentDrafts[content.Path] = "preserved in-memory draft"
	s.applyMaterialsActionResult(application.Action{Kind: "document.save", TaskID: "task", Path: content.Path, Text: "stale save"},
		application.ActionResult{Path: content.Path, Data: documents.Content{Hash: "stale hash"}})
	if s.materialsUnit.state.documentHash != content.Hash || s.materialsUnit.state.documentContent != content.Text || s.materialsUnit.state.documentDrafts[content.Path] != "preserved in-memory draft" {
		t.Fatal("a stale save result changed the reader or discarded a draft")
	}
}
