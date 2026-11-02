package desktop

import (
	"strings"
	"testing"

	"silverwing-go/internal/domain"
	markdownui "silverwing-go/internal/ui/markdown"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo/ui"
)

func scrollTaskCreationToNotes(t *testing.T, view *ui.Tester) ui.Rect {
	t.Helper()
	left, found := view.Find("任务基本信息")
	if !found {
		t.Fatal("actual task dialog has no basic-information scroll")
	}
	view.Scroll(left.X+left.W*.65, left.Y+left.H*.6, 0, 10000)
	view.Frame()
	return left
}

func TestTaskCreationActualLeftScrollHasOwnedMarkdownAndTemplates(t *testing.T) {
	s, view := taskCreationCompactFixture(t, 1100, false, 1)
	state := s.taskUnit.state.taskCreation
	t.Cleanup(func() { closeTaskCreationMarkdown(state) })
	state.templatesError = ""
	state.templates = []domain.AgentTaskTemplate{
		{ID: "first", Name: "模板一", Content: "# 第一份\r\n\r\n原始格式  \r\n"},
		{ID: "second", Name: "模板二", Content: "# 第二份\n\n替换后正文\n"},
	}
	view.Frame()
	left := scrollTaskCreationToNotes(t, view)
	if state.markdown == nil || state.markdown.notes == nil || !view.HasText("任务说明 Markdown 编辑区") || !view.HasText("模板一") || !view.HasText("查看生成说明") {
		t.Fatalf("actual creation entry is missing Markdown controls: %v", view.Texts())
	}
	if _, found := view.Find("任务服务列表"); !found {
		t.Fatal("notes replaced the existing services column")
	}
	if err := view.Click("模板一"); err != nil {
		t.Fatal(err)
	}
	owner := state.markdown
	draft := owner.notes
	buffer := draft.editor.NativeState().Buffer()
	if state.notes != state.templates[0].Content || state.selectedTemplate != "first" || draft.editor.Snapshot().Source != state.notes {
		t.Fatal("template selection did not update the owned Buffer and original notes payload")
	}
	native := draft.editor.NativeState()
	native.SetSelection(len(buffer.String()), len(buffer.String()))
	native.Focus()
	view.Frame()
	view.Type("手工补充😀")
	manual := state.notes
	view.Scroll(left.X+left.W*.65, left.Y+left.H*.6, 0, 10000)
	view.Frame()
	if err := view.Click("模板二"); err != nil {
		t.Fatal(err)
	}
	if state.pendingTemplate != "second" || state.notes != manual || !view.HasText("保留当前内容") {
		t.Fatal("dirty template selection bypassed replacement confirmation")
	}
	if err := view.Click("保留当前内容"); err != nil {
		t.Fatal(err)
	}
	if state.notes != manual || state.pendingTemplate != "" {
		t.Fatal("keeping current notes changed the draft")
	}
	if err := view.Click("模板二"); err != nil {
		t.Fatal(err)
	}
	if err := view.Click("替换"); err != nil {
		t.Fatal(err)
	}
	if state.notes != state.templates[1].Content || draft.editor.Snapshot().Source != state.notes || state.markdown != owner || draft.editor.NativeState().Buffer() != buffer {
		t.Fatal("accepted template replacement lost the form owner or selected source")
	}
	payload, err := s.taskCreationPayload()
	if err != nil || payload["taskNotes"] != state.templates[1].Content {
		t.Fatalf("task notes no longer feed original creation payload: %+v %v", payload, err)
	}
	native.Focus()
	view.Frame()
	view.Command("undo")
	if state.notes != manual {
		t.Fatalf("template replacement lost Undo: %q want %q", state.notes, manual)
	}
	view.Command("redo")
	if state.notes != state.templates[1].Content {
		t.Fatal("template replacement lost Redo")
	}
}

func TestTaskCreationPreflightCapturesMarkdownIMEBeforePayload(t *testing.T) {
	s, view := taskCreationCompactFixture(t, 1100, false, 1)
	state := s.taskUnit.state.taskCreation
	t.Cleanup(func() { closeTaskCreationMarkdown(state) })
	const source = "\ufeff# 说明\r\n\r\n正文  \r\n"
	if !s.replaceTaskCreationMarkdownNotes(state, source) {
		t.Fatal(state.inlineError)
	}
	scrollTaskCreationToNotes(t, view)
	native := state.markdown.notes.editor.NativeState()
	buffer := native.Buffer()
	native.SetSelection(len(buffer.String()), len(buffer.String()))
	native.Focus()
	view.Frame()
	view.Compose("中文😀", 3)
	if !native.Composing() || state.notes != source {
		t.Fatal("preflight test did not start with a virtual preedit")
	}
	// Even a later configuration/validation failure must retain the just-
	// captured source. The submit boundary cannot omit composition text.
	state.configError = "fixture configuration error"
	s.beginTaskCreationPreflight()
	if native.Composing() || state.notes != source+"中文😀" || native.Buffer() != buffer {
		t.Fatalf("preflight omitted IME or replaced owner: %q", state.notes)
	}
	payload, err := s.taskCreationPayload()
	if err != nil || payload["taskNotes"] != state.notes {
		t.Fatalf("captured note did not feed task payload: %+v %v", payload, err)
	}
}

func TestTaskCreationGeneratedDocumentsUseSeparateReadonlyReader(t *testing.T) {
	s, view := taskCreationCompactFixture(t, 1100, false, 1)
	state := s.taskUnit.state.taskCreation
	t.Cleanup(func() { closeTaskCreationMarkdown(state) })
	const notes = "# 可编辑说明\n\n人工正文\n"
	if !s.replaceTaskCreationMarkdownNotes(state, notes) {
		t.Fatal(state.inlineError)
	}
	state.materialsPreviewInputSig = s.taskCreationMaterialsPreviewSignature()
	state.agentPreviewSig = s.taskCreationAgentPreviewSignature()
	state.agentPreviewErr = ""
	state.agentPreview = &workspace.AgentDocumentPreview{Files: []workspace.AgentDocumentPreviewFile{
		{RelativePath: "AGENTS.md", Content: "# 生成 AGENTS\n\n生成文档只读\n"},
		{RelativePath: "TASK-CONTEXT.md", Content: "# 生成 CONTEXT\n\n上下文只读\n"},
	}}
	state.agentPreviewFile = "AGENTS.md"
	scrollTaskCreationToNotes(t, view)
	buffer := state.markdown.notes.editor.NativeState().Buffer()
	if err := view.Click("查看生成说明"); err != nil {
		t.Fatal(err)
	}
	owner := state.markdown
	if !owner.previewOpen || owner.reader == nil || !view.HasText("生成文档只读") {
		t.Fatalf("generated preview did not use separate Reader: %v", view.Texts())
	}
	if owner.reader.Document().ID == owner.notes.editor.Snapshot().DocumentID || owner.reader.Snapshot().Source != state.agentPreview.Files[0].Content {
		t.Fatal("generated reader borrowed editable notes owner or transformed source")
	}
	if err := view.Click("生成文档只读"); err != nil {
		t.Fatal(err)
	}
	view.Type("不能写入")
	if err := view.RightClick("生成文档只读"); err != nil {
		t.Fatal(err)
	}
	if err := view.ChooseMenuItem("查看 Markdown 源码"); err != nil {
		t.Fatal(err)
	}
	view.Type("不能写入源码")
	if owner.reader.Mode() != markdownui.Source || owner.reader.Snapshot().Source != state.agentPreview.Files[0].Content || state.notes != notes || state.markdown.notes.editor.NativeState().Buffer() != buffer {
		t.Fatal("generated source view changed a generated file or the editable notes")
	}
	for _, label := range view.Texts() {
		if strings.Contains(label, "实时编辑") {
			t.Fatal("generated Reader offered an editable mode")
		}
	}
	if err := view.Click("关闭预览"); err != nil {
		t.Fatal(err)
	}
	if owner.previewOpen || owner.reader != nil || state.notes != notes {
		t.Fatal("closing generated preview lost notes or left its reader active")
	}
}
