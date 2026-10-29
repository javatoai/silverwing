package application

import (
	"context"
	"fmt"
	"path/filepath"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/productprofile"
	"silverwing-go/internal/workspace"
)

func (a *App) workspaceAction(ctx context.Context, action Action, output func(process.StreamEvent)) (ActionResult, error) {
	return a.workspaceActionWithProgress(ctx, action, output, nil)
}
func (a *App) workspaceActionWithProgress(ctx context.Context, action Action, _ func(process.StreamEvent), progress func(workspace.TaskCreationProgress)) (actionResult ActionResult, actionErr error) {
	if result, handled, err := a.dispatchTypedTaskAction(ctx, action, progress); handled {
		return result, err
	}
	config, err := a.Config.Load()
	if err != nil {
		return ActionResult{}, err
	}
	service := a.taskService(config)
	defer func() { appendMaterialsCatalogWarnings(&actionResult, service.Documents) }()
	if result, handled, e := taskCreationPreviewAction(ctx, action, config, service); handled {
		return result, e
	}
	manifest, path, err := a.taskInConfig(action, config)
	if err != nil {
		return ActionResult{}, err
	}
	if err := a.validateDesktopTaskContext(action, config, manifest, path); err != nil {
		return ActionResult{}, err
	}
	return ActionResult{}, fmt.Errorf("%s不支持任务操作：%s", productprofile.ActionEditionLabel, action.Kind)
}
func taskCreatedPath(config domain.AppConfig, m domain.TaskManifest) string {
	return filepath.Join(value(config.TaskRoot), m.TaskDirectoryName)
}
