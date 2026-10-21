package application

import (
	"encoding/json"
	"errors"
	"strings"

	"silverwing-go/internal/domain"
)

func (a *App) moduleConfig(action Action) (ActionResult, error) {
	serviceID := action.ServiceID
	if serviceID == "" {
		serviceID = textPayload(action, "serviceId")
	}
	moduleID := action.ModuleID
	if moduleID == "" {
		moduleID = textPayload(action, "moduleId")
	}
	groupID := textPayload(action, "groupId")
	_, err := a.Config.Update(func(c domain.AppConfig) (domain.AppConfig, error) {
		for gi := range c.Groups {
			if groupID != "" && c.Groups[gi].ID != groupID {
				continue
			}
			for si := range c.Groups[gi].Services {
				service := &c.Groups[gi].Services[si]
				if service.ID != serviceID {
					continue
				}
				index := -1
				for i, m := range service.Modules {
					if m.ID == moduleID {
						index = i
						break
					}
				}
				if action.Kind == "service.module-remove" {
					if index < 0 {
						return c, errors.New("模块不存在")
					}
					service.Modules = append(service.Modules[:index], service.Modules[index+1:]...)
					return c, nil
				}
				if strings.TrimSpace(moduleID) == "" {
					return c, errors.New("模块 ID 不能为空")
				}
				module := domain.ServiceModuleConfig{ID: moduleID, Name: "default", Strategy: domain.WorkspaceStandardWorktree, TagEnabled: true, TagMode: domain.TagBuildMergeToTargetBranch, TagMessagePrefix: "Tag", CustomCommands: []domain.WorkspaceCommandConfig{}}
				if index >= 0 {
					module = service.Modules[index]
				}
				if _, ok := action.Payload["masterBranch"]; ok {
					module.MasterBranch = optional(textPayload(action, "masterBranch"))
				}
				if _, ok := action.Payload["tagTargetRef"]; ok {
					module.TagTargetRef = optional(textPayload(action, "tagTargetRef"))
				}
				if name := textPayload(action, "name"); name != "" {
					module.Name = name
				}
				if strategy := textPayload(action, "strategy"); strategy != "" {
					module.Strategy = domain.WorkspaceStrategy(strategy)
				}
				if mode := textPayload(action, "tagMode"); mode != "" {
					module.TagMode = domain.TagBuildMode(mode)
				}
				if _, ok := action.Payload["tagMessagePrefix"]; ok {
					module.TagMessagePrefix = textPayload(action, "tagMessagePrefix")
				}
				if _, ok := action.Payload["tagEnabled"]; ok {
					module.TagEnabled = boolPayload(action, "tagEnabled")
				}
				if raw, ok := action.Payload["module"]; ok {
					data, e := json.Marshal(raw)
					if e != nil {
						return c, e
					}
					if e = json.Unmarshal(data, &module); e != nil {
						return c, e
					}
				}
				if index < 0 {
					service.Modules = append(service.Modules, module)
				} else {
					service.Modules[index] = module
				}
				return c, nil
			}
		}
		return c, errors.New("服务不存在")
	})
	return ActionResult{Message: "模块配置已更新"}, err
}
func optional(text string) *string {
	if text == "" {
		return nil
	}
	return &text
}
