package desktop

import (
	"encoding/json"
	"strings"

	"github.com/egoist/mygo/ui"
	"silverwing-go/internal/application"
)

func (s *app) settingsMeegleDefaultSprintProject(c *ui.Context, p palette, projects []map[string]any) {
	selectedKey := s.configString("meegleDefaultSprintProjectKey")
	selectedLabel := "未指定"
	if selectedKey != "" {
		selectedLabel = selectedKey
		for _, project := range projects {
			if mapString(project, "project_key") == selectedKey {
				selectedLabel = meegleProjectLabel(project)
				break
			}
		}
	}
	settingsHelpLabel(c, p, "默认 Sprint 团队空间", "多个 Sprint 同时进行时，优先选择此空间中唯一进行中的 Sprint；仍有多个时手动选择。", 12, 600)
	button := ui.ButtonBase(c.Key("meegle-default-sprint-project-picker")).Row().Height(36).FillWidth().Padding(0, 12).Gap(8).Radius(8).Border(1, p.line).Background(p.surface).Label(selectedLabel)
	button.Children(func() {
		label(c, selectedLabel, 11, p.text).Grow(1).SingleLine()
		icon(c, "chevron-down", 14, p.muted)
	})
	button.Menu(func(menu *ui.Menu) {
		if menu.Item("未指定").Disabled(s.busy || selectedKey == "").Chosen() {
			s.saveConfigField("meegleDefaultSprintProjectKey", nil)
		}
		for _, project := range projects {
			projectKey := strings.TrimSpace(mapString(project, "project_key"))
			projectLabel := meegleProjectLabel(project)
			if menu.Item(projectLabel).Disabled(s.busy || projectKey == selectedKey).Chosen() {
				s.saveConfigField("meegleDefaultSprintProjectKey", nullableString(projectKey))
			}
		}
	})
}

func meegleProjectLabel(project map[string]any) string {
	name := strings.TrimSpace(mapString(project, "simple_name"))
	key := strings.TrimSpace(mapString(project, "project_key"))
	if name == "" {
		return key
	}
	if key == "" {
		return name
	}
	return name + " · " + key
}

func (s *app) saveMeegleProjectSettings(projects []map[string]any) {
	s.saveMeegleProjectSettingsWithPickerToken(projects, "", "")
}

func (s *app) saveMeegleProjectSettingsWithPickerToken(projects []map[string]any, token, projectKey string) bool {
	current := mapsFromArray(s.configArray("meegleProjects"))
	if token == "" && s.settingsAutosaveDraftPending("settings:meegle-projects") {
		draft := s.settingsMeegleProjectsDraftReader(current)()
		for _, project := range projects {
			for index, saved := range current {
				if mapString(project, "project_key") == mapString(saved, "project_key") {
					project["project_key"], project["simple_name"] = draft[index]["project_key"], draft[index]["simple_name"]
					break
				}
			}
		}
	}
	for _, project := range projects {
		project["project_key"] = strings.TrimSpace(mapString(project, "project_key"))
		project["simple_name"] = strings.TrimSpace(mapString(project, "simple_name"))
	}
	config, err := s.settingsConfigMap()
	if err != nil {
		s.lastError = err.Error()
		if state := s.settingsUnit.state.meegleProjectCatalog; token != "" && state != nil {
			state.pickerSaveError = "添加项目空间失败：" + err.Error()
		}
		return false
	}
	config["meegleProjects"] = projects
	if token == "" {
		s.cancelSettingsAutosave("settings:meegle-projects")
		s.saveSettingsConfigMap(config, "meegleProjects")
		return true
	}
	if s.busy {
		s.lastError = "当前有操作正在执行，请等待完成或取消后重试。"
		if state := s.settingsUnit.state.meegleProjectCatalog; state != nil {
			state.pickerSaveError = "添加失败：当前有操作正在执行，请稍后重试。"
		}
		return false
	}
	baseline, err := s.settingsConfigMap()
	if err != nil {
		s.lastError = err.Error()
		if state := s.settingsUnit.state.meegleProjectCatalog; state != nil {
			state.pickerSaveError = "添加项目空间失败：" + err.Error()
		}
		return false
	}
	update, err := settingsConfigFieldsUpdate(baseline, config, []string{"meegleProjects"})
	if err != nil {
		s.lastError = "无法编码设置：" + err.Error()
		if state := s.settingsUnit.state.meegleProjectCatalog; state != nil {
			state.pickerSaveError = s.lastError
		}
		return false
	}
	if len(update.Fields)+len(update.ObjectFields) == 0 {
		if state := s.settingsUnit.state.meegleProjectCatalog; state != nil {
			state.pickerSaveError = "所选项目空间没有产生可保存的配置变化。"
		}
		return false
	}
	s.act("config.fields.save", "", "", "", "", "", map[string]any{
		"update":                            update,
		"meegleProjectPickerSaveToken":      token,
		"meegleProjectPickerSaveProjectKey": projectKey,
	})
	return true
}

func (s *app) saveSettingsCLIPath(key, value string) {
	config, err := s.settingsConfigMap()
	if err != nil {
		s.lastError = err.Error()
		return
	}
	normalized := strings.TrimSpace(value)
	config[key] = nullableString(normalized)
	tool := map[string]string{"meegleExecutablePath": "meegle"}[key]
	if tool != "" {
		if s.busy {
			s.lastError = "当前有操作正在执行，请等待完成或取消后重试。"
			return
		}
		raw, err := json.Marshal(config[key])
		if err != nil {
			s.lastError = "无法编码设置：" + err.Error()
			return
		}
		update := application.ConfigFieldsUpdate{Fields: map[string]json.RawMessage{key: raw}}
		s.act("config.fields.save", "", "", "", "", "", map[string]any{
			"update": update, "settingsCLIPathSave": settingsCLIPathSaveScope{Tool: tool, Path: normalized, Home: s.snapshot.Home},
		})
		return
	}
	s.saveSettingsConfigMap(config, key)
}

type settingsCLIPathSaveScope struct{ Tool, Path, Home string }

// Called after the successful save's snapshot is applied. Authentication and
// status requests must capture the new command/home, never the previous draft.
func (s *app) afterSettingsCLIPathSave(action application.Action) {
	if action.Kind != "config.fields.save" || s.page != "settings" {
		return
	}
	scope, ok := action.Payload["settingsCLIPathSave"].(settingsCLIPathSaveScope)
	if !ok || scope.Tool != "meegle" || scope.Home != s.snapshot.Home || s.settingsUnit.state.settingsPage != "cli-"+scope.Tool {
		return
	}
	key := scope.Tool + "ExecutablePath"
	update, ok := action.Payload["update"].(application.ConfigFieldsUpdate)
	if !ok || len(update.Fields) != 1 || len(update.ObjectFields) != 0 || update.Fields[key] == nil || s.configString(key) != scope.Path {
		return
	}
	var requested *string
	if json.Unmarshal(update.Fields[key], &requested) != nil || (requested == nil && scope.Path != "") || (requested != nil && strings.TrimSpace(*requested) != scope.Path) {
		return
	}
	s.act("cli.status", "", "", "", "", "", map[string]any{"tool": scope.Tool, "force": true})
}

func (s *app) settingsRequirementCopyIcons(c *ui.Context, p palette) {
	legacyEnabled := s.configBool("showTaskAreaCopyIcons", true)
	requirementEnabled := s.configBool("showTaskAreaRequirementCopyIcons", true)
	current := legacyEnabled && requirementEnabled
	value := s.settingsBoolInput("showTaskAreaRequirementCopyIcons", current)
	settingsSwitchRow(c, p, "showTaskAreaRequirementCopyIcons", "需求链接和需求编号复制", "显示需求链接与需求编号旁的复制图标。", value)
	if *value == current {
		return
	}
	config, err := s.settingsConfigMap()
	if err != nil {
		s.lastError = err.Error()
		return
	}
	config["showTaskAreaCopyIcons"] = true
	config["showTaskAreaRequirementCopyIcons"] = *value
	s.saveSettingsConfigMap(config, "showTaskAreaCopyIcons", "showTaskAreaRequirementCopyIcons")
}
