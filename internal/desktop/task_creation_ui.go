package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
	"strings"
	"sync/atomic"
)

type taskCreationState struct {
	token, title                                                                                string
	config                                                                                      domain.AppConfig
	draft                                                                                       domain.RequirementDraftState
	notes, groupID, groupQuery, phase, inlineError, aiError, lastName, lastLink, taskFieldFocus string
	configError                                                                                 string
	requirements                                                                                *taskCreationRequirementsState
	aiNaming                                                                                    *taskCreationAINamingState
	initialized, aiEligible, aiPending, pendingPreflight                                        bool
	templates                                                                                   []domain.AgentTaskTemplate
	templatesLoading                                                                            bool
	templatesError, selectedTemplate, pendingTemplate                                           string
	openCodex                                                                                   bool
	progressGeneration                                                                          uint64
	progressMessage                                                                             string
}

var taskCreationSequence atomic.Uint64

func (s *app) initTaskCreation(title, requirementTitle, requirementURL string) {
	s.closeTaskCreationRequirements()
	s.closeTaskCreationAINaming()
	config := domain.DefaultAppConfig()
	err := json.Unmarshal([]byte(s.snapshot.ConfigJSON), &config)
	state := &taskCreationState{token: fmt.Sprint(taskCreationSequence.Add(1)), title: title, config: config, phase: "editing", aiEligible: requirementTitle != "", openCodex: containsString(config.DefaultWorkspaceToolIDs, "codex")}
	var selected *string
	if requirementTitle != "" {
		selected = &requirementTitle
	}
	state.draft = domain.RequirementDraftState{TaskName: requirementTitle}.ChangeRequirement(requirementURL, "", selected)
	state.lastName, state.lastLink = state.draft.TaskName, state.draft.RequirementLink
	if len(config.Groups) > 0 {
		state.groupID, state.groupQuery = config.Groups[0].ID, config.Groups[0].Name
	}
	if err != nil {
		state.configError = "无法读取当前配置：" + err.Error()
	}
	if s.settingsTemplatesLoaded {
		state.templates = taskTemplatesFromMaps(s.settingsTemplates)
	}
	s.taskCreation = state
}
func taskTemplatesFromMaps(items []map[string]any) []domain.AgentTaskTemplate {
	result := []domain.AgentTaskTemplate{}
	for _, item := range items {
		id, name := mapString(item, "id"), mapString(item, "name")
		if id != "" && name != "" {
			result = append(result, domain.AgentTaskTemplate{ID: id, Name: name, Content: mapString(item, "content")})
		}
	}
	return result
}
func (s *app) loadTaskCreationTemplates() {
	if state := s.taskCreation; state != nil && !state.templatesLoading {
		state.templatesLoading = true
		s.act("instructions.templates.list", "", "", "", "", "", map[string]any{"taskCreateToken": state.token})
	}
}
func (s *app) syncTaskCreationDraft() {
	state := s.taskCreation
	if state == nil {
		return
	}
	if state.draft.TaskName != state.lastName {
		state.draft = state.draft.EditName(state.draft.TaskName)
		state.lastName = state.draft.TaskName
		state.inlineError = ""
	}
	if state.draft.RequirementLink != state.lastLink {
		link := state.draft.RequirementLink
		state.draft = state.draft.ChangeRequirement(link, "", nil)
		state.lastLink = link
		s.cancelTaskCreationAINaming()
		state.aiEligible = false
		state.inlineError = ""
	}
}
func (s *app) invalidateTaskCreationPreview(_ string) {
	if s.taskCreation != nil {
		s.taskCreation.inlineError = ""
	}
}
func (s *app) requestTaskCreationAINaming(force bool) { s.requestTaskCreationAINamingJob(force) }
func (s *app) taskCreationValidationFailure() (string, *taskCreationValidationTarget) {
	state := s.taskCreation
	if state == nil {
		return "任务表单已关闭", nil
	}
	if state.configError != "" {
		return state.configError, nil
	}
	if state.groupID == "" {
		return "请选择项目组", nil
	}
	if _, err := workspace.RequireValidTaskDirectoryName(state.draft.TaskName); err != nil {
		return err.Error(), nil
	}
	if state.requirements != nil && state.requirements.inputIsQuery {
		return "请选择需求或填写完整需求链接", nil
	}
	return "", nil
}

type taskCreationValidationTarget struct{ field string }

func (s *app) taskCreationPayload() (map[string]any, error) {
	if message, _ := s.taskCreationValidationFailure(); message != "" {
		return nil, fmt.Errorf("%s", message)
	}
	state := s.taskCreation
	ids := []string{}
	if taskCreationCodexEnabled(state) && state.openCodex {
		ids = append(ids, "codex")
	}
	return map[string]any{"folderName": state.draft.TaskName, "groupId": state.groupID, "requirementLink": state.draft.RequirementLink, "taskNotes": state.notes, "workspaceToolIds": ids, "taskCreateToken": state.token}, nil
}

func taskCreationCodexEnabled(state *taskCreationState) bool {
	return state != nil && containsString(state.config.DefaultWorkspaceToolIDs, "codex")
}

func (s *app) beginTaskCreationPreview() bool {
	payload, err := s.taskCreationPayload()
	if err != nil {
		s.taskCreation.inlineError = err.Error()
		return false
	}
	if s.busy {
		return false
	}
	s.cancelTaskCreationAINaming()
	s.taskCreation.phase = "creating"
	s.taskCreation.progressMessage = "正在创建任务…"
	s.act("task.create", "", "", "", "", "", payload)
	return true
}
func taskCreationActionPayload(payload map[string]any, token string) map[string]any {
	copy := make(map[string]any, len(payload)+1)
	for k, v := range payload {
		copy[k] = v
	}
	copy["taskCreateToken"] = token
	return copy
}
func taskCreationOwnedAction(kind string) bool {
	return kind == "task.create" || kind == "requirements.ai-name" || kind == "instructions.templates.list"
}
func decodeTaskCreationResult[T any](data any) (value T, err error) {
	raw, err := json.Marshal(data)
	if err == nil {
		err = json.Unmarshal(raw, &value)
	}
	return
}
func (s *app) taskCreationActionResult(action application.Action, result application.ActionResult) bool {
	state := s.taskCreation
	if state == nil || payloadString(action.Payload, "taskCreateToken") != state.token {
		return payloadString(action.Payload, "taskCreateToken") != "" && taskCreationOwnedAction(action.Kind)
	}
	switch action.Kind {
	case "requirements.ai-name":
		state.aiPending = false
		if payloadString(action.Payload, "requestLink") != state.draft.RequirementLink || !state.aiEligible {
			return true
		}
		suggestion, err := decodeTaskCreationResult[domain.RequirementAiNamingSuggestion](result.Data)
		if err != nil {
			state.aiError = err.Error()
			return true
		}
		updated, err := state.draft.ApplyAINaming(state.draft.RequirementLink, suggestion, "")
		if err != nil {
			state.aiError = err.Error()
			return true
		}
		state.draft = updated
		state.lastName = updated.TaskName
		state.aiError = ""
		return true
	case "instructions.templates.list":
		state.templatesLoading = false
		state.templates, _ = decodeData[domain.AgentTaskTemplate](result.Data)
		return true
	case "task.create":
		s.closeTaskCreationRequirements()
		s.closeTaskCreationAINaming()
		s.taskCreation = nil
		s.dialogOpen = false
		s.form = actionForm{}
		s.lastMessage = result.Message
		s.taskCreationWarning = result.Text
		return true
	}
	return false
}
func (s *app) taskCreationActionError(action application.Action, cause error) bool {
	state := s.taskCreation
	if state == nil || payloadString(action.Payload, "taskCreateToken") != state.token {
		return payloadString(action.Payload, "taskCreateToken") != "" && taskCreationOwnedAction(action.Kind)
	}
	switch action.Kind {
	case "requirements.ai-name":
		state.aiPending = false
		state.aiError = "AI 命名失败，可手工填写：" + cause.Error()
		return true
	case "task.create":
		state.phase = "editing"
		state.inlineError = cause.Error()
		return true
	case "instructions.templates.list":
		state.templatesLoading = false
		state.templatesError = cause.Error()
		return true
	}
	return false
}
func (s *app) requestTaskCreationDismiss() {
	state := s.taskCreation
	if state == nil {
		return
	}
	if state.phase == "creating" {
		return
	}
	if strings.TrimSpace(state.notes) != "" {
		s.confirmNative("关闭创建任务", "未保存的任务说明将丢失。", state.draft.TaskName, func() {
			if s.taskCreation == state {
				s.discardTaskCreation(state)
			}
		})
		return
	}
	s.discardTaskCreation(state)
}
func (s *app) discardTaskCreation(state *taskCreationState) {
	if s.taskCreation != state {
		return
	}
	s.closeTaskCreationRequirements()
	s.closeTaskCreationAINaming()
	s.taskCreation = nil
	s.dialogOpen = false
	s.form = actionForm{}
}
func (s *app) renderTaskCreationDialog(c *ui.Context, p palette) bool {
	state := s.taskCreation
	if state == nil || !s.dialogOpen || s.form.mode != "task-create" {
		return false
	}
	s.syncTaskCreationDraft()
	s.ensureTaskCreationAINamingScope()
	if !state.initialized && s.backend != nil && s.win != nil && !s.win.IsDestroyed() {
		state.initialized = true
		if !s.settingsTemplatesLoaded {
			s.loadTaskCreationTemplates()
		}
		s.requestTaskCreationAINaming(false)
	}
	open := s.dialogOpen
	ui.DialogBase(c.Key("task-create-dialog"), &open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.32))
		w, h := c.Size()
		panel.Width(min(760, w-32)).MaxHeight(h-32).Padding(20).Gap(12).Background(p.surface).Border(1, p.line).Radius(16).Label("创建测试任务").Children(func() {
			label(c, "创建测试任务", 20, p.text).FontWeight(600)
			ui.Scroll(c).FillWidth().MaxHeight(max(180, h-220)).Gap(12).Disabled(state.phase == "creating").Children(func() {
				s.renderTaskCreationRequirements(c, p)
				label(c, "任务名称 / 文件夹名", 12, p.muted)
				ui.Row(c).FillWidth().Gap(8).Children(func() {
					ui.TextInput(c.Key("task-create-name"), &state.draft.TaskName).Grow(1).Height(36).Label("任务名称").OnChange(s.syncTaskCreationDraft)
					if state.aiEligible && state.config.AIRequirementNamingEnabled {
						if iconButtonDisabled(c, "task-create-ai-name", "refresh", "生成任务名称", p, state.aiPending || s.busy).Clicked() {
							s.requestTaskCreationAINaming(true)
						}
					}
				})
				if state.aiPending {
					label(c, "正在获取命名建议…", 11, p.muted)
				}
				if state.aiError != "" {
					label(c, state.aiError, 11, p.amber).Wrap()
				}
				label(c, "项目组", 12, p.muted)
				ui.Row(c).FillWidth().Gap(6).Wrap().Children(func() {
					for _, group := range state.config.Groups {
						g := group
						if action(c, "qa-task-group-"+g.ID, "tasks", g.Name, p, state.groupID == g.ID) {
							state.groupID = g.ID
							state.groupQuery = g.Name
						}
					}
				})
				ui.Row(c).FillWidth().Gap(8).Children(func() {
					label(c, "需求说明", 12, p.muted).Grow(1)
					if action(c, "task-create-load-templates", "refresh", "读取模板", p, false) && !s.busy {
						s.loadTaskCreationTemplates()
					}
				})
				if state.templatesError != "" {
					label(c, state.templatesError, 11, p.amber).Wrap()
				}
				ui.Row(c).FillWidth().Gap(6).Wrap().Children(func() {
					for _, template := range state.templates {
						t := template
						if action(c, "task-create-template-"+t.ID, "document", t.Name, p, state.selectedTemplate == t.ID) {
							s.toggleTaskCreationTemplate(t)
						}
					}
				})
				ui.TextArea(c.Key("task-create-notes"), &state.notes).FillWidth().Height(160).Padding(10).Border(1, p.line).Radius(8).Label("需求说明").Placeholder("测试目标、范围、验收标准及需要保留的上下文")
				if taskCreationCodexEnabled(state) {
					ui.Row(c).FillWidth().Gap(10).Children(func() {
						label(c, "创建后在 Codex 中打开", 12, p.text).Grow(1)
						ui.Switch(c.Key("qa-task-open-codex"), &state.openCodex).Width(52).Height(32).Label("创建后在 Codex 中打开")
					})
				}
			})
			if state.inlineError != "" {
				label(c, state.inlineError, 12, p.danger).Wrap()
			}
			if state.phase == "creating" {
				label(c, state.progressMessage, 12, p.muted).Wrap()
			}
			ui.Row(c).FillWidth().Gap(8).Justify(ui.End).Children(func() {
				if action(c, "task-create-cancel", "close", "取消", p, false) && state.phase != "creating" {
					s.requestTaskCreationDismiss()
				}
				if action(c, "task-create-submit", "add", "创建任务", p, true) && state.phase != "creating" {
					s.beginTaskCreationPreview()
				}
			})
		})
	})
	if !open {
		s.requestTaskCreationDismiss()
	}
	return true
}
func taskCreationSelectedTemplateID(notes string, templates []domain.AgentTaskTemplate) string {
	selected := ""
	for _, template := range templates {
		if template.Content != notes {
			continue
		}
		if selected != "" {
			return ""
		}
		selected = template.ID
	}
	return selected
}

func (s *app) toggleTaskCreationTemplate(template domain.AgentTaskTemplate) {
	state := s.taskCreation
	if state == nil || state.phase != "editing" {
		return
	}
	selectedID := state.selectedTemplate
	if selectedID == "" {
		selectedID = taskCreationSelectedTemplateID(state.notes, state.templates)
	}
	if selectedID == template.ID {
		if state.notes == template.Content {
			state.notes = ""
		}
		state.selectedTemplate, state.pendingTemplate = "", ""
		return
	}
	apply := func() {
		state.notes = template.Content
		state.selectedTemplate, state.pendingTemplate = template.ID, ""
	}
	if strings.TrimSpace(state.notes) == "" {
		apply()
		return
	}
	for _, selected := range state.templates {
		if selected.ID == selectedID && selected.Content == state.notes {
			apply()
			return
		}
	}
	notes, selection := state.notes, state.selectedTemplate
	s.confirmNative("使用说明模板", "当前说明内容将被替换。", template.Name, func() {
		if s.taskCreation == state && state.phase == "editing" && state.notes == notes && state.selectedTemplate == selection {
			apply()
		}
	})
}

func (s *app) taskCreationProgressUpdates(ctx context.Context, action application.Action) func(workspace.TaskCreationProgress) {
	state := s.taskCreation
	if state == nil || payloadString(action.Payload, "taskCreateToken") != state.token {
		return nil
	}
	return func(event workspace.TaskCreationProgress) {
		if s.win == nil || s.win.IsDestroyed() || ctx.Err() != nil {
			return
		}
		s.win.Update(func() {
			if s.taskCreation == state {
				state.progressMessage = event.Message
			}
		})
	}
}

type taskCreationWorkspaceToolOption struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	Available   bool   `json:"available"`
	Reason      string `json:"reason"`
}
