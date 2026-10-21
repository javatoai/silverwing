package desktop

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"silverwing-go/internal/application"
)

func settingsExtensionSourceSaveAction(kind string) bool {
	return kind == "skills.sources/save" || kind == "codex.extensions/marketplaces/add-source"
}

func settingsExtensionSourceConfiguration(action application.Action, snapshot application.Snapshot) ([]map[string]any, string, bool, error) {
	if !settingsExtensionSourceSaveAction(action.Kind) {
		return nil, "", false, nil
	}
	sourceID := mapString(mapData(action.Payload["source"]), "id")
	if sourceID == "" {
		return nil, "", false, fmt.Errorf("保存请求缺少来源 ID")
	}
	var config map[string]any
	if err := json.Unmarshal([]byte(snapshot.ConfigJSON), &config); err != nil {
		return nil, sourceID, false, err
	}
	if config == nil {
		return nil, sourceID, false, fmt.Errorf("读取到的配置为空")
	}
	key := "skillSources"
	if action.Kind == "codex.extensions/marketplaces/add-source" {
		key = "codexPluginMarketplaceSources"
	}
	sources := mapsFromData(config[key])
	for _, source := range sources {
		if mapString(source, "id") == sourceID && settingsExtensionSourceValuesMatch(mapData(action.Payload["source"]), source) {
			return sources, sourceID, true, nil
		}
	}
	return sources, sourceID, false, nil
}

func settingsExtensionSourceValuesMatch(submitted, saved map[string]any) bool {
	for key := range submitted {
		if !reflect.DeepEqual(settingsExtensionSourceValue(submitted, key), settingsExtensionSourceValue(saved, key)) {
			return false
		}
	}
	return true
}

func settingsExtensionSourceValue(source map[string]any, key string) any {
	value := source[key]
	switch key {
	case "ref", "skillRoot":
		if value == nil {
			return ""
		}
	case "marketplaceDirectory":
		if value == nil {
			return "plugins"
		}
	case "enabled":
		if value == nil {
			return true
		}
	}
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return value
}

// Called after a fresh application Snapshot has been applied. A source is saved
// before its checkout refresh starts, so cancellation/refresh failure can still
// leave a committed source. Keep that error visible while reconciling the form
// and cached rows; never retry a clone or change any configuration here.
func (s *app) reconcileSettingsExtensionSourceSave(action application.Action, snapshot application.Snapshot, operationErr error) bool {
	if operationErr == nil || !settingsExtensionSourceSaveAction(action.Kind) {
		return false
	}
	sources, sourceID, committed, recoveryErr := settingsExtensionSourceConfiguration(action, snapshot)
	if recoveryErr != nil {
		s.settingsExtensionSourceRecoveryFailure(action, operationErr, recoveryErr)
		return false
	}
	if snapshot.Home != s.snapshot.Home || sourceID == "" {
		return false
	}
	if !committed {
		if s.settingsExtensionSourceFormMatches(action) {
			s.dialogOpen = true
		}
		s.lastError = "来源未保存，输入已保留：" + operationErr.Error()
		return false
	}
	if action.Kind == "skills.sources/save" {
		s.settingsSkillSources = reconcileSettingsExtensionSourceRows(s.settingsSkillSources, sources, sourceID, operationErr.Error())
	} else {
		s.settingsMarketplaces = reconcileSettingsExtensionSourceRows(s.settingsMarketplaces, sources, sourceID, operationErr.Error())
	}
	if s.settingsExtensionSourceFormMatches(action) {
		s.dialogOpen = false
		s.resetSettingsExtensionForm()
	}
	s.lastError = "来源已保存，但目录读取未完成：" + operationErr.Error()
	return true
}

func (s *app) settingsExtensionSourceRecoveryFailure(action application.Action, operationErr, recoveryErr error) bool {
	if !settingsExtensionSourceSaveAction(action.Kind) {
		return false
	}
	if s.settingsExtensionSourceFormMatches(action) {
		s.dialogOpen = true
	}
	s.lastError = "无法确认来源保存状态，输入已保留"
	if recoveryErr != nil {
		s.lastError += "：" + recoveryErr.Error()
	}
	if operationErr != nil {
		s.lastError += "；原操作失败：" + operationErr.Error()
	}
	return true
}

func (s *app) settingsExtensionSourceFormMatches(action application.Action) bool {
	if s.form.kind != action.Kind {
		return false
	}
	source := mapData(action.Payload["source"])
	matchedName, matchedRepository := false, false
	for _, field := range s.form.fields {
		switch field.key {
		case "name", "repositoryUrl", "ref", "skillRoot":
			value := field.value
			if input := s.inputs["form:"+field.key]; input != nil {
				value = *input
			}
			if strings.TrimSpace(value) != strings.TrimSpace(mapString(source, field.key)) {
				return false
			}
			matchedName = matchedName || field.key == "name"
			matchedRepository = matchedRepository || field.key == "repositoryUrl"
		}
	}
	return matchedName && matchedRepository
}

func reconcileSettingsExtensionSourceRows(previous, sources []map[string]any, savedID, failure string) []map[string]any {
	rows := make([]map[string]any, 0, len(sources))
	for _, source := range sources {
		id := mapString(source, "id")
		status := make(map[string]any)
		for _, row := range previous {
			if mapString(mapData(row["source"]), "id") == id {
				for key, value := range mapData(row["status"]) {
					status[key] = value
				}
				break
			}
		}
		if id == savedID {
			status["error"] = failure
		}
		rows = append(rows, map[string]any{"source": source, "status": status})
	}
	return rows
}
