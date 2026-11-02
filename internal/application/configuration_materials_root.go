package application

import (
	"fmt"
	"strings"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
)

func (a *App) saveMaterialsRoot(action Action) (ActionResult, error) {
	var root *string
	if input := strings.TrimSpace(action.Path); input != "" {
		paths, err := documents.EnsureMaterialsPaths(input)
		if err != nil {
			return ActionResult{}, fmt.Errorf("准备任务资料目录失败：%w", err)
		}
		path := paths.Root
		root = &path
	}
	_, err := a.Config.Update(func(config domain.AppConfig) (domain.AppConfig, error) {
		if value(config.RequirementMaterialsRoot) != value(root) && config.MaterialsGitSync != nil {
			settings := *config.MaterialsGitSync
			settings.Enabled = false
			config.MaterialsGitSync = &settings
		}
		config.RequirementMaterialsRoot = root
		return config, nil
	})
	return ActionResult{Message: "任务资料根目录已保存", Path: value(root)}, err
}
