package application

import (
	"context"
	"errors"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/productprofile"
	"silverwing-go/internal/workspace"
)

type taskManifestMutation func(context.Context, domain.AppConfig, *workspace.TaskApplicationService, domain.TaskManifest, string) (OperationResult[domain.TaskManifest], error)

func (a *App) mutateTask(ctx context.Context, target TaskTarget, kind string, payload map[string]any, mutate taskManifestMutation) (result OperationResult[domain.TaskManifest], resultErr error) {
	result.omitData = true
	if err := operationContextError(ctx); err != nil {
		return result, err
	}
	config, err := a.Config.Load()
	if err != nil {
		return result, err
	}
	service := a.taskService(config)
	defer func() { appendOperationCatalogWarnings(&result, service.Documents) }()
	action := target.action(kind, payload)
	manifest, path, err := a.taskInConfig(action, config)
	if err != nil {
		return result, err
	}
	if err = a.validateDesktopTaskContext(action, config, manifest, path); err != nil {
		return result, err
	}
	return mutate(a.desktopTaskMutationContext(ctx, action), config, service, manifest, path)
}

func appendOperationCatalogWarnings[T any](result *OperationResult[T], documents *workspace.AgentDocumentService) {
	legacy := result.ToActionResult()
	appendMaterialsCatalogWarnings(&legacy, documents)
	result.Message, result.Text, result.Path = legacy.Message, legacy.Text, legacy.Path
}

func (a *App) ArchiveTask(ctx context.Context, request TaskArchiveRequest) (OperationResult[domain.TaskManifest], error) {
	return a.mutateTask(ctx, request.Target, "task.archive", map[string]any{"cleanup": request.Cleanup}, func(ctx context.Context, config domain.AppConfig, service *workspace.TaskApplicationService, _ domain.TaskManifest, path string) (OperationResult[domain.TaskManifest], error) {
		manifest, err := service.Archive(ctx, config, path, false)
		return OperationResult[domain.TaskManifest]{Message: "任务已归档", Data: manifest}, err
	})
}
func (a *App) RestoreTask(ctx context.Context, target TaskTarget) (OperationResult[domain.TaskManifest], error) {
	return a.mutateTask(ctx, target, "task.restore", nil, func(ctx context.Context, config domain.AppConfig, service *workspace.TaskApplicationService, _ domain.TaskManifest, path string) (OperationResult[domain.TaskManifest], error) {
		manifest, err := service.Restore(ctx, config, path)
		return OperationResult[domain.TaskManifest]{Message: "任务已恢复", Data: manifest}, err
	})
}
func (a *App) DeleteTask(ctx context.Context, request TaskDeleteRequest) (OperationResult[struct{}], error) {
	result, err := a.mutateTask(ctx, request.Target, "task.delete", map[string]any{"forceDiscard": request.ForceDiscard}, func(ctx context.Context, config domain.AppConfig, service *workspace.TaskApplicationService, _ domain.TaskManifest, path string) (OperationResult[domain.TaskManifest], error) {
		return OperationResult[domain.TaskManifest]{Message: "任务已删除"}, service.Delete(ctx, config, path, false)
	})
	return OperationResult[struct{}]{Message: result.Message, Text: result.Text, Path: result.Path}, err
}
func (a *App) RetryTaskMaterials(ctx context.Context, target TaskTarget) (OperationResult[domain.TaskManifest], error) {
	return a.mutateTask(ctx, target, "task.materials-retry", nil, func(ctx context.Context, config domain.AppConfig, service *workspace.TaskApplicationService, _ domain.TaskManifest, path string) (OperationResult[domain.TaskManifest], error) {
		manifest, err := service.RetryRequirementMaterials(ctx, config, path)
		return OperationResult[domain.TaskManifest]{Message: "资料目录操作已返回", Data: manifest}, err
	})
}
func (a *App) LaunchTaskTools(ctx context.Context, request TaskToolsRequest) (OperationResult[domain.TaskManifest], error) {
	return a.mutateTask(ctx, request.Target, "task.tools-launch", map[string]any{"toolIds": request.ToolIDs}, func(ctx context.Context, _ domain.AppConfig, service *workspace.TaskApplicationService, manifest domain.TaskManifest, path string) (OperationResult[domain.TaskManifest], error) {
		for _, id := range request.ToolIDs {
			if id != "codex" {
				return OperationResult[domain.TaskManifest]{}, errors.New(productprofile.ActionEditionLabel + "只支持 Codex")
			}
		}
		updated, err := service.LaunchWorkspaceTools(ctx, path, manifest, request.ToolIDs)
		if err == nil {
			err = codexLaunchFailure(updated)
		}
		return OperationResult[domain.TaskManifest]{Message: "Codex 打开请求已返回", Data: updated}, err
	})
}
func (a *App) RetryTaskTool(ctx context.Context, request TaskToolRetryRequest) (OperationResult[domain.TaskManifest], error) {
	return a.mutateTask(ctx, request.Target, "task.tools-retry", map[string]any{"toolId": request.ToolID}, func(ctx context.Context, _ domain.AppConfig, service *workspace.TaskApplicationService, manifest domain.TaskManifest, path string) (OperationResult[domain.TaskManifest], error) {
		if request.ToolID != "codex" {
			return OperationResult[domain.TaskManifest]{}, errors.New(productprofile.ActionEditionLabel + "只支持 Codex")
		}
		updated, err := service.RetryWorkspaceTool(ctx, path, manifest, request.ToolID)
		if err == nil {
			err = codexLaunchFailure(updated)
		}
		return OperationResult[domain.TaskManifest]{Message: "Codex 打开请求已返回", Data: updated}, err
	})
}
