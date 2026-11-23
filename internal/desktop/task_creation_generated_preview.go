package desktop

import (
	"slices"

	markdownui "silverwing-go/internal/ui/markdown"
	"silverwing-go/internal/workspace"

	"github.com/egoist/mygo/ui"
)

type taskCreationGeneratedPreviewState struct {
	open   bool
	reader *markdownui.ReaderState
	error  string
}

func closeTaskCreationGeneratedPreview(state *taskCreationState) {
	if state == nil || state.generatedPreview == nil {
		return
	}
	if state.generatedPreview.reader != nil {
		state.generatedPreview.reader.Close()
	}
	state.generatedPreview = nil
}

func (s *app) openTaskCreationGeneratedPreview(state *taskCreationState) {
	state.generatedPreview = &taskCreationGeneratedPreviewState{open: true}
	s.refreshTaskCreationMaterialsPreview()
	if !state.materialsPreviewPending {
		s.refreshTaskCreationAgentPreview()
	}
}

func (s *app) renderTaskCreationGeneratedPreview(c *ui.Context, p palette, state *taskCreationState) {
	owner := state.generatedPreview
	if owner == nil {
		return
	}
	ui.DialogBase(c.Key("task-create-generated-markdown"), &owner.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.32))
		width, height := c.Size()
		panel.Width(min(820, width-48)).Height(min(760, height-48)).Padding(18).Gap(12).Background(p.surface).Border(1, p.line).Radius(18).Label("生成说明只读预览")
		panel.Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				label(c, workspace.AgentRouterFileName, 18, p.text).FontWeight(600).Grow(1)
				if action(c, "task-create-preview-close", "close", "关闭预览", p, false) {
					owner.open = false
				}
			})
			if state.materialsPreviewPending || state.agentPreviewBusy {
				label(c, "正在生成任务说明预览…", 12, p.muted)
				return
			}
			if state.materialsPreviewErr != "" {
				label(c, state.materialsPreviewErr, 11, p.amber).FillWidth().Wrap()
			}
			if state.agentPreviewErr != "" {
				label(c, "预览失败："+state.agentPreviewErr, 12, p.amber).FillWidth().Wrap()
				if action(c, "task-create-preview-retry", "refresh", "重试生成预览", p, false) {
					state.agentPreviewSig = ""
					s.refreshTaskCreationAgentPreview()
				}
				return
			}
			if state.agentPreview == nil {
				label(c, "尚未生成任务说明预览。", 12, p.muted)
				return
			}
			index := slices.IndexFunc(state.agentPreview.Files, func(file workspace.AgentDocumentPreviewFile) bool {
				return file.RelativePath == workspace.AgentRouterFileName
			})
			if index < 0 {
				label(c, "尚未生成任务说明预览。", 12, p.muted)
				return
			}
			file := state.agentPreview.Files[index]
			document := markdownui.Document{ID: "task-create:" + state.token + ":generated:" + file.RelativePath, Source: file.Content}
			if owner.reader == nil {
				owner.reader = markdownui.NewReaderState(document)
			} else {
				owner.reader.SetDocument(document)
			}
			if owner.error != "" {
				label(c, owner.error, 11, p.amber).FillWidth().Wrap()
			}
			markdownui.MarkdownReader(c, owner.reader, s.transientMarkdownReaderOptions(p, func(err error) {
				if err != nil && s.taskUnit.state.taskCreation == state && state.generatedPreview == owner {
					owner.error = err.Error()
				}
			})).Grow(1).FillWidth().MinHeight(0)
		})
	})
	if !owner.open {
		closeTaskCreationGeneratedPreview(state)
	}
}
