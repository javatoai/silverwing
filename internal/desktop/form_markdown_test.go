package desktop

import (
	"context"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/application"
	markdownui "silverwing-go/internal/ui/markdown"

	"github.com/egoist/mygo/ui"
)

type transientFormBackend struct{ actions chan application.Action }

func (*transientFormBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, nil
}

func (backend *transientFormBackend) Dispatch(ctx context.Context, action application.Action) (application.ActionResult, error) {
	select {
	case backend.actions <- action:
	case <-ctx.Done():
		return application.ActionResult{}, ctx.Err()
	}
	<-ctx.Done()
	return application.ActionResult{}, ctx.Err()
}

func transientFormFixture(t *testing.T) (*app, *ui.Tester) {
	t.Helper()
	s := &app{inputs: map[string]*string{}}
	t.Cleanup(s.closeActionFormMarkdown)
	view := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() {
			if s.dialogOpen {
				s.actionDialog(c, setTheme(c, false))
			}
		})
	}, 900, 800)
	return s, view
}

func TestFormMarkdownMetadataLeavesOrdinaryMultilinePlain(t *testing.T) {
	for _, name := range []string{"notes.md", "notes.MARKDOWN", " 说明.Md "} {
		if !formFieldUsesMarkdown(formField{format: formFieldFileBody}, name) {
			t.Fatalf("Markdown file name %q stayed plain", name)
		}
	}
	for _, name := range []string{"notes.txt", "script.sh", "notes.json", "Dockerfile", "notes.md.txt", ""} {
		if formFieldUsesMarkdown(formField{format: formFieldFileBody}, name) {
			t.Fatalf("ordinary file name %q became Markdown", name)
		}
	}
	s, view := transientFormFixture(t)
	s.openForm("fixture", "配置", "", "", "", "", formField{key: "json", label: "JSON 参数", value: "{\"command\": \"build\"}", multiline: true})
	view.Frame()
	if len(s.form.markdown.drafts) != 0 || view.HasText("JSON 参数 Markdown 编辑区") {
		t.Fatal("ordinary multiline field was converted")
	}
}

func TestNewFileMarkdownFormatFollowsNameWithoutLosingDraft(t *testing.T) {
	const source = "\ufeff# 原文\r\n\r\n正文  \r\n"
	s, view := transientFormFixture(t)
	s.openForm("document.create", "新建文件", "", "", "", "", formField{key: "name", label: "文件名", value: "说明.md"}, formField{key: "text", label: "初始内容", value: source, multiline: true, format: formFieldFileBody})
	view.Frame()
	owner := s.form.markdown
	draft := owner.drafts["text"]
	if draft == nil || !view.HasText("初始内容 Markdown 编辑区") || draft.editor.Snapshot().Source != source {
		t.Fatal("Markdown initial body did not use public editor")
	}
	buffer := draft.editor.NativeState().Buffer()
	native := draft.editor.NativeState()
	native.SetSelection(len(buffer.String()), len(buffer.String()))
	native.Focus()
	view.Frame()
	view.Compose("中文😀", 3)
	*s.input("form:name", "") = "说明.txt"
	view.Frame()
	want := source + "中文😀"
	if draft.active || view.HasText("初始内容 Markdown 编辑区") || *s.input("form:text", "") != want || native.Composing() {
		t.Fatalf("Markdown to plain lost IME/source: active=%t source=%q", draft.active, *s.input("form:text", ""))
	}
	*s.input("form:text", "") = want + "\r\nplain"
	view.Frame()
	*s.input("form:name", "") = "说明.markdown"
	view.Frame()
	if !draft.active || s.form.markdown != owner || owner.drafts["text"] != draft || draft.editor.NativeState().Buffer() != buffer || draft.editor.Snapshot().Source != want+"\r\nplain" {
		t.Fatal("plain to Markdown replaced owner or lost text")
	}
}

func TestFormReplacementAndCancelRetainRejectedMarkdownIME(t *testing.T) {
	s, view := transientFormFixture(t)
	s.openForm("instructions.templates.save", "新建模板", "", "", "", "", formField{key: "name", label: "名称", value: "示例"}, formField{key: "content", label: "模板内容", value: "ab", multiline: true, format: formFieldMarkdown})
	view.Frame()
	owner := s.form.markdown
	draft := owner.drafts["content"]
	native := draft.editor.NativeState()
	native.Buffer().SetMaxSourceBytes(3)
	native.SetSelection(2, 2)
	native.Focus()
	view.Frame()
	view.Compose("中文", 2)
	if s.openForm("other", "其它表单", "", "", "", "") {
		t.Fatal("new form silently replaced rejected composition")
	}
	if err := view.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if !s.dialogOpen || s.form.markdown != owner || !native.Composing() || draft.editor.Snapshot().Source != "ab" || draft.error == "" {
		t.Fatal("cancel discarded rejected IME or its owning form")
	}
	native.Buffer().SetMaxSourceBytes(100)
	if !s.captureActionFormMarkdown() || *s.input("form:content", "") != "ab中文" {
		t.Fatal("corrected composition could not be captured")
	}
	if err := view.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if s.dialogOpen || s.form.markdown != nil {
		t.Fatal("captured draft could not close")
	}
}

func TestTemplateFormEntryAndCtrlSKeepOriginalPayload(t *testing.T) {
	for _, edit := range []bool{false, true} {
		t.Run(map[bool]string{false: "new", true: "edit"}[edit], func(t *testing.T) {
			const source = "\ufeff# 模板\r\n\r\n保留 **格式**  \r\n"
			backend := &transientFormBackend{actions: make(chan application.Action, 2)}
			s := &app{inputs: map[string]*string{}, backend: backend, backendGate: make(chan struct{}, 1)}
			s.settingsUnit.state.settingsTemplatesLoaded = true
			if edit {
				s.settingsUnit.state.settingsTemplates = []map[string]any{{"id": "kept-template", "name": "现有模板", "content": source}}
			}
			t.Cleanup(func() {
				s.closeActionFormMarkdown()
				if s.cancelOperation != nil {
					s.cancelOperation()
				}
			})
			view := ui.NewTester(func(c *ui.Context) {
				p := setTheme(c, false)
				ui.Column(c).Fill().Children(func() { s.settingsInstructionTemplates(c, p) })
				if s.dialogOpen {
					s.actionDialog(c, p)
				}
			}, 1000, 800)
			button := map[bool]string{true: "编辑模板", false: "新建模板"}[edit]
			if err := view.Click(button); err != nil {
				t.Fatal(err)
			}
			draft := s.form.markdown.drafts["content"]
			if draft == nil || !view.HasText("模板内容 Markdown 编辑区") {
				t.Fatal("actual settings entry did not render public Markdown editor")
			}
			if edit {
				if *s.input("form:id", "") != "kept-template" || draft.editor.Snapshot().Source != source {
					t.Fatal("template edit changed ID/source during initialization")
				}
			} else {
				*s.input("form:name", "") = "新模板"
				if err := draft.replace(source); err != nil {
					t.Fatal(err)
				}
			}
			native := draft.editor.NativeState()
			native.SetSelection(len(native.Buffer().String()), len(native.Buffer().String()))
			native.Focus()
			view.Frame()
			view.Compose("输入😀", 3)
			view.Key(ui.Cmd, ui.KeyS)
			var action application.Action
			select {
			case action = <-backend.actions:
			case <-time.After(time.Second):
				t.Fatalf("Ctrl+S did not submit template: %q", s.lastError)
			}
			want := source + "输入😀"
			if action.Kind != "instructions.templates.save" || action.Text != want || action.Payload["content"] != want || s.dialogOpen {
				t.Fatalf("template original payload changed: %+v", action)
			}
			if edit && action.Payload["id"] != "kept-template" {
				t.Fatal("editing saved a different template")
			}
			if len(backend.actions) != 0 {
				t.Fatal("one Ctrl+S submitted the form more than once")
			}
		})
	}
}

func TestTransientMarkdownUsesURLInsertionWithoutLocalImport(t *testing.T) {
	s, view := transientFormFixture(t)
	s.openForm("instructions.templates.save", "模板", "", "", "", "", formField{key: "content", label: "模板内容", multiline: true, format: formFieldMarkdown})
	view.Frame()
	draft := s.form.markdown.drafts["content"]
	if err := draft.editor.Execute(markdownui.EditorCommand{Kind: markdownui.InsertImage, Target: "https://example.com/image.png", Text: "图片"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(*s.input("form:content", ""), "https://example.com/image.png") {
		t.Fatal("URL image insertion did not reach original form value")
	}
	if err := draft.editor.Execute(markdownui.EditorCommand{Kind: markdownui.InsertImage}); err != nil {
		t.Fatal(err)
	}
	view.Frame()
	if view.HasText("选择本地图片") {
		t.Fatal("unpersisted form offered local resource import")
	}
}
