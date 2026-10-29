package desktop

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

const markdownCopyCode = "\tfmt.Println(\"合成代码\")  \r\n\tnext()"
const markdownCopySource = "复制选区保留\r\n\r\n```go\r\n" + markdownCopyCode + "\r\n```\r\n"

type markdownCopyBackend struct {
	content application.RichClipboardContent
	failure error
	writes  int
	reads   int
}

func (b *markdownCopyBackend) Snapshot(context.Context) (application.Snapshot, error) {
	b.reads++
	return application.Snapshot{}, errors.New("unexpected snapshot")
}
func (b *markdownCopyBackend) Dispatch(context.Context, application.Action) (application.ActionResult, error) {
	b.reads++
	return application.ActionResult{}, errors.New("unexpected business request")
}
func (b *markdownCopyBackend) ReadRichClipboard() (application.RichClipboardContent, error) {
	return b.content, nil
}
func (b *markdownCopyBackend) WriteRichClipboard(content application.RichClipboardContent) error {
	b.writes++
	if b.failure != nil {
		return b.failure
	}
	b.content = content
	return nil
}

func TestMarkdownCodeCopyFeedbackSharedReaders(t *testing.T) {
	for _, route := range []string{"materials", "requirement-body", "requirement-comment", "readonly"} {
		t.Run(route, func(t *testing.T) {
			s := &app{page: "tasks"}
			var render func(*ui.Context, palette)
			var document *markdownEditorState
			switch route {
			case "materials":
				var action application.Action
				var result application.ActionResult
				s, action, result = materialsTextPreviewFixture(t, "synthetic.md", []byte(markdownCopySource))
				s.applyMaterialsActionResult(action, result)
				render = s.materials
			case "requirement-body", "requirement-comment":
				reader := s.requirementReader("", "https://project.feishu.cn/fixture/userstory/detail/1001")
				body := markdownCopySource
				if route == "requirement-comment" {
					body = "合成正文"
					s.updateRequirementReaderComments(reader, application.RequirementComments{Comments: []domain.RequirementComment{{ID: "synthetic", AuthorName: "合成用户", Content: markdownCopySource}}})
					for _, state := range reader.commentMarkdown {
						document = state
					}
				}
				s.updateRequirementReaderBody(reader, application.RequirementContent{Body: body})
				if document == nil {
					document = reader.bodyMarkdown
				}
				render = func(c *ui.Context, p palette) { s.renderRequirementReadonlyMarkdownReader(c, p, reader) }
				t.Cleanup(s.requirementUnit.closeRequirementReaders)
			default:
				document = newReadonlyMarkdownState("synthetic-copy", markdownCopySource, nil)
				render = func(c *ui.Context, p palette) { s.renderReadonlyMarkdownReader(c, p, document) }
			}
			backend := &markdownCopyBackend{}
			s.backend = backend
			t.Cleanup(func() { closeMarkdownPreviewState(document); closeMarkdownPreviewState(s.materialsUnit.state.markdown) })
			tester := ui.NewTester(func(c *ui.Context) {
				p := setTheme(c, route == "requirement-comment")
				ui.Column(c).Fill().Children(func() { render(c, p) })
				s.renderOperationNotifications(c)
			}, 800, 650)
			tester.SetLocale("zh-Hans")
			if document == nil {
				document = s.materialsUnit.state.markdown
			}
			before := document.doc.Source()
			if err := tester.Click("复制代码"); err != nil {
				t.Fatal(err)
			}
			if backend.writes != 1 || backend.content.Text != markdownCopyCode || backend.content.HTML != "" {
				t.Fatalf("code bytes/clipboard payload changed: writes=%d content=%+v", backend.writes, backend.content)
			}
			if s.lastMessage != "已复制" || s.lastError != "" || !tester.HasText("已复制") {
				t.Fatalf("copy did not show lightweight success: message=%q error=%q texts=%q", s.lastMessage, s.lastError, tester.Texts())
			}
			if document.doc.Source() != before || backend.reads != 0 {
				t.Fatal("copy changed source or invoked a business request")
			}
		})
	}
}

func TestMarkdownCodeCopyFeedbackObservedFailure(t *testing.T) {
	s := &app{backend: &markdownCopyBackend{content: application.RichClipboardContent{Text: "previous"}, failure: errors.New("clipboard busy")}, lastMessage: "previous success"}
	state := newReadonlyMarkdownState("failure", markdownCopySource, nil)
	t.Cleanup(func() { closeMarkdownPreviewState(state) })
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		s.renderReadonlyMarkdownReader(c, p, state)
		s.renderOperationNotifications(c)
	}, 800, 650)
	tester.SetLocale("zh-Hans")
	if err := tester.Click("复制代码"); err != nil {
		t.Fatal(err)
	}
	backend := s.backend.(*markdownCopyBackend)
	if backend.writes != 1 || backend.content.Text != "previous" || s.lastMessage != "" || s.lastError != "复制失败：clipboard busy" || tester.HasText("已复制") || !tester.HasText(s.lastError) {
		t.Fatalf("observed failure reported success: message=%q error=%q content=%q", s.lastMessage, s.lastError, backend.content.Text)
	}
}

func TestMarkdownCodeCopyFeedbackFallbackKeepsSelectionAndScroll(t *testing.T) {
	s := &app{}
	source := markdownCopySource + "\n" + strings.Repeat("后续长文内容\n\n", 40)
	state := newReadonlyMarkdownState("selection", source, nil)
	t.Cleanup(func() { closeMarkdownPreviewState(state) })
	tester := ui.NewTester(func(c *ui.Context) {
		p := setTheme(c, false)
		s.renderReadonlyMarkdownReader(c, p, state)
		s.renderOperationNotifications(c)
	}, 800, 650)
	tester.SetLocale("zh-Hans")
	if err := tester.Click("复制选区保留"); err != nil {
		t.Fatal(err)
	}
	tester.Command("selectAll")
	tester.Command("copy")
	selection := tester.Clipboard()
	if !strings.Contains(selection, "复制选区保留") || s.lastMessage != "" {
		t.Fatal("ordinary native copy changed or incorrectly showed success feedback")
	}
	tester.Key(ui.Cmd|ui.Shift, ui.KeyC)
	selectedSource := tester.Clipboard()
	if !strings.Contains(selectedSource, markdownCopyCode) || !strings.Contains(selectedSource, "后续长文内容") {
		t.Fatalf("native Markdown selection did not include the source fixture: %q", selectedSource)
	}
	state.readonlyReader.previewScroll.Y = 4
	tester.Frame()
	beforeScroll := state.readonlyReader.previewScroll.Y
	if err := tester.Click("复制代码"); err != nil {
		t.Fatal(err)
	}
	if tester.Clipboard() != markdownCopyCode || !tester.HasText("已复制") || state.readonlyReader.previewScroll.Y != beforeScroll || beforeScroll == 0 {
		t.Fatalf("fallback copy bytes/feedback/scroll changed: clipboard=%q scroll=%v/%v", tester.Clipboard(), beforeScroll, state.readonlyReader.previewScroll.Y)
	}
	tester.SetClipboard("sentinel")
	// Copy as Markdown remains routed through the same native selection scope
	// while its descendant code button owns focus; no pointer reselect is used.
	tester.Key(ui.Cmd|ui.Shift, ui.KeyC)
	if tester.Clipboard() != selectedSource || state.doc.Source() != source {
		t.Fatal("code copy discarded the native text selection or changed source")
	}
}
