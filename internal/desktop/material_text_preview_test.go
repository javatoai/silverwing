package desktop

import (
	"context"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"silverwing-go/internal/application"
	"silverwing-go/internal/documents"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	codeeditor "github.com/egoist/mygo/ui/editor"
)

func materialsTextPreviewFixture(t *testing.T, name string, data []byte) (*app, application.Action, application.ActionResult) {
	t.Helper()
	root := t.TempDir()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	content, err := (&documents.Service{}).ReadMaterialsPreview(root, path)
	if err != nil {
		t.Fatal(err)
	}
	s := &app{page: "tasks", tab: 1, documentsLoaded: true, documentPath: path, documentKind: documentKindForPath(path), documentReadPending: true,
		tasks:          []task{{id: "text-task", path: root, materialsPath: root, materialsStatus: "READY"}},
		documents:      []application.Document{{Path: path, Name: name, Kind: documentKindForPath(path)}},
		documentDrafts: map[string]string{}, documentDirtyMap: map[string]bool{}}
	return s, application.Action{Kind: "document.read", TaskID: "text-task", Path: path}, application.ActionResult{Path: path, Text: content.Text, Data: content}
}

func TestMaterialsDetectedTextUsesReadonlyNativePreview(t *testing.T) {
	const source = "2026-10-15 INFO 中文\r\n\t缩进  保留\n# 日志中的 Markdown 标记\n<script>只作为文本显示</script>\r\n"
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint(dark), func(t *testing.T) {
			s, action, result := materialsTextPreviewFixture(t, "ITS-人工补发测试.log", []byte(source))
			if !s.actionResultRelevant(action) {
				t.Fatal("current read rejected")
			}
			s.applyMaterialsActionResult(action, result)
			if s.documentKind != "text" || !s.documentReadOnly || s.documentEditing || s.documentContent != source {
				t.Fatal("detected text not bound read-only")
			}
			tester := ui.NewTester(func(c *ui.Context) {
				p := setTheme(c, dark)
				s.beginOverflowMenuFrame()
				s.materials(c, p)
				s.endOverflowMenuFrame()
			}, 980, 650)
			tester.ClickAt(400, 100)
			tester.Command("selectAll")
			tester.Command("copy")
			if tester.Clipboard() != source {
				t.Fatalf("native selection copy changed text: %q", tester.Clipboard())
			}
			if tester.HasText("此格式暂不支持预览") || s.markdown != nil {
				t.Fatal("log used unsupported or Markdown path")
			}
			if err := tester.Click("更多资料操作"); err != nil {
				t.Fatal(err)
			}
			if err := tester.Click("复制文件原文"); err != nil {
				t.Fatal(err)
			}
			if tester.Clipboard() != source {
				t.Fatalf("source copy changed whitespace/endings: %q", tester.Clipboard())
			}
			if len(tester.OpenedURLs()) != 0 {
				t.Fatal("preview opened an external application")
			}
			if directory := os.Getenv("SILVERWING_TEXT_PREVIEW_EVIDENCE"); directory != "" {
				file, err := os.Create(filepath.Join(directory, fmt.Sprintf("log-preview-dark-%v.png", dark)))
				if err != nil {
					t.Fatal(err)
				}
				err = png.Encode(file, tester.Image())
				closeErr := file.Close()
				if err != nil || closeErr != nil {
					t.Fatalf("capture: %v %v", err, closeErr)
				}
			}
		})
	}
}

func TestMaterialsDetectedTextNativeIndentSelectionAndIdentity(t *testing.T) {
	const prefix = "first\r\n\t中文缩进  text\n# literal\r\n"
	s, action, result := materialsTextPreviewFixture(t, "indent.log", []byte(prefix+strings.Repeat("long text line\n", 100)))
	s.applyMaterialsActionResult(action, result)
	s.readingSession = &desktopReadingSession{cache: EmptyReadingSnapshot(), scrollStates: map[string]readingScrollBinding{}}
	var owner *nativeCodePreviewState
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		s.prepareDocumentFind()
		owner = s.renderDetectedMaterialsText(c, p)
	}, 700, 350)
	tester.ClickAt(200, 50)
	view := owner.view
	firstTop, _, _ := view.LineGeometry(0)
	secondTop, _, _ := view.LineGeometry(1)
	if view.Font.Family != "monospace" || view.Font.Size != 13 || secondTop-firstTop != 20 {
		t.Fatalf("readonly source typography differs from TextEditor: font=%+v line step=%v", view.Font, secondTop-firstTop)
	}
	if !view.ReadOnly || view.Buffer.OriginalText() != s.documentContent {
		t.Fatal("native text buffer is editable or changed source")
	}
	if view.TabSize != 4 || !strings.HasPrefix(view.Buffer.Line(1), "\t中文缩进  ") {
		t.Fatal("native tab/space indentation was lost")
	}
	view.Select(0, len(view.Buffer.String()))
	tester.Frame()
	tester.Command("copy")
	if tester.Clipboard() != s.documentContent {
		t.Fatal("mixed line ending selection copy changed source")
	}
	tester.Type("must not edit")
	if view.Buffer.OriginalText() != s.documentContent {
		t.Fatal("read-only view accepted input")
	}
	view.SetScrollPosition(codeeditor.ScrollPosition{Y: 300})
	tester.Frame()
	saved := s.readingScrollState("text").Y
	if saved <= 0 {
		t.Fatal("native scroll was not recorded")
	}
	oldPath, oldSource := s.documentPath, s.documentContent
	s.documentPath = filepath.Join(filepath.Dir(oldPath), "other.blob")
	s.documentContent = "new document"
	tester.Frame()
	if owner.view == view || owner.view.Buffer.OriginalText() != "new document" || owner.view.ScrollPosition().Y != 0 {
		t.Fatal("new document reused old source or scroll")
	}
	s.documentPath, s.documentContent = oldPath, oldSource
	tester.Frame()
	if got := owner.view.ScrollPosition().Y; got < float64(saved)-1 || got > float64(saved)+1 {
		t.Fatalf("old document scroll not restored: %v want %v", got, saved)
	}
}

func TestMaterialsDetectedBinaryKeepsSystemOpenPrompt(t *testing.T) {
	s, action, result := materialsTextPreviewFixture(t, "fake.log", []byte{'a', 0, 'b'})
	s.applyMaterialsActionResult(action, result)
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		s.beginOverflowMenuFrame()
		s.materials(c, p)
		s.endOverflowMenuFrame()
	}, 980, 650)
	if !tester.HasText("使用系统应用打开") || s.documentKind != "unsupported" || s.documentContent != "" {
		t.Fatal("binary did not retain explicit system-open prompt")
	}
	if len(tester.OpenedURLs()) != 0 {
		t.Fatal("system application opened without a click")
	}
}

func TestMaterialsDetectedTextLateResultsStayScoped(t *testing.T) {
	s, oldAction, result := materialsTextPreviewFixture(t, "old.log", []byte("old result"))
	ctx, cancel := context.WithCancel(context.Background())
	scope, _ := s.materialsFileScope(oldAction.TaskID)
	old := &materialsFileJob{key: "read", scope: scope, action: oldAction, window: &mygo.Window{}, ctx: ctx, cancel: cancel}
	newer := &materialsFileJob{key: "read"}
	state := &materialsFileJobState{current: map[string]*materialsFileJob{"read": newer}}
	s.documentPath = filepath.Join(scope.root, "new.log")
	s.documentContent = "new selection"
	s.documentReadPending = true
	s.finishMaterialsFileJob(state, old, result, nil)
	if s.documentContent != "new selection" || !s.documentReadPending || state.current["read"] != newer || ctx.Err() == nil {
		t.Fatal("superseded file read replaced current selection or request")
	}
	if s.actionResultRelevant(oldAction) {
		t.Fatal("late file result is relevant")
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	old = &materialsFileJob{key: "task-read", scope: scope, action: oldAction, window: &mygo.Window{}, ctx: ctx2, cancel: cancel2}
	state.current[old.key] = old
	s.tasks = append(s.tasks, task{id: "another-task", path: t.TempDir(), materialsPath: t.TempDir(), materialsStatus: "READY"})
	s.selected = 1
	s.finishMaterialsFileJob(state, old, result, nil)
	if s.documentContent != "new selection" || s.actionResultRelevant(oldAction) {
		t.Fatal("late task read entered another task")
	}
}
