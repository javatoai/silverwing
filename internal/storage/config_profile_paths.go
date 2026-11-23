package storage

import (
	"fmt"
	"os"
	"path/filepath"

	"silverwing-go/internal/domain"
)

func (s *ConfigStore) initialConfig() domain.AppConfig {
	config := domain.DefaultAppConfig()
	if s.paths.ProfileID != "" {
		return s.runtimeConfig(config)
	}
	taskRoot := s.paths.Tasks
	config.TaskRoot = &taskRoot
	config.RequirementMaterialsRoot = defaultProfileMaterialsRoot(s.paths.Home)
	return config
}

func (s *ConfigStore) persistedShards(config domain.AppConfig) configShards {
	if s.paths.ProfileID != "" {
		config.RequirementMaterialsRoot = nil
	}
	return shardsFor(config)
}

func (s *ConfigStore) configFromShards(decoded decodedConfig) (domain.AppConfig, error) {
	config := decoded.Config
	if s.paths.ProfileID == "" {
		return config, nil
	}
	if err := s.validateProfileConfigPaths(config); err != nil {
		return domain.AppConfig{}, err
	}
	return s.runtimeConfig(config), nil
}

func (s *ConfigStore) validateProfileConfigPaths(config domain.AppConfig) error {
	if config.RequirementMaterialsRoot != nil {
		return fmt.Errorf("配置分片包含随数据目录解析的内部路径")
	}
	if config.TaskRoot != nil {
		root, err := validateTaskRootPath(s.paths.Home, *config.TaskRoot, true)
		if err != nil {
			return err
		}
		if root != *config.TaskRoot {
			return fmt.Errorf("配置中的任务存放目录必须是已规范化的绝对路径")
		}
	}
	if SharedDocumentsCheckout(s.paths.Home, config) {
		task, project := config.MaterialsGitSettings(), config.ProjectInstructionsGitSettings()
		if !domain.SameMaterialsGitRepositoryURL(task.RepositoryURL, project.RepositoryURL) || task.Branch != project.Branch {
			return fmt.Errorf("任务资料与项目资料共用本地仓库时，需使用同一 Git 地址和同步分支")
		}
	}
	return nil
}

// SharedDocumentsCheckout reports whether the two configured document roles use the same checkout.
func SharedDocumentsCheckout(home string, config domain.AppConfig) bool {
	task, project := config.MaterialsGitSettings(), config.ProjectInstructionsGitSettings()
	if task.RepositoryURL == "" || project.RepositoryURL == "" {
		return false
	}
	taskRoot := domain.TaskMaterialsRepositoryRoot(home, task)
	projectRoot := domain.ProjectInstructionsRepositoryRoot(home, project)
	return taskRoot != "" && projectRoot != "" && sameProfileDocumentsCheckout(taskRoot, projectRoot)
}

func sameProfileDocumentsCheckout(task, project string) bool {
	if sameDirectoryPath(task, project) {
		return true
	}
	taskInfo, taskErr := os.Stat(task)
	projectInfo, projectErr := os.Stat(project)
	return taskErr == nil && projectErr == nil && os.SameFile(taskInfo, projectInfo)
}

func (s *ConfigStore) runtimeConfig(config domain.AppConfig) domain.AppConfig {
	if s.paths.ProfileID == "" {
		return config
	}
	materials := domain.TaskMaterialsRepositoryRoot(s.paths.Home, config.MaterialsGitSettings())
	if materials == "" {
		materials = filepath.Join(s.paths.Home, "materials")
	}
	config.RequirementMaterialsRoot = &materials
	return config
}
