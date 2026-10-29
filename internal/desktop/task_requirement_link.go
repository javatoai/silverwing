package desktop

import (
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func (s *app) openTaskRequirementForm(t task) {
	scope, valid := s.taskMenuScopeFor(t)
	if !valid || s.busy || s.backend == nil || t.requirementURL != "" {
		return
	}
	s.openForm("task.requirement-link", "关联需求", t.id, "", "", t.path,
		formField{key: "requirementLink", label: "需求链接", placeholder: "粘贴飞书需求完整链接"})
	s.form.taskScope = &scope
	s.form.text = t.title
}

func (s *app) submitTaskRequirementForm(form actionForm, payload map[string]any) {
	link := strings.TrimSpace(payloadString(payload, "requirementLink"))
	if domain.ParseFeishuWorkItemLink(link) == nil {
		for i := range s.form.fields {
			s.form.fields[i].error = "请填写有效的飞书需求完整链接"
		}
		return
	}
	for i := range s.form.fields {
		s.form.fields[i].error = ""
	}
	if form.taskScope == nil || !s.taskMenuScopeCurrent(*form.taskScope) {
		s.lastError = "任务或配置已变化，请重新打开关联需求。"
		for i := range s.form.fields {
			s.form.fields[i].error = s.lastError
		}
		return
	}
	s.dialogOpen = false
	s.dispatchTaskContextAction(application.Action{Kind: "task.requirement-link", TaskID: form.taskID, Path: form.path, Payload: map[string]any{"requirementLink": link}}, form.taskScope)
}
