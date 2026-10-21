package desktop

import (
	"encoding/json"
	"fmt"
	"strings"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"

	"github.com/egoist/mygo/ui"
)

func (s *app) requirementForURL(url string) (application.Requirement, bool) {
	url = strings.TrimSpace(url)
	if url == "" {
		return application.Requirement{}, false
	}
	for _, requirement := range s.requirements {
		if strings.TrimSpace(requirement.URL) == url && requirement.WorkItem.ID != "" {
			return requirement, true
		}
	}
	return application.Requirement{}, false
}

func (s *app) replicaRepositories(c *ui.Context, p palette) {
	ui.Column(c).Grow(1).MinHeight(0).Padding(16, 28, 28, 8).Gap(14).Children(func() {
		s.serviceList(c, p)
	})
}

func (s *app) serviceList(c *ui.Context, p palette) {
	groupID := s.selectedServiceGroupID()
	group := s.taskGroup(groupID)
	services := s.servicesForGroup(group)
	query := strings.TrimSpace(*s.input("service-search:"+groupID, ""))
	filtered := make([]application.Service, 0, len(services))
	for _, service := range services {
		repositoryName, repositoryPath := s.repositoryDetails(service.RepositoryID)
		searchable := strings.ToLower(strings.Join([]string{service.Name, service.ID, repositoryName, repositoryPath}, " "))
		if query == "" || strings.Contains(searchable, strings.ToLower(query)) {
			filtered = append(filtered, service)
		}
	}

	showGroupSidebar := len(s.snapshot.Groups) > 1 || (len(s.snapshot.Groups) == 1 && s.snapshot.Groups[0].ID != domain.DefaultGroupID)
	ui.Row(c).Grow(1).MinHeight(0).Gap(12).AlignItems(ui.Stretch).Children(func() {
		if showGroupSidebar {
			s.repositoryGroupSidebar(c, p, groupID)
		}

		ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Gap(10).Children(func() {
			if showGroupSidebar {
				ui.Row(c).FillWidth().Padding(10, 14).Gap(10).AlignItems(ui.Center).Background(p.soft).Radius(12).Children(func() {
					icon(c, "repo", 18, p.blue)
					label(c, firstNonEmpty(group.Name, "项目组"), 14, p.text).FontWeight(600).Grow(1).SingleLine()
					label(c, fmt.Sprintf("%d 个仓库", len(services)), 11, p.muted).SingleLine()
				})
			}
			ui.Row(c).FillWidth().MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c.Key("service-search:"+groupID), s.input("service-search:"+groupID, "")).Grow(1).MinWidth(0).Height(36).Placeholder("按仓库名称或本地路径筛选")
				if action(c, "repository-add", "plus", "添加仓库", p, true) {
					s.openRepositoryBatchDialog()
				}
				currentServices := s.servicesForGroup(s.taskGroup(groupID))
				s.overflowMenu(c, p, "repository-groups-more", "仓库与项目组操作", s.busy, func(m *overflowMenuBuilder) {
					if len(currentServices) > 1 {
						text := "调整顺序"
						if s.serviceReorderMode {
							text = "完成排序"
						}
						if m.Item("service-reorder-toggle", text).Icon("sort").Chosen() {
							s.serviceReorderMode = !s.serviceReorderMode
						}
					}
					m.Separator()
					if m.Item("group-add", "新建组").Icon("plus").Chosen() {
						s.openForm("settings.group.add", "新建项目组", "", "", "", "", formField{key: "name", label: "项目组名称"})
					}
					if groupID == "" {
						return
					}
					group := s.taskGroup(groupID)
					m.Separator()
					if m.Item("group-rename", "重命名当前组").Icon("edit").Chosen() {
						s.openForm("settings.group.rename", "重命名项目组", "", group.ID, "", "", formField{key: "name", label: "项目组名称", value: group.Name})
					}
					if len(s.snapshot.Groups) > 1 && len(currentServices) == 0 {
						m.Separator()
						if m.Item("group-remove", "删除空组").Icon("delete").Dangerous().Chosen() {
							s.confirmNative("删除项目组", "只允许删除没有服务的项目组。", group.Name, func() {
								s.serviceReorderMode = false
								s.removeConfigGroup(group.ID)
							})
						}
					}
				})
			})

			ui.Scroll(c).Grow(1).MinHeight(0).Gap(0).Children(func() {
				if len(s.snapshot.Groups) == 0 {
					emptyPanel(c, p, "还没有项目组", "新建一个组后即可添加本地 Git 仓库。")
					return
				}
				if len(services) == 0 {
					emptyPanel(c, p, "此组还没有仓库", "添加一个或多个本地 Git 仓库。")
					return
				}
				if len(filtered) == 0 {
					emptyPanel(c, p, "没有匹配的仓库", "请调整仓库名称或本地路径筛选条件。")
					return
				}
				for index, service := range filtered {
					repositoryName, repositoryPath := s.repositoryDetails(service.RepositoryID)
					ui.Row(c.Key("service-row-"+service.ID)).FillWidth().Padding(12, 9).Gap(11).AlignItems(ui.Center).Children(func() {
						icon(c, "repo", 18, p.blue)
						ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
							ui.Row(c).Gap(7).AlignItems(ui.Center).Children(func() {
								label(c, service.Name, 13, p.text).FontWeight(600).Grow(1).SingleLine()
								if !s.serviceEnabled(groupID, service.ID) {
									pill(c, "已停用", p)
								}
							})
							label(c, firstNonEmpty(repositoryPath, repositoryName, "仓库路径未配置"), 11, p.muted).SingleLine().Tooltip(firstNonEmpty(repositoryPath, repositoryName))
							label(c, fmt.Sprintf("%d 个模块", len(service.Modules)), 10, p.muted).SingleLine()
						})
						if s.serviceReorderMode {
							position := servicePosition(services, service.ID)
							if position > 0 && serviceMoveAction(c, "service-up-"+service.ID, "上移", -90, p) {
								s.moveService(groupID, service.ID, -1)
							}
							if position >= 0 && position < len(services)-1 && serviceMoveAction(c, "service-down-"+service.ID, "下移", 90, p) {
								s.moveService(groupID, service.ID, 1)
							}
						} else {
							if repositoryRowAction(c, "service-config-"+service.ID, "edit", "配置", "配置仓库 "+service.Name, p, p.text, s.busy) {
								s.openServiceEditor(groupID, service.ID, "basic")
							}
							if repositoryRowAction(c, "service-remove-"+service.ID, "delete", "移除", "从当前组移除仓库 "+service.Name, p, p.danger, s.busy) {
								s.confirmNative("从当前组移除仓库？", "这不会删除本地 Git 仓库，也不会改动已有任务；如果仍被任务引用，后端会阻止移除。", service.Name, func() {
									s.act("service.remove", "", service.ID, "", "", "", map[string]any{"groupId": groupID})
								})
							}
						}
					})
					if index < len(filtered)-1 {
						ui.Divider(c)
					}
				}
			})
		})
	})
}

func repositoryRowAction(c *ui.Context, key, image, text, description string, p palette, color ui.Color, disabled bool) bool {
	button := ui.ButtonBase(c.Key(key)).Row().Height(32).Padding(0, 10).Gap(6).Radius(8).AlignItems(ui.Center).
		Label(description).Tooltip(description).FocusRing(true).Disabled(disabled)
	if disabled {
		color = p.muted
	} else if button.Hovered() {
		button.Background(p.soft)
	}
	button.Children(func() {
		icon(c, image, 14, color)
		label(c, text, 12, color).NoWrap()
	})
	return button.Clicked()
}

func serviceMoveAction(c *ui.Context, key, title string, rotation float32, p palette) bool {
	button := ui.ButtonBase(c.Key(key)).Row().Height(30).Gap(5).Padding(0, 8).Radius(8).
		AlignItems(ui.Center).Border(1, p.line).Background(p.surface).Label(title).Tooltip(title)
	if button.Hovered() {
		button.Background(p.soft)
	}
	button.Children(func() {
		ui.Icon(c, icons["arrow"]).Width(13).Height(13).Shrink(0).TextColor(p.muted).Rotate(rotation)
		label(c, title, 10, p.text).SingleLine()
	})
	return button.Clicked()
}

func (s *app) selectedServiceGroupID() string {
	for _, group := range s.snapshot.Groups {
		if group.ID == s.serviceGroupID {
			return group.ID
		}
	}
	if len(s.snapshot.Groups) == 0 {
		s.serviceGroupID = ""
		s.serviceReorderMode = false
		return ""
	}
	previous := s.serviceGroupID
	s.serviceGroupID = s.snapshot.Groups[0].ID
	if previous != "" && previous != s.serviceGroupID {
		s.serviceReorderMode = false
	}
	return s.serviceGroupID
}

func (s *app) servicesForGroup(group application.Group) []application.Service {
	services := make([]application.Service, 0, len(group.ServiceIDs))
	if len(group.ServiceIDs) > 0 {
		for _, id := range group.ServiceIDs {
			if service, ok := s.serviceByID(group.ID, id); ok {
				services = append(services, service)
			}
		}
		return services
	}
	for _, service := range s.snapshot.Services {
		if service.GroupID == group.ID {
			services = append(services, service)
		}
	}
	return services
}

func (s *app) repositoryDetails(repositoryID string) (name, path string) {
	for _, repository := range s.snapshot.Repositories {
		if repository.ID == repositoryID {
			return repository.Name, repository.RootPath
		}
	}
	return repositoryID, ""
}

func (s *app) serviceEnabled(groupID, serviceID string) bool {
	config, err := s.readSettingsConfig()
	if err != nil {
		return true
	}
	service := findServiceConfig(config, serviceID, groupID)
	if service == nil {
		return true
	}
	if enabled, ok := service["enabled"].(bool); ok {
		return enabled
	}
	return true
}

func (s *app) moveService(groupID, serviceID string, delta int) {
	config, err := s.settingsConfigMap()
	if err != nil {
		s.lastError = err.Error()
		return
	}
	groups, _ := config["groups"].([]any)
	for _, item := range groups {
		group := mapData(item)
		if mapString(group, "id") != groupID {
			continue
		}
		services, _ := group["services"].([]any)
		position := -1
		for index, raw := range services {
			if mapString(mapData(raw), "id") == serviceID {
				position = index
				break
			}
		}
		next := position + delta
		if position < 0 || next < 0 || next >= len(services) {
			return
		}
		services[position], services[next] = services[next], services[position]
		group["services"] = services
		config["groups"] = groups
		s.saveSettingsConfigMap(config)
		return
	}
	s.lastError = "找不到当前服务所属的项目组。"
}

func servicePosition(services []application.Service, id string) int {
	for index, service := range services {
		if service.ID == id {
			return index
		}
	}
	return -1
}

func serviceAddFields(groupID string) []formField {
	return []formField{
		{key: "groupId", label: "项目组", value: groupID},
		{key: "serviceId", label: "服务 ID"},
		{key: "name", label: "服务名称"},
		{key: "repositoryId", label: "仓库", value: ""},
		{key: "masterBranch", label: "主分支", value: "origin/master"},
		{key: "enabled", label: "服务状态", value: "true", options: []string{"true", "false"}},
		{key: "developmentTool", label: "默认开发工具", value: "INTELLIJ_IDEA", options: developmentToolOptions()},
		{key: "strategy", label: "默认模块工作区策略", value: "STANDARD_WORKTREE", options: []string{"STANDARD_WORKTREE", "INDEPENDENT_CLONE"}},
		{key: "commitMessageTemplate", label: "提交说明模板", multiline: true},
		{key: "bootstrapCopyRules", label: "初始化复制规则（JSON 数组）", multiline: true, value: "[]"},
		{key: "bootstrapCommands", label: "初始化命令（JSON 数组）", multiline: true, value: "[]"},
	}
}

func developmentToolOptions() []string {
	return []string{"INTELLIJ_IDEA", "WEBSTORM", "PYCHARM", "VISUAL_STUDIO_CODE", "ANDROID_STUDIO", "DEVECO_STUDIO"}
}

func (s *app) serviceEditorFields(serviceID string, service application.Service) []formField {
	fields := []formField{
		{key: "name", label: "服务名称", value: service.Name},
		{key: "repositoryId", label: "仓库", value: service.RepositoryID},
		{key: "enabled", label: "服务状态", value: "true", options: []string{"true", "false"}},
		{key: "masterBranch", label: "主分支", value: service.MasterBranch},
		{key: "developmentTool", label: "默认开发工具", value: "INTELLIJ_IDEA", options: developmentToolOptions()},
		{key: "commitMessageTemplate", label: "提交说明模板", multiline: true},
		{key: "bootstrapCopyRules", label: "初始化复制规则（JSON 数组）", multiline: true, value: "[]"},
		{key: "bootstrapCommands", label: "初始化命令（JSON 数组）", multiline: true, value: "[]"},
	}
	config, err := s.readSettingsConfig()
	if err != nil {
		return fields
	}
	item := findServiceConfig(config, serviceID, service.GroupID)
	if item == nil {
		return fields
	}
	setFormValue(fields, "enabled", jsonBoolString(item["enabled"], "true"))
	setFormValue(fields, "masterBranch", firstNonEmpty(mapString(item, "masterBranch"), service.MasterBranch))
	setFormValue(fields, "developmentTool", firstNonEmpty(mapString(item, "developmentTool"), "INTELLIJ_IDEA"))
	setFormValue(fields, "commitMessageTemplate", mapString(item, "commitMessageTemplate"))
	bootstrap := mapData(item["bootstrap"])
	setFormValue(fields, "bootstrapCopyRules", jsonStringOrDefault(bootstrap["copyRules"], "[]"))
	setFormValue(fields, "bootstrapCommands", jsonStringOrDefault(bootstrap["commands"], "[]"))
	return fields
}

func setFormValue(fields []formField, key, value string) {
	for index := range fields {
		if fields[index].key == key {
			fields[index].value = value
			return
		}
	}
}

func jsonBoolString(value any, fallback string) string {
	if boolean, ok := value.(bool); ok {
		return fmt.Sprint(boolean)
	}
	return fallback
}

func jsonStringOrDefault(value any, fallback string) string {
	if value == nil {
		return fallback
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fallback
	}
	return string(encoded)
}

func (s *app) moduleConfigurationFields(groupID, serviceID string, module application.Module) []formField {
	name := module.Name
	masterBranch := module.MasterBranch
	strategy := module.Strategy
	customCommands := "[]"
	if config, err := s.readSettingsConfig(); err == nil {
		if service := findServiceConfig(config, serviceID, groupID); service != nil {
			if modules, ok := service["modules"].([]any); ok {
				for _, raw := range modules {
					candidate, _ := raw.(map[string]any)
					if mapString(candidate, "id") != module.ID {
						continue
					}
					name = firstNonEmpty(mapString(candidate, "name"), name)
					masterBranch = mapString(candidate, "masterBranch")
					strategy = firstNonEmpty(mapString(candidate, "strategy"), strategy)
					customCommands = jsonStringOrDefault(candidate["customCommands"], "[]")
					break
				}
			}
		}
	}
	return []formField{
		{key: "moduleId", label: "模块 ID", value: module.ID},
		{key: "name", label: "模块名称", value: name},
		{key: "masterBranch", label: "主分支（留空则继承）", value: masterBranch},
		{key: "strategy", label: "工作区策略", value: strategy, options: []string{"STANDARD_WORKTREE", "INDEPENDENT_CLONE"}},
		{key: "customCommands", label: "快捷命令配置（JSON 数组）", value: customCommands, multiline: true},
	}
}

func (s *app) moduleConfigurationFieldsWithoutID(groupID, serviceID string, module application.Module) []formField {
	fields := s.moduleConfigurationFields(groupID, serviceID, module)
	return fields[1:]
}

func (s *app) replicaSettings(c *ui.Context, p palette) {
	s.settingsScreen(c, p)
}

func (s *app) replicaSkills(c *ui.Context, p palette) {
	s.renderSkillsBrowser(c, p)
}

func emptyPanel(c *ui.Context, p palette, title, detail string) {
	ui.Column(c).FillWidth().Padding(20).Gap(8).Background(p.surface).Border(1, p.line).Radius(12).Children(func() {
		label(c, title, 14, p.text).FontWeight(600)
		label(c, detail, 12, p.muted).LineHeight(1.5)
	})
}
