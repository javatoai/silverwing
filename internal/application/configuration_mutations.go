package application

import (
	"encoding/json"
	"errors"
	"fmt"

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
	remaining := map[string]bool{}
	for _, group := range after.Groups {
		remaining[group.ID] = true
	}
	removed := map[string]bool{}
	for _, group := range before.Groups {
		if !remaining[group.ID] {
			removed[group.ID] = true
		}
	}
	if len(removed) == 0 || before.TaskRoot == nil {
		return nil
	}
	scan, err := a.Manifests.Scan(*before.TaskRoot)
	if err != nil {
		return err
	}
	if len(scan.Failures) > 0 || len(scan.UnsupportedPaths) > 0 {
		return fmt.Errorf("存在无法读取的任务清单，请先修复后再删除项目组")
	}
	for _, entry := range scan.Current {
		if removed[entry.Manifest.GroupID] {
			return fmt.Errorf("该项目组仍有任务，不能删除")
		}
	}
	return nil
}
