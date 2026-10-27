package desktop

import (
	"encoding/json"
	"fmt"
	"strings"

	"silverwing-go/internal/documents"
)

func (s *app) submitForm() {
	materialsForm := isMaterialsFileManagementAction(s.form.kind) || s.form.kind == "document.rename"
	if (materialsForm && s.materialsFileInputBusy()) || (!materialsForm && s.busy) {
		s.lastError = "当前有操作正在执行，请等待完成或取消后重试。"
		return
	}
	payload := make(map[string]any, len(s.form.fields))
	for _, field := range s.form.fields {
		payload[field.key] = *s.input("form:"+field.key, field.value)
	}
	form := s.form
	if form.kind == "document.create" || form.kind == "document.mkdir" || form.kind == "document.rename" || form.kind == "document.copy" {
		name, _ := payload["name"].(string)
		validation := documents.ValidateFileName(name)
		for index := range s.form.fields {
			if s.form.fields[index].key == "name" {
				s.form.fields[index].error = ""
				if validation != nil {
					s.form.fields[index].error = validation.Error()
				}
			}
		}
		if validation != nil {
			return
		}
	}
	text := form.text
	if text == "" {
		for _, field := range form.fields {
			if field.multiline {
				text = *s.input("form:"+field.key, field.value)
				break
			}
		}
	}
	serviceID, moduleID, path := form.serviceID, form.moduleID, normalizeActionPath(form.path)
	if serviceID == "" {
		serviceID = payloadString(payload, "serviceId")
	}
	if moduleID == "" {
		moduleID = payloadString(payload, "moduleId")
	}
	if form.kind == "config.import-preview" || form.kind == "config.import" {
		path = payloadString(payload, "path")
	}

	if form.kind == "config.import-preview" {
		s.importPath = path
	}

	if s.settingsActionForm(form, payload) {
		if s.win != nil {
			s.win.Invalidate()
		}
		return
	}

	s.dialogOpen = false
	s.act(form.kind, form.taskID, serviceID, moduleID, path, text, payload)
}

func parseFormBool(payload map[string]any, key string, fallback bool) (bool, error) {
	value, exists := payload[key]
	if !exists {
		return fallback, nil
	}
	text := strings.ToLower(strings.TrimSpace(fmt.Sprint(value)))
	switch text {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s 只能填写 true 或 false", key)
	}
}

func parseJSONArrayField(payload map[string]any, key string) ([]any, error) {
	value, exists := payload[key]
	if !exists {
		return []any{}, nil
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" {
		return []any{}, nil
	}
	var items []any
	if err := json.Unmarshal([]byte(text), &items); err != nil {
		return nil, fmt.Errorf("%s 必须是有效 JSON 数组：%w", key, err)
	}
	if items == nil {
		return nil, fmt.Errorf("%s 必须是 JSON 数组", key)
	}
	return items, nil
}

func payloadString(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}
