package desktop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"silverwing-go/internal/application"
)

func (s *app) settingsConfigMap() (map[string]any, error) {
	return decodeSettingsConfig(s.settingsUnit.state.configJSON)
}

func decodeSettingsConfig(source string) (map[string]any, error) {
	config := make(map[string]any)
	if strings.TrimSpace(source) == "" {
		return nil, fmt.Errorf("当前配置尚未加载")
	}
	if err := json.Unmarshal([]byte(source), &config); err != nil {
		return nil, fmt.Errorf("读取当前配置失败：%w", err)
	}
	if config == nil {
		return nil, fmt.Errorf("读取当前配置失败：需要 JSON 对象")
	}
	return config, nil
}

func (s *app) saveConfigField(key string, value any) {
	config, err := s.settingsConfigMap()
	if err != nil {
		s.lastError = err.Error()
		return
	}
	config[key] = value
	s.saveSettingsConfigMap(config, key)
}

func (s *app) saveSettingsConfigMap(config map[string]any, ownedFields ...string) {
	if s.busy {
		s.lastError = "当前有操作正在执行，请等待完成或取消后重试。"
		return
	}
	baseline, err := s.settingsConfigMap()
	if err != nil {
		s.lastError = err.Error()
		return
	}
	update, err := settingsConfigFieldsUpdate(baseline, config, ownedFields)
	if err != nil {
		s.lastError = "无法编码设置：" + err.Error()
		return
	}
	if len(update.Fields)+len(update.ObjectFields) == 0 {
		return
	}
	s.saveSettingsConfigFields(update)
}

func (s *app) saveSettingsConfigFields(update application.ConfigFieldsUpdate) {
	if s.busy {
		s.lastError = "当前有操作正在执行，请等待完成或取消后重试。"
		return
	}
	s.act("config.fields.save", "", "", "", "", "", map[string]any{"update": update})
}

func settingsConfigFieldsUpdate(baseline, config map[string]any, ownedFields []string) (application.ConfigFieldsUpdate, error) {
	update := application.ConfigFieldsUpdate{Fields: map[string]json.RawMessage{}, ObjectFields: map[string]map[string]json.RawMessage{}}
	owned := make(map[string]bool, len(ownedFields))
	for _, key := range ownedFields {
		if _, exists := config[key]; !exists {
			return update, fmt.Errorf("设置字段不存在：%s", key)
		}
		owned[key] = true
	}
	for key, value := range config {
		if len(owned) != 0 && !owned[key] {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return update, err
		}
		if len(owned) == 0 {
			previous, err := json.Marshal(baseline[key])
			if err != nil {
				return update, err
			}
			if bytes.Equal(previous, encoded) {
				continue
			}
		}
		if object, ok := value.(map[string]any); ok {
			previous, _ := baseline[key].(map[string]any)
			changes := map[string]json.RawMessage{}
			for field, value := range object {
				raw, err := json.Marshal(value)
				if err != nil {
					return update, err
				}
				old, err := json.Marshal(previous[field])
				if err != nil {
					return update, err
				}
				if len(owned) != 0 || !bytes.Equal(old, raw) {
					changes[field] = raw
				}
			}
			if len(changes) != 0 {
				update.ObjectFields[key] = changes
			}
		} else {
			update.Fields[key] = encoded
		}
	}
	return update, nil
}

func (s *app) updateConfigObjectField(objectKey, fieldKey string, value any) {
	config, err := s.settingsConfigMap()
	if err != nil {
		s.lastError = err.Error()
		return
	}
	if _, exists := config[objectKey]; !exists {
		s.lastError = "设置对象不存在：" + objectKey
		return
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		s.lastError = "无法编码设置：" + err.Error()
		return
	}
	s.saveSettingsConfigFields(application.ConfigFieldsUpdate{ObjectFields: map[string]map[string]json.RawMessage{objectKey: {fieldKey: encoded}}})
}

func (s *app) updateConfigGroup(groupID string, update func(map[string]any)) error {
	config, err := s.settingsConfigMap()
	if err != nil {
		return err
	}
	groups, _ := config["groups"].([]any)
	found := false
	for _, item := range groups {
		group, ok := item.(map[string]any)
		if ok && mapString(group, "id") == groupID {
			update(group)
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("项目组 %q 不在当前配置中", groupID)
	}
	config["groups"] = groups
	s.saveSettingsConfigMap(config)
	return nil
}

func (s *app) removeConfigGroup(groupID string) {
	config, err := s.settingsConfigMap()
	if err != nil {
		s.lastError = err.Error()
		return
	}
	groups, _ := config["groups"].([]any)
	if len(groups) <= 1 {
		s.lastError = "至少保留一个项目组。"
		return
	}
	filtered := make([]any, 0, len(groups))
	found := false
	for _, item := range groups {
		group, _ := item.(map[string]any)
		if mapString(group, "id") == groupID {
			found = true
			services, _ := group["services"].([]any)
			if len(services) > 0 {
				s.lastError = "此项目组仍包含服务，不能删除。"
				return
			}
			continue
		}
		filtered = append(filtered, item)
	}
	if !found {
		s.lastError = "项目组不在当前配置中。"
		return
	}
	config["groups"] = filtered
	s.saveSettingsConfigMap(config)
}

func (s *app) configString(key string) string {
	config, err := s.readSettingsConfig()
	if err != nil {
		return ""
	}
	return mapString(config, key)
}

func (s *app) configBool(key string, fallback bool) bool {
	config, err := s.readSettingsConfig()
	if err != nil {
		return fallback
	}
	value, ok := config[key].(bool)
	if !ok {
		return fallback
	}
	return value
}

func (s *app) configInt(key string, fallback int) int {
	config, err := s.readSettingsConfig()
	if err != nil {
		return fallback
	}
	switch value := config[key].(type) {
	case float64:
		return int(value)
	case int:
		return value
	default:
		return fallback
	}
}

func (s *app) configArray(key string) []any {
	return cloneConfigArray(s.readConfigArray(key))
}

func (s *app) configNestedBool(objectKey, fieldKey string, fallback bool) bool {
	config, err := s.readSettingsConfig()
	if err != nil {
		return fallback
	}
	object, _ := config[objectKey].(map[string]any)
	value, ok := object[fieldKey].(bool)
	if !ok {
		return fallback
	}
	return value
}

func mapValue(value any, key string) map[string]any {
	return mapData(dataValue(value, key))
}

func findMap(items []map[string]any, key, value string) map[string]any {
	for _, item := range items {
		if mapString(item, key) == value {
			return item
		}
	}
	return nil
}

func mustArray(value any) []any {
	items, _ := value.([]any)
	return items
}

func (s *app) settingsConfigPathValue(key string) string { return s.configString(key) }

func (s *app) settingsConfigNestedUpdate(objectKey, fieldKey string, value any) {
	s.updateConfigObjectField(objectKey, fieldKey, value)
}

func (s *app) settingsDevelopmentToolPath(key string) string { return s.developmentToolPath(key) }

func (s *app) settingsSyncBool(key string, value bool) { s.saveConfigField(key, value) }

func (s *app) settingsRefreshAction(kind string, payload map[string]any) {
	s.act(kind, "", "", "", "", "", payload)
}

func nullableString(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func mapsFromArray(values []any) []map[string]any {
	items := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if item, ok := value.(map[string]any); ok {
			items = append(items, item)
		}
	}
	return items
}

func mapString(value map[string]any, key string) string {
	if text, ok := value[key].(string); ok {
		return text
	}
	for candidate, raw := range value {
		if strings.EqualFold(candidate, key) {
			if text, ok := raw.(string); ok {
				return text
			}
		}
	}
	return ""
}

func jsonString(value any) string {
	if value == nil {
		return ""
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}

func stringArray(values []any) []string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		if item, ok := value.(string); ok {
			items = append(items, item)
		}
	}
	return items
}

func removeString(values []string, target string) []string {
	items := make([]string, 0, len(values))
	for _, value := range values {
		if value != target {
			items = append(items, value)
		}
	}
	return items
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

func removeMapIndex(values []map[string]any, index int) []map[string]any {
	if index < 0 || index >= len(values) {
		return values
	}
	return append(values[:index], values[index+1:]...)
}

func removeMapValue(values []map[string]any, target map[string]any) []map[string]any {
	for index, value := range values {
		if fmt.Sprint(value["type"]) == fmt.Sprint(target["type"]) {
			return removeMapIndex(values, index)
		}
	}
	return values
}

func firstLine(value string) string {
	line, _, _ := strings.Cut(value, "\n")
	return strings.TrimSpace(line)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func parseBoundedInt(value string, minValue, maxValue int) (int, error) {
	var parsed int
	if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d", &parsed); err != nil || fmt.Sprint(parsed) != strings.TrimSpace(value) || parsed < minValue || parsed > maxValue {
		return 0, fmt.Errorf("invalid integer")
	}
	return parsed, nil
}
