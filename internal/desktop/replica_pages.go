package desktop

import (
	"encoding/json"
	"fmt"
	"strings"

	"silverwing-go/internal/application"

	"github.com/egoist/mygo/ui"
)

func (s *app) requirementForURL(url string) (application.Requirement, bool) {
	url = strings.TrimSpace(url)
	if url == "" {
		return application.Requirement{}, false
	}
	for _, requirement := range s.requirementUnit.state.requirements {
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
	const searchKey = "service-search"
	query := strings.TrimSpace(*s.input(searchKey, ""))
	browsing := &s.workspaceUnit.state.serviceList
	filtered := browsing.project(s.serviceCatalog(), s.settingsUnit.state.configJSON, query, s.serviceEnabled)
	services := browsing.services
	runtimeVisible := len(services) > 0 && s.integrationReady("genbu") && !s.workspaceUnit.state.serviceReorderMode
	windowWidth, _ := c.Size()
	listWidth := browsing.canvas.Bounds(c).W
	if browsing.windowWidth != windowWidth || listWidth <= 0 {
		browsing.windowWidth = windowWidth
		c.AnimationFrame()
	}
	compactRuntime := listWidth < serviceCatalogDirectActionsMinWidth

	ui.Column(c.Key("service-catalog")).Bind(&browsing.canvas).Grow(1).MinWidth(0).MinHeight(0).Gap(10).Children(func() {
		ui.Row(c).FillWidth().MinWidth(0).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c.Key(searchKey), s.input(searchKey, "")).Grow(1).MinWidth(0).Height(36).Placeholder("按仓库名称或本地路径筛选")
			if runtimeVisible {
				label(c, "运行环境", 12, p.muted).NoWrap()
				renderServiceEnvironmentTabs(c, p, "catalog-runtime-environments", "运行环境", s.catalogRuntimeEnvironment(), s.selectCatalogRuntimeEnvironment)
			}
			if action(c, "repository-add", "plus", "添加仓库", p, true) {
				s.openRepositoryBatchDialog()
			}
			if len(services) > 1 {
				s.overflowMenu(c, p, "repository-more", "仓库操作", s.busy, func(m *overflowMenuBuilder) {
					text := "调整顺序"
					if s.workspaceUnit.state.serviceReorderMode {
						text = "完成排序"
					}
					if m.Item("service-reorder-toggle", text).Icon("sort").Chosen() {
						s.workspaceUnit.state.serviceReorderMode = !s.workspaceUnit.state.serviceReorderMode
					}
				})
			}
		})

		ui.Column(c).Grow(1).MinHeight(0).Gap(0).Children(func() {
			if len(services) == 0 {
				emptyPanel(c, p, "还没有仓库", "添加一个或多个本地 Git 仓库。")
				return
			}
			if len(filtered) == 0 {
				emptyPanel(c, p, "没有匹配的仓库", "请调整仓库名称或本地路径筛选条件。")
				return
			}
			browsing.list.Key = func(position int) any { return filtered[position].ID }
			ui.List(c.Key("service-list"), &browsing.list, len(filtered)).Grow(1).MinHeight(0).Gap(0).Rows(func(row ui.ListRow) {
				index, c := row.Index, row.Context
				service := filtered[index]
				repositoryName, repositoryPath := s.repositoryDetails(service.RepositoryID)
				ui.Row(c.Key("service-row-"+service.ID)).FillWidth().Padding(12, 9).Gap(11).AlignItems(ui.Center).Children(func() {
					icon(c, "repo", 18, p.blue)
					ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
						label(c, service.Name, 13, p.text).FontWeight(600).SingleLine()
						label(c, firstNonEmpty(repositoryPath, repositoryName, "仓库路径未配置"), 11, p.muted).SingleLine().TruncateStart().Tooltip(firstNonEmpty(repositoryPath, repositoryName))
						label(c, fmt.Sprintf("%d 个模块", len(service.Modules)), 10, p.muted).SingleLine()
					})
					if !s.serviceEnabled(service.ID) {
						pill(c, "已停用", p)
					}
					if s.workspaceUnit.state.serviceReorderMode {
						position := browsing.positions[service.ID]
						if position > 0 && position != browsing.enabledCount && serviceMoveAction(c, "service-up-"+service.ID, "上移", -90, p) {
							s.moveService(service.ID, services[position-1].ID)
						}
						if position >= 0 && position+1 < len(services) && position+1 != browsing.enabledCount && serviceMoveAction(c, "service-down-"+service.ID, "下移", 90, p) {
							s.moveService(service.ID, services[position+1].ID)
						}
					} else {
						s.renderServiceCatalogActions(c, p, service, runtimeVisible, compactRuntime)
					}
				})
				if index < len(filtered)-1 {
					ui.Divider(c)
				}
			})
		})
	})
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

func (s *app) repositoryDetails(repositoryID string) (name, path string) {
	if repository, found := s.serviceCatalog().repositories[repositoryID]; found {
		return repository.Name, repository.RootPath
	}
	return repositoryID, ""
}

func (s *app) serviceEnabled(serviceID string) bool {
	return mapBool(s.readServiceConfig(serviceID), "enabled", true)
}

func (s *app) moveService(serviceID, adjacentID string) {
	config, err := s.settingsConfigMap()
	if err != nil {
		s.lastError = err.Error()
		return
	}
	services, _ := config["services"].([]any)
	current, adjacent := -1, -1
	for position, item := range services {
		switch mapString(mapData(item), "id") {
		case serviceID:
			current = position
		case adjacentID:
			adjacent = position
		}
	}
	if current < 0 || adjacent < 0 || mapBool(mapData(services[current]), "enabled", true) != mapBool(mapData(services[adjacent]), "enabled", true) {
		return
	}
	services[current], services[adjacent] = services[adjacent], services[current]
	s.saveSettingsConfigMap(config, "services")
}

func servicePosition(services []application.Service, id string) int {
	for index, service := range services {
		if service.ID == id {
			return index
		}
	}
	return -1
}

func serviceAddFields() []formField {
	return []formField{
		{key: "serviceId", label: "服务 ID"},
		{key: "name", label: "服务名称"},
		{key: "repositoryId", label: "仓库", value: ""},
		{key: "masterBranch", label: "主分支", value: "origin/master"},
		{key: "enabled", label: "服务状态", value: "true", options: []string{"true", "false"}},
		{key: "genbuServiceName", label: "Genbu 服务名"},
		{key: "genbuProbeEnabled", label: "Genbu 探测", value: "false", options: []string{"false", "true"}},
		{key: "developmentTool", label: "默认开发工具", value: "INTELLIJ_IDEA", options: developmentToolOptions()},
		{key: "testTagBaselineRef", label: "测试 Tag 基线", value: "origin/release/test"},
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
		{key: "genbuServiceName", label: "Genbu 服务名"},
		{key: "genbuProbeEnabled", label: "Genbu 探测", value: "false", options: []string{"false", "true"}},
		{key: "masterBranch", label: "主分支", value: service.MasterBranch},
		{key: "testTagBaselineRef", label: "测试 Tag 基线"},
		{key: "developmentTool", label: "默认开发工具", value: "INTELLIJ_IDEA", options: developmentToolOptions()},
		{key: "commitMessageTemplate", label: "提交说明模板", multiline: true},
		{key: "bootstrapCopyRules", label: "初始化复制规则（JSON 数组）", multiline: true, value: "[]"},
		{key: "bootstrapCommands", label: "初始化命令（JSON 数组）", multiline: true, value: "[]"},
	}
	item := s.readServiceConfig(serviceID)
	if item == nil {
		return fields
	}
	setFormValue(fields, "enabled", jsonBoolString(item["enabled"], "true"))
	setFormValue(fields, "genbuServiceName", mapString(item, "genbuServiceName"))
	setFormValue(fields, "genbuProbeEnabled", jsonBoolString(item["genbuProbeEnabled"], "false"))
	setFormValue(fields, "testTagBaselineRef", mapString(item, "testTagBaselineRef"))
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

func (s *app) moduleConfigurationFields(serviceID string, module application.Module) []formField {
	name := module.Name
	masterBranch := module.MasterBranch
	strategy := module.Strategy
	tagEnabled := fmt.Sprint(module.TagEnabled)
	tagMode, tagTargetRef, tagMessagePrefix := "MERGE_TO_TARGET_BRANCH", module.TagTargetRef, "Tag"
	customCommands := "[]"
	if service := s.readServiceConfig(serviceID); service != nil {
		if modules, ok := service["modules"].([]any); ok {
			for _, raw := range modules {
				candidate, _ := raw.(map[string]any)
				if mapString(candidate, "id") != module.ID {
					continue
				}
				name = firstNonEmpty(mapString(candidate, "name"), name)
				masterBranch = mapString(candidate, "masterBranch")
				strategy = firstNonEmpty(mapString(candidate, "strategy"), strategy)
				tagMode = firstNonEmpty(mapString(candidate, "tagMode"), tagMode)
				tagTargetRef = mapString(candidate, "tagTargetRef")
				tagMessagePrefix = firstNonEmpty(mapString(candidate, "tagMessagePrefix"), tagMessagePrefix)
				if value, ok := candidate["tagEnabled"].(bool); ok {
					tagEnabled = fmt.Sprint(value)
				}
				customCommands = jsonStringOrDefault(candidate["customCommands"], "[]")
				break
			}
		}
	}
	return []formField{
		{key: "moduleId", label: "模块 ID", value: module.ID},
		{key: "name", label: "模块名称", value: name},
		{key: "masterBranch", label: "主分支（留空则继承）", value: masterBranch},
		{key: "strategy", label: "工作区策略", value: strategy, options: []string{"STANDARD_WORKTREE", "INDEPENDENT_CLONE"}},
		{key: "tagEnabled", label: "Tag", value: tagEnabled, options: []string{"true", "false"}},
		{key: "tagMode", label: "Tag 模式", value: tagMode, options: []string{"MERGE_TO_TARGET_BRANCH", "CURRENT_BRANCH"}},
		{key: "tagTargetRef", label: "Tag 目标分支（留空则继承）", value: tagTargetRef},
		{key: "tagMessagePrefix", label: "Tag 消息前缀", value: tagMessagePrefix},
		{key: "customCommands", label: "快捷命令配置（JSON 数组）", value: customCommands, multiline: true},
	}
}

func (s *app) moduleConfigurationFieldsWithoutID(serviceID string, module application.Module) []formField {
	fields := s.moduleConfigurationFields(serviceID, module)
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
