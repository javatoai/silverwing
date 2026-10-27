package application

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"silverwing-go/internal/domain"
)

func (a *App) saveMaterialsRoot(action Action) (ActionResult, error) {
	var root *string
	if input := strings.TrimSpace(action.Path); input != "" {
		path, err := filepath.Abs(input)
		if err != nil {
			return ActionResult{}, fmt.Errorf("规范任务资料根目录失败：%w", err)
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			return ActionResult{}, fmt.Errorf("创建任务资料根目录失败：%w", err)
		}
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
