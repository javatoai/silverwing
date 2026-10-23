package application

import (
	"encoding/json"
	"errors"
	"fmt"

	"silverwing-go/internal/domain"
)

// ConfigFieldsUpdate describes the fields owned by one ordinary settings edit.
// Arrays replace their field; object leaves are merged with the current object.
// An explicit JSON null clears a nullable field, while an absent key is untouched.
type ConfigFieldsUpdate struct {
	Fields       map[string]json.RawMessage            `json:"fields,omitempty"`
	ObjectFields map[string]map[string]json.RawMessage `json:"objectFields,omitempty"`
}

func (a *App) saveConfigurationFields(action Action) (ActionResult, error) {
	update, err := decodeConfigurationFieldsUpdate(action)
	if err != nil {
		return ActionResult{}, err
	}
	_, err = a.Config.Update(func(current domain.AppConfig) (domain.AppConfig, error) {
		next, err := mergeConfigurationFields(current, update)
		if err != nil {
			return current, err
		}
		if err := a.validateConfigurationRemovals(current, next); err != nil {
			return current, err
		}
		return next, nil
	})
	return ActionResult{Message: "设置已保存"}, err
}

func decodeConfigurationFieldsUpdate(action Action) (ConfigFieldsUpdate, error) {
	var update ConfigFieldsUpdate
	if action.Payload["update"] == nil {
		return update, errors.New("缺少设置字段更新")
	}
	encoded, err := json.Marshal(action.Payload["update"])
	if err != nil {
		return update, err
	}
	if err := domain.DecodeStrict(encoded, &update); err != nil {
		return update, fmt.Errorf("设置字段更新不合法：%w", err)
	}
	if len(update.Fields)+len(update.ObjectFields) == 0 {
		return update, errors.New("设置字段更新不能为空")
	}
	return update, nil
}

func mergeConfigurationFields(current domain.AppConfig, update ConfigFieldsUpdate) (domain.AppConfig, error) {
	encoded, err := json.Marshal(current)
	if err != nil {
		return current, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return current, err
	}
	for key, raw := range update.Fields {
		if _, exists := fields[key]; !exists {
			return current, fmt.Errorf("未知设置字段：%s", key)
		}
		switch key {
		case "schemaVersion", "genbuExecutableAutoDetected":
			return current, fmt.Errorf("此设置字段不能直接更新：%s", key)
		case "toolVisibility", "sidebarVisibility":
			return current, errors.New("显示设置需要按具体开关更新")
		}
		if _, overlap := update.ObjectFields[key]; overlap {
			return current, fmt.Errorf("设置字段不能同时整体与局部更新：%s", key)
		}
		fields[key] = raw
	}
	for objectKey, changes := range update.ObjectFields {
		if objectKey != "toolVisibility" && objectKey != "sidebarVisibility" {
			return current, fmt.Errorf("不支持局部更新的设置对象：%s", objectKey)
		}
		if len(changes) == 0 {
			return current, fmt.Errorf("设置对象更新不能为空：%s", objectKey)
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(fields[objectKey], &object); err != nil {
			return current, err
		}
		for key, raw := range changes {
			if _, exists := object[key]; !exists {
				return current, fmt.Errorf("未知设置字段：%s.%s", objectKey, key)
			}
			object[key] = raw
		}
		fields[objectKey], err = json.Marshal(object)
		if err != nil {
			return current, err
		}
	}
	encoded, err = json.Marshal(fields)
	if err != nil {
		return current, err
	}
	var next domain.AppConfig
	if err := domain.DecodeStrict(encoded, &next); err != nil {
		return current, err
	}
	if !sameConfigurationString(current.TaskRoot, next.TaskRoot) {
		return current, errors.New("任务根目录变更需要先选择迁移或直接切换")
	}
	if _, changed := update.Fields["genbuExecutablePath"]; changed {
		next.GenbuExecutableAutoDetected = current.GenbuExecutableAutoDetected && sameConfigurationString(current.GenbuExecutablePath, next.GenbuExecutablePath)
	}
	if _, changed := update.Fields["meegleProjects"]; changed {
		if _, explicitDefault := update.Fields["meegleDefaultSprintProjectKey"]; !explicitDefault {
			next.MeegleDefaultSprintProjectKey = current.MeegleDefaultSprintProjectKey
			if !configurationMeegleProjectExists(next, next.MeegleDefaultSprintProjectKey) {
				next.MeegleDefaultSprintProjectKey = nil
			}
		}
	}
	if _, changed := update.Fields["meegleDefaultSprintProjectKey"]; changed && !configurationMeegleProjectExists(next, next.MeegleDefaultSprintProjectKey) {
		return current, errors.New("默认 Sprint 团队空间必须是已配置的 Meegle 项目")
	}
	if err := next.Validate(); err != nil {
		return current, err
	}
	return next, nil
}

func sameConfigurationString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func configurationMeegleProjectExists(config domain.AppConfig, key *string) bool {
	if key == nil {
		return true
	}
	for _, project := range config.MeegleProjects {
		if project.ProjectKey == *key {
			return true
		}
	}
	return false
}
