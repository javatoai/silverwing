package application

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"silverwing-go/internal/domain"
)

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
		if err := a.validateDocumentsGitSettingsEdit(current, config); err != nil {
			return current, err
		}
		if value(current.TaskRoot) != value(config.TaskRoot) {
			if a.Paths.ProfileID != "" {
				return current, errors.New("请在任务与工作区设置中切换任务目录")
			}
			return current, errors.New("任务根目录变更需要先选择迁移或直接切换")
		}
		if err := a.validateConfigurationRemovals(current, config); err != nil {
			return current, err
		}
		// Keep legacy shard values for older readers. Whole-config saves may
		// contain these keys, but the embedded clients have no executable settings.
		config.GenbuExecutablePath, config.GenbuExecutableAutoDetected = current.GenbuExecutablePath, current.GenbuExecutableAutoDetected
		config.DragonExecutablePath, config.DragonExecutableAutoDetected = current.DragonExecutablePath, current.DragonExecutableAutoDetected
		return config, nil
	})
	return ActionResult{Message: "设置已保存"}, err
}

func (a *App) validateConfigurationRemovals(before, after domain.AppConfig) error {
	retained := make(map[string]bool)
	for _, service := range after.ServiceCatalog() {
		retained[service.ID] = true
	}
	removed := make(map[string]bool)
	for _, service := range before.ServiceCatalog() {
		if !retained[service.ID] {
			removed[service.ID] = true
		}
	}
	return a.ensureServicesNotUsedByTasks(before, removed)
}

func (a *App) ensureServicesNotUsedByTasks(config domain.AppConfig, removed map[string]bool) error {
	if len(removed) == 0 || config.TaskRoot == nil {
		return nil
	}
	if a.Paths.ProfileID != "" {
		info, err := os.Stat(*config.TaskRoot)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("无法访问任务目录，请先恢复后再删除服务：%s", *config.TaskRoot)
		}
	}
	scan, err := a.Manifests.Scan(*config.TaskRoot)
	if err != nil {
		return fmt.Errorf("无法读取任务目录，请先修复后再删除服务：%w", err)
	}
	if len(scan.Failures) > 0 {
		names := make([]string, 0, len(scan.Failures))
		for path := range scan.Failures {
			names = append(names, filepath.Base(path))
		}
		sort.Strings(names)
		return fmt.Errorf("存在无法读取的任务清单，请先修复后再删除服务：%s", strings.Join(names, "、"))
	}
	for _, entry := range scan.Current {
		task := entry.Manifest
		for _, service := range task.Services {
			if removed[service.GroupServiceID] {
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
	_, err := a.Config.Update(func(config domain.AppConfig) (domain.AppConfig, error) {
		removed, err := config.Service(id)
		if err != nil {
			return config, err
		}
		if err := a.ensureServicesNotUsedByTasks(config, map[string]bool{id: true}); err != nil {
			return config, err
		}
		services := make([]domain.ServiceConfig, 0, len(config.ServiceCatalog())-1)
		keepRepository := false
		for _, service := range config.ServiceCatalog() {
			if service.ID == id {
				continue
			}
			services = append(services, service)
			keepRepository = keepRepository || service.RepositoryID == removed.RepositoryID
		}
		config.Services, config.Groups = services, nil
		if !keepRepository {
			repositories := make([]domain.RepositoryConfig, 0, len(config.Repositories))
			for _, repository := range config.Repositories {
				if repository.ID != removed.RepositoryID {
					repositories = append(repositories, repository)
				}
			}
			config.Repositories = repositories
		}
		return config, nil
	})
	return ActionResult{Message: "服务配置已移除"}, err
}
