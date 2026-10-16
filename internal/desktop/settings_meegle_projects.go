package desktop

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
)

func (s *app) settingsMeegleProjects(c *ui.Context, p palette) {
	settingsCard(c, p, "Meegle 项目空间", "配置空间 Key 与短名，用于需求列表查询。", func() {
		projects := mapsFromArray(s.configArray("meegleProjects"))
		if len(projects) == 0 {
			settingsMuted(c, p, "尚未配置 Meegle 项目空间。")
		}
		structureChanged := false
		for index, project := range projects {
			id := fmt.Sprint(index)
			key, name := mapString(project, "project_key"), mapString(project, "simple_name")
			ui.Row(c).Gap(7).Children(func() {
				ui.TextInput(c.Key("meegle-project-key-"+id), s.input("setting:meegle-project-key:"+id, key)).Grow(1).Height(34).Placeholder("项目 Key")
				ui.TextInput(c.Key("meegle-project-name-"+id), s.input("setting:meegle-project-name:"+id, name)).Grow(1).Height(34).Placeholder("短名")
				if iconButton(c, "meegle-project-remove-"+id, "delete", "删除项目空间", p).Disabled(s.busy).Clicked() {
					s.cancelSettingsAutosave("settings:meegle-projects")
					draft := s.settingsMeegleProjectsDraftReader(projects)()
					for _, row := range draft {
						row["project_key"] = strings.TrimSpace(mapString(row, "project_key"))
						row["simple_name"] = strings.TrimSpace(mapString(row, "simple_name"))
					}
					s.saveMeegleProjectSettings(removeMapIndex(draft, index))
					structureChanged = true
				}
			})
		}
		if !structureChanged {
			readDraft := s.settingsMeegleProjectsDraftReader(projects)
			readLatest := func() string {
				encoded, _ := json.Marshal(readDraft())
				return string(encoded)
			}
			s.observeSettingsAutosave("settings:meegle-projects", readLatest(), s.saveSettingsMeegleProjectsAutosave, readLatest)
		}
		s.settingsMeegleDefaultSprintProject(c, p, projects)
		ui.Row(c.Key("meegle-project-add-row")).Gap(8).Children(func() {
			button := ui.ButtonBase(c.Key("meegle-project-add")).Row().Gap(6).Height(34).Padding(0, 12).Radius(17).
				AlignItems(ui.Center).Border(1, p.line).Label("添加项目空间").Disabled(s.busy)
			button.Children(func() { icon(c, "plus", 15, p.text); label(c, "添加项目空间", 12, p.text) })
			if button.Clicked() {
				if state := s.meegleProjectCatalog; state != nil && state.pickerOpen {
					s.closeMeegleProjectPicker()
				} else {
					s.openMeegleProjectPicker()
				}
			}
			s.renderMeegleProjectPicker(c, p, button)
		})
	})
}

func (s *app) settingsMeegleProjectsDraftReader(projects []map[string]any) func() []map[string]any {
	var inputs []struct{ key, name *string }
	for index, project := range projects {
		id := fmt.Sprint(index)
		inputs = append(inputs, struct{ key, name *string }{
			s.input("setting:meegle-project-key:"+id, mapString(project, "project_key")),
			s.input("setting:meegle-project-name:"+id, mapString(project, "simple_name")),
		})
	}
	return func() []map[string]any {
		draft := make([]map[string]any, 0, len(inputs))
		for _, input := range inputs {
			draft = append(draft, map[string]any{"project_key": *input.key, "simple_name": *input.name})
		}
		return draft
	}
}

func (s *app) saveSettingsMeegleProjectsAutosave(value string) {
	var captured []map[string]any
	if err := json.Unmarshal([]byte(value), &captured); err != nil || captured == nil {
		s.lastError = "无法读取 Meegle 项目设置草稿。"
		return
	}
	for _, project := range captured {
		project["project_key"] = strings.TrimSpace(mapString(project, "project_key"))
		project["simple_name"] = strings.TrimSpace(mapString(project, "simple_name"))
	}
	config, err := s.settingsConfigMap()
	if err != nil {
		s.lastError = err.Error()
		return
	}
	config["meegleProjects"] = captured
	s.saveSettingsConfigMap(config, "meegleProjects")
}
