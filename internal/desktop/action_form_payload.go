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
	if form.kind == "task.requirement-link" {
		s.submitTaskRequirementForm(form, payload)
		return
	}
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
	if form.kind == "repository.add" {
		path = payloadString(payload, "path")
	}
	if form.kind == "config.import-preview" {
		s.materialsUnit.state.importPath = path
	}
	if form.mode == "task-create" {
		groupID, serviceIDs, selections := s.taskSelectionPayload(payloadString(payload, "branch"))
		if groupID == "" {
			s.lastError = "请选择项目组。"
			return
		}
		payload["groupId"] = groupID
		payload["serviceIds"] = serviceIDs
		payload["serviceSelections"] = selections
	}
	if form.mode == "workspace-add" {
		if s.workspaceUnit.state.workspaceAddServiceID == "" || s.workspaceUnit.state.workspaceAddModuleID == "" {
			s.lastError = "请选择服务和模块。"
			return
		}
		serviceID, moduleID = s.workspaceUnit.state.workspaceAddServiceID, s.workspaceUnit.state.workspaceAddModuleID
		payload["baseRef"] = payloadString(payload, "baseRef")
		payload["strategy"] = payloadString(payload, "strategy")
		payload["branch"] = payloadString(payload, "branch")
	}
	if form.kind == "service.save" && (form.mode == "service-add" || form.mode == "service-edit") {
		var err error
		serviceID, err = s.serviceFormPayload(form, payload)
		if err != nil {
			s.lastError = err.Error()
			return
		}
	}
	if form.kind == "service.module-save" {
		if moduleID == "" {
			moduleID = payloadString(payload, "moduleId")
		}
		if moduleID == "" || payloadString(payload, "name") == "" {
			s.lastError = "模块 ID 和名称不能为空。"
			return
		}
		tagEnabled, err := parseFormBool(payload, "tagEnabled", true)
		if err != nil {
			s.lastError = err.Error()
			return
		}
		payload["serviceId"] = serviceID
		payload["groupId"] = s.serviceFormGroupID(payload)
		payload["moduleId"] = moduleID
		payload["tagEnabled"] = tagEnabled
		if err := s.moduleFormPayload(serviceID, moduleID, payload); err != nil {
			s.lastError = err.Error()
			return
		}
	}
	if form.kind == "service.module-remove" {
		payload["serviceId"] = serviceID
		payload["groupId"] = s.serviceFormGroupID(payload)
		payload["moduleId"] = moduleID
	}
	if form.kind == "service.remove" {
		payload["groupId"] = s.serviceFormGroupID(payload)
	}
	if s.settingsActionForm(form, payload) {
		if s.win != nil {
			s.win.Invalidate()
		}
		return
	}
	if expected := s.confirmationValue(form, serviceID, moduleID); expected != "" && payloadString(payload, "confirm") != expected {
		s.lastError = "确认内容不匹配，操作未执行。"
		return
	}
	s.dialogOpen = false
	s.act(form.kind, form.taskID, serviceID, moduleID, path, text, payload)
}

func (s *app) confirmationValue(form actionForm, serviceID, moduleID string) string {
	switch form.kind {
	case "repository.remove":
		for _, repository := range s.snapshot.Repositories {
			if repository.ID == serviceID {
				return repository.Name
			}
		}
	case "service.remove":
		if service, ok := s.serviceByID(s.selectedServiceGroupID(), serviceID); ok {
			return service.Name
		}
	case "service.module-remove":
		if service, ok := s.serviceByID(s.selectedServiceGroupID(), serviceID); ok {
			for _, module := range service.Modules {
				if module.ID == moduleID {
					return module.ID
				}
			}
		}
	case "workspace.remove", "skills.remove":
		return "REMOVE"
	case "document.trash":
		return "TRASH"
	}
	return ""
}

func (s *app) taskSelectionPayload(branch string) (string, []string, []any) {
	group := s.taskGroup(s.taskUnit.state.taskGroupID)
	serviceIDs := make([]string, 0)
	selections := make([]any, 0)
	for _, serviceID := range group.ServiceIDs {
		if !s.taskUnit.state.taskServices[serviceID] {
			continue
		}
		service, ok := s.serviceByID(group.ID, serviceID)
		if !ok {
			continue
		}
		modules := make([]any, 0)
		for _, module := range service.Modules {
			if !s.taskUnit.state.taskModules[serviceID][module.ID] {
				continue
			}
			strategy := module.Strategy
			if strategy == "" {
				strategy = "STANDARD_WORKTREE"
			}
			baseRef := module.MasterBranch
			if baseRef == "" {
				baseRef = service.MasterBranch
			}
			modules = append(modules, map[string]any{
				"id": module.ID, "name": module.Name, "strategy": strategy,
				"baseRef": baseRef, "baseRemote": "origin", "targetBranch": branch,
				"source": "CONFIGURED", "tagEnabled": module.TagEnabled,
				"tagMode": "MERGE_TO_TARGET_BRANCH", "tagTargetRef": module.TagTargetRef,
				"tagMessagePrefix": "Tag",
			})
		}
		if len(modules) == 0 {
			continue
		}
		serviceIDs = append(serviceIDs, serviceID)
		selections = append(selections, map[string]any{"serviceId": serviceID, "modules": modules})
	}
	return group.ID, serviceIDs, selections
}

func (s *app) serviceFormPayload(form actionForm, payload map[string]any) (string, error) {
	serviceID := form.serviceID
	if serviceID == "" {
		serviceID = payloadString(payload, "serviceId")
	}
	name := payloadString(payload, "name")
	repositoryID := payloadString(payload, "repositoryId")
	masterBranch := payloadString(payload, "masterBranch")
	strategy := payloadString(payload, "strategy")
	if strategy == "" {
		strategy = "STANDARD_WORKTREE"
	}

	if form.mode == "service-add" {
		groupID := payloadString(payload, "groupId")
		if groupID == "" || serviceID == "" || name == "" || repositoryID == "" || masterBranch == "" {
			return "", fmt.Errorf("项目组、服务 ID、名称、仓库和主分支均不能为空")
		}
		enabled, err := parseFormBool(payload, "enabled", true)
		if err != nil {
			return "", err
		}
		genbuProbeEnabled, err := parseFormBool(payload, "genbuProbeEnabled", false)
		if err != nil {
			return "", err
		}
		copyRules, err := parseJSONArrayField(payload, "bootstrapCopyRules")
		if err != nil {
			return "", err
		}
		bootstrapCommands, err := parseJSONArrayField(payload, "bootstrapCommands")
		if err != nil {
			return "", err
		}
		customCommands, err := parseJSONArrayField(payload, "customCommands")
		if err != nil {
			return "", err
		}
		genbuServiceName := payloadString(payload, "genbuServiceName")
		if genbuServiceName == "" {
			genbuServiceName = name
		}
		baseline := payloadString(payload, "testTagBaselineRef")
		if baseline == "" {
			baseline = "origin/release/test"
		}
		payload["service"] = map[string]any{
			"id": serviceID, "repositoryId": repositoryID, "displayName": name,
			"enabled": enabled, "genbuProbeEnabled": genbuProbeEnabled, "genbuServiceName": genbuServiceName,
			"masterBranch": masterBranch, "testTagBaselineRef": baseline,
			"developmentTool": firstNonEmpty(payloadString(payload, "developmentTool"), "INTELLIJ_IDEA"), "modules": []any{map[string]any{
				"id": "default", "name": "default", "strategy": strategy,
				"tagEnabled": true, "tagMode": "MERGE_TO_TARGET_BRANCH",
				"tagMessagePrefix": "Tag", "customCommands": customCommands,
			}},
			"bootstrap":             map[string]any{"copyRules": copyRules, "commands": bootstrapCommands},
			"commitMessageTemplate": payloadString(payload, "commitMessageTemplate"),
		}
		payload["groupId"] = groupID
		return serviceID, nil
	}
	groupID := s.serviceFormGroupID(payload)
	payload["groupId"] = groupID

	var config map[string]any
	if err := json.Unmarshal([]byte(s.settingsUnit.state.configJSON), &config); err != nil {
		return "", fmt.Errorf("读取当前服务配置失败：%w", err)
	}
	service := findServiceConfig(config, serviceID, groupID)
	if service == nil {
		return "", fmt.Errorf("服务 %q 不在当前配置中", serviceID)
	}
	if name != "" {
		service["displayName"] = name
	}
	if repositoryID != "" {
		service["repositoryId"] = repositoryID
	}
	service["masterBranch"] = masterBranch
	service["testTagBaselineRef"] = payloadString(payload, "testTagBaselineRef")
	service["genbuServiceName"] = payloadString(payload, "genbuServiceName")
	enabled, err := parseFormBool(payload, "enabled", true)
	if err != nil {
		return "", err
	}
	genbuProbeEnabled, err := parseFormBool(payload, "genbuProbeEnabled", false)
	if err != nil {
		return "", err
	}
	service["enabled"] = enabled
	service["genbuProbeEnabled"] = genbuProbeEnabled
	service["developmentTool"] = payloadString(payload, "developmentTool")
	service["commitMessageTemplate"] = payloadString(payload, "commitMessageTemplate")
	copyRules, err := parseJSONArrayField(payload, "bootstrapCopyRules")
	if err != nil {
		return "", err
	}
	bootstrapCommands, err := parseJSONArrayField(payload, "bootstrapCommands")
	if err != nil {
		return "", err
	}
	bootstrap := mapData(service["bootstrap"])
	bootstrap["copyRules"] = copyRules
	bootstrap["commands"] = bootstrapCommands
	service["bootstrap"] = bootstrap
	payload["service"] = service
	return serviceID, nil
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

func (s *app) moduleFormPayload(serviceID, moduleID string, payload map[string]any) error {
	config, err := s.settingsConfigMap()
	if err != nil {
		return err
	}
	service := findServiceConfig(config, serviceID, s.serviceFormGroupID(payload))
	if service == nil {
		return fmt.Errorf("服务 %q 不在当前配置中", serviceID)
	}
	modules, _ := service["modules"].([]any)
	var module map[string]any
	for _, raw := range modules {
		candidate := mapData(raw)
		if mapString(candidate, "id") == moduleID {
			module = candidate
			break
		}
	}
	if module == nil {
		module = map[string]any{"id": moduleID, "customCommands": []any{}}
	}
	module["id"] = moduleID
	module["name"] = payloadString(payload, "name")
	module["strategy"] = payloadString(payload, "strategy")
	module["tagMode"] = payloadString(payload, "tagMode")
	module["tagMessagePrefix"] = payloadString(payload, "tagMessagePrefix")
	tagEnabled, err := parseFormBool(payload, "tagEnabled", true)
	if err != nil {
		return err
	}
	module["tagEnabled"] = tagEnabled
	masterBranch := payloadString(payload, "masterBranch")
	tagTargetRef := payloadString(payload, "tagTargetRef")
	if masterBranch == "" {
		module["masterBranch"] = nil
	} else {
		module["masterBranch"] = masterBranch
	}
	if tagTargetRef == "" {
		module["tagTargetRef"] = nil
	} else {
		module["tagTargetRef"] = tagTargetRef
	}
	customCommands, err := parseJSONArrayField(payload, "customCommands")
	if err != nil {
		return err
	}
	module["customCommands"] = customCommands
	payload["module"] = module
	return nil
}

func (s *app) serviceFormGroupID(payload map[string]any) string {
	if groupID := payloadString(payload, "groupId"); groupID != "" {
		return groupID
	}
	return s.selectedServiceGroupID()
}

// A service ID is unique only within its owning group. An unscoped lookup is
// allowed only when the ID resolves unambiguously.
func findServiceConfig(config map[string]any, serviceID string, groupIDs ...string) map[string]any {
	// Editing callers own config; do not borrow the settings read cache here.
	return newDesktopServiceConfigIndex(config).find(serviceID, groupIDs...)
}

func payloadString(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}
