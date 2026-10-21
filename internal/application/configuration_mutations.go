package application

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"silverwing-go/internal/domain"
)

type configuredServiceID struct {
	groupID, serviceID string
}

func (a *App) saveConfiguration(action Action) (ActionResult, error) {
	input := actionText(action)
	if input == "" {
		input = textPayload(action, "configJSON")
	}
	var config domain.AppConfig
	if err := json.Unmarshal([]byte(input), &config); err != nil {
		return ActionResult{}, err
	}
	if err := config.Validate(); err != nil {
		return ActionResult{}, err
	}
	_, err := a.Config.Update(func(current domain.AppConfig) (domain.AppConfig, error) {
		if value(current.TaskRoot) != value(config.TaskRoot) {
			return current, errors.New("任务根目录变更需要先选择迁移或直接切换")
		}
		if err := a.validateConfigurationRemovals(current, config); err != nil {
			return current, err
		}
		return config, nil
	})
	return ActionResult{Message: "设置已保存"}, err
}

func (a *App) validateConfigurationRemovals(before, after domain.AppConfig) error {
	groups := make(map[string]domain.GroupConfig, len(after.Groups))
	for _, group := range after.Groups {
		groups[group.ID] = group
	}
	removedGroups := map[string]bool{}
	removedServices := map[configuredServiceID]bool{}
	for _, group := range before.Groups {
		next, exists := groups[group.ID]
		if !exists {
			if len(group.Services) != 0 {
				return errors.New("只能删除空组，请先移除组内服务")
			}
			removedGroups[group.ID] = true
			continue
		}
		services := make(map[string]bool, len(next.Services))
		for _, service := range next.Services {
			services[service.ID] = true
		}
		for _, service := range group.Services {
			if !services[service.ID] {
				removedServices[configuredServiceID{group.ID, service.ID}] = true
			}
		}
	}
	return a.ensureConfigurationNotUsedByTasks(before, removedGroups, removedServices)
}

func (a *App) ensureConfigurationNotUsedByTasks(config domain.AppConfig, groups map[string]bool, services map[configuredServiceID]bool) error {
	if len(groups)+len(services) == 0 || config.TaskRoot == nil {
		return nil
	}
	scan, err := a.Manifests.Scan(*config.TaskRoot)
	if err != nil {
		return fmt.Errorf("无法读取任务目录，请先修复后再删除组或服务：%w", err)
	}
	if len(scan.Failures) > 0 {
		names := make([]string, 0, len(scan.Failures))
		for path := range scan.Failures {
			names = append(names, filepath.Base(path))
		}
		sort.Strings(names)
		return fmt.Errorf("存在无法读取的任务清单，请先修复后再删除组或服务：%s", strings.Join(names, "、"))
	}
	for _, entry := range scan.Current {
		task := entry.Manifest
		if groups[task.GroupID] {
			return errors.New("该组仍有历史研发任务，不能删除")
		}
		for _, service := range task.Services {
			if services[configuredServiceID{task.GroupID, service.GroupServiceID}] {
				return errors.New("该服务仍被研发任务引用，不能移除")
			}
		}
	}
	return nil
}

func (a *App) removeConfiguredService(action Action) (ActionResult, error) {
	id := action.ServiceID
	if id == "" {
		id = textPayload(action, "serviceId")
	}
	groupID := textPayload(action, "groupId")
	if groupID == "" {
		groupID = domain.DefaultGroupID
	}
	_, err := a.Config.Update(func(config domain.AppConfig) (domain.AppConfig, error) {
		for gi := range config.Groups {
			if config.Groups[gi].ID != groupID {
				continue
			}
			for si, service := range config.Groups[gi].Services {
				if service.ID != id {
					continue
				}
				removed := map[configuredServiceID]bool{{groupID, id}: true}
				if err := a.ensureConfigurationNotUsedByTasks(config, nil, removed); err != nil {
					return config, err
				}
				config.Groups[gi].Services = append(config.Groups[gi].Services[:si], config.Groups[gi].Services[si+1:]...)
				for _, group := range config.Groups {
					for _, other := range group.Services {
						if other.RepositoryID == service.RepositoryID {
							return config, nil
						}
					}
				}
				repositories := make([]domain.RepositoryConfig, 0, len(config.Repositories))
				for _, repository := range config.Repositories {
					if repository.ID != service.RepositoryID {
						repositories = append(repositories, repository)
					}
				}
				config.Repositories = repositories
				return config, nil
			}
			return config, errors.New("服务不存在")
		}
		return config, errors.New("组不存在")
	})
	return ActionResult{Message: "服务配置已移除"}, err
}
