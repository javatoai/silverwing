package desktop

import (
	"testing"

	markdownui "silverwing-go/internal/ui/markdown"
	"silverwing-go/internal/workspace"
)

func TestTaskCreationFormOmitsTaskNotesFromSubmission(t *testing.T) {
	s, view := taskCreationCompactFixture(t, 1100, false, 1)
	state := s.taskUnit.state.taskCreation
	for _, removed := range []string{"任务说明 Markdown 编辑区", "从模板填充", "还没有可用模板", "创建后可在任务详情中继续管理任务说明"} {
		if view.HasText(removed) {
			t.Fatalf("removed notes control or hint is visible: %q", removed)
		}
	}
	if _, found := view.Find("任务说明"); found {
		t.Fatal("the form still has a task notes field")
	}
	for _, label := range []string{"查看生成说明", "任务服务列表", "检查并创建任务"} {
		if _, found := view.Find(label); !found {
			t.Fatalf("creation form is missing %q", label)
		}
	}

	payload, err := s.taskCreationPayload()
	if err != nil {
		t.Fatal(err)
	}
	state.preview = s.snapshotTaskCreationPreview(payload)
	submission := taskCreationSubmissionPayload(state, nil)
	if _, exists := submission["taskNotes"]; exists {
		t.Fatal("creation submission still includes taskNotes")
	}
	selections, _ := submission["serviceSelections"].([]workspace.TaskServiceSelection)
	if submission["taskCreateToken"] != state.token || submission["folderName"] != state.draft.TaskName ||
		submission["requirementLink"] != state.draft.RequirementLink || len(selections) != 1 || selections[0].ServiceID != "service" {
		t.Fatalf("creation submission omitted existing form values: %+v", submission)
	}
}

func TestTaskCreationGeneratedDocumentsUseReadonlyReader(t *testing.T) {
	s, view := taskCreationCompactFixture(t, 1100, false, 1)
	state := s.taskUnit.state.taskCreation
	t.Cleanup(func() { closeTaskCreationGeneratedPreview(state) })
	state.materialsPreviewInputSig = s.taskCreationMaterialsPreviewSignature()
	state.agentPreviewSig = s.taskCreationAgentPreviewSignature()
	state.agentPreview = &workspace.AgentDocumentPreview{Files: []workspace.AgentDocumentPreviewFile{
		{RelativePath: workspace.AgentRouterFileName, Content: "# 生成 AGENTS\n\n生成文档只读\n"},
		{RelativePath: ".workspace/agent/TASK-CONTEXT.md", Content: "旧上下文"},
	}}
	if err := view.Click("查看生成说明"); err != nil {
		t.Fatal(err)
	}
	preview := state.generatedPreview
	if preview == nil || !preview.open || preview.reader == nil || !view.HasText("生成文档只读") {
		t.Fatalf("generated preview did not open the reader: %v", view.Texts())
	}
	if view.HasText("TASK-CONTEXT.md") || view.HasText("旧上下文") {
		t.Fatal("generated preview offers a separate task context")
	}
	source := state.agentPreview.Files[0].Content
	if preview.reader.Snapshot().Source != source {
		t.Fatal("generated preview lost the original Markdown")
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
	if preview.reader.Mode() != markdownui.Source || preview.reader.Snapshot().Source != source || state.agentPreview.Files[0].Content != source {
		t.Fatal("generated Markdown source was unavailable or editable")
	}
	if view.HasText("实时编辑") {
		t.Fatal("generated reader offered an editable mode")
	}
	readerContext := preview.reader.Context()
	if err := view.Click("关闭预览"); err != nil {
		t.Fatal(err)
	}
	if state.generatedPreview != nil || readerContext.Err() == nil {
		t.Fatal("closing generated preview left its reader active")
	}
	if !s.dialogOpen || s.taskUnit.state.taskCreation != state {
		t.Fatal("closing generated preview dismissed the creation form")
	}
}
