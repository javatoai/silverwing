package desktop

import (
	"fmt"

	markdownui "silverwing-go/internal/ui/markdown"

	"github.com/egoist/mygo/ui"
)

type taskCreationMarkdownState struct {
	notes       *transientMarkdownDraft
	previewOpen bool
	reader      *markdownui.ReaderState
	error       string
}

func (s *app) ensureTaskCreationMarkdown(state *taskCreationState) *taskCreationMarkdownState {
	if state.markdown == nil {
		state.markdown = &taskCreationMarkdownState{notes: newTransientMarkdownDraft("task-create:"+state.token+":notes", &state.notes)}
	}
	return state.markdown
}

func (s *app) captureTaskCreationMarkdown(state *taskCreationState) error {
	if state == nil || state.markdown == nil {
		return nil
	}
	if s.taskUnit.state.taskCreation != state {
		return fmt.Errorf("任务创建表单已变化，请重新打开")
	}
	return state.markdown.notes.capture()
}

func closeTaskCreationMarkdown(state *taskCreationState) {
	if state == nil || state.markdown == nil {
		return
	}
	state.markdown.notes.close()
	if state.markdown.reader != nil {
		state.markdown.reader.Close()
	}
	state.markdown = nil
}

func (s *app) replaceTaskCreationMarkdownNotes(state *taskCreationState, source string) bool {
	if state.markdown == nil {
		state.notes = source
	} else if err := state.markdown.notes.replace(source); err != nil {
		state.inlineError = err.Error()
		return false
	}
	s.invalidateTaskCreationPreview("任务说明已变化，请重新预检。")
	return true
}

func (s *app) renderTaskCreationMarkdownNotes(c *ui.Context, p palette) {
	state := s.taskUnit.state.taskCreation
	if state == nil {
		return
	}
	owner := s.ensureTaskCreationMarkdown(state)
	ui.Column(c.Key("task-create-markdown-notes")).FillWidth().MinWidth(0).Gap(6).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			label(c, "任务说明", 12, p.muted).Grow(1)
			if action(c, "task-create-generated-preview", "", "查看生成说明", p, false) {
				if err := s.captureTaskCreationMarkdown(state); err != nil {
					state.inlineError = err.Error()
				} else {
					owner.previewOpen = true
					owner.error = ""
					s.refreshTaskCreationMaterialsPreview()
					if !state.materialsPreviewPending {
						s.refreshTaskCreationAgentPreview()
					}
				}
			}
		})
		label(c, "从模板填充（单选）", 11, p.muted)
		if state.templatesLoading {
			label(c, "正在读取任务说明模板…", 11, p.muted)
		} else if state.templatesError != "" {
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				label(c, "模板读取失败："+state.templatesError, 11, p.amber).Grow(1).Wrap()
				if action(c, "task-create-templates-retry", "refresh", "重试", p, false) {
					state.templatesError = ""
					s.loadTaskCreationTemplates()
				}
			})
		} else if len(state.templates) == 0 {
			label(c, "还没有可用模板，可在设置中创建任务说明模板。", 11, p.muted).FillWidth().Wrap()
		} else {
			ui.Row(c).FillWidth().MinWidth(0).Gap(6).Wrap().Children(func() {
				for _, template := range state.templates {
					button := ui.ButtonBase(c.Key("task-create-template-"+template.ID)).Height(30).Padding(0, 10).Radius(16).Label(template.Name).Tooltip(template.Name)
					if state.selectedTemplate == template.ID {
						button.Background(p.selected)
					}
					button.Children(func() { label(c, template.Name, 11, p.text).SingleLine() })
					if button.Clicked() {
						s.toggleTaskCreationTemplate(template)
					}
				}
			})
		}
		if state.pendingTemplate != "" {
			name := state.pendingTemplate
			if template := s.taskCreationTemplate(name); template != nil {
				name = template.Name
			}
			label(c, "替换当前手工编辑的说明为“"+name+"”？", 11, p.amber).FillWidth().Wrap()
			ui.Row(c).Gap(8).Wrap().Children(func() {
				if action(c, "task-create-template-confirm", "check", "替换", p, true) {
					s.confirmTaskCreationTemplateReplacement(true)
				}
				if action(c, "task-create-template-cancel", "close", "保留当前内容", p, false) {
					s.confirmTaskCreationTemplateReplacement(false)
				}
			})
		}
		current := func() bool { return s.dialogOpen && s.taskUnit.state.taskCreation == state && state.markdown == owner }
		s.renderTransientMarkdown(c, p, owner.notes, current,
			func() { s.invalidateTaskCreationPreview("任务说明已变化，请重新预检。") },
			func() {
				if !s.busy && state.phase != "preview" && state.phase != "creating" {
					s.beginTaskCreationPreflight()
				}
			}, "任务说明 Markdown 编辑区", 240)
		label(c, "创建后可在任务详情中继续管理任务说明。", 10, p.muted).FillWidth().Wrap()
	})
}

func (s *app) renderTaskCreationGeneratedMarkdown(c *ui.Context, p palette, state *taskCreationState) {
	owner := state.markdown
	if owner == nil || !owner.previewOpen {
		return
	}
	ui.DialogBase(c.Key("task-create-generated-markdown"), &owner.previewOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.32))
		width, height := c.Size()
		panel.Width(min(820, width-48)).Height(min(760, height-48)).Padding(18).Gap(12).Background(p.surface).Border(1, p.line).Radius(18).Label("生成说明只读预览")
		panel.Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				label(c, "生成说明", 18, p.text).FontWeight(600).Grow(1)
				if action(c, "task-create-preview-close", "close", "关闭预览", p, false) {
					owner.previewOpen = false
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
			if state.agentPreview == nil || len(state.agentPreview.Files) == 0 {
				label(c, "尚未生成任务说明预览。", 12, p.muted)
				return
			}
			selected := 0
			for index, file := range state.agentPreview.Files {
				if file.RelativePath == state.agentPreviewFile {
					selected = index
					break
				}
			}
			ui.Row(c).FillWidth().Gap(6).Wrap().Children(func() {
				for index, file := range state.agentPreview.Files {
					file := file
					s.settingsChoice(c, p, fmt.Sprintf("task-create-preview-file-%d", index), file.RelativePath, index == selected, func() { state.agentPreviewFile = file.RelativePath })
				}
			})
			file := state.agentPreview.Files[selected]
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
				if err != nil && s.taskUnit.state.taskCreation == state && state.markdown == owner {
					owner.error = err.Error()
				}
			})).Grow(1).FillWidth().MinHeight(0)
		})
	})
	if !owner.previewOpen && owner.reader != nil {
		owner.reader.Close()
		owner.reader = nil
	}
}
