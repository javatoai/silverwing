package application

import (
	"context"
	"errors"
	"fmt"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/systemtrash"
	"silverwing-go/internal/workspace"
)

type taskManifestMutation func(context.Context, domain.AppConfig, *workspace.TaskApplicationService, domain.TaskManifest, string) (OperationResult[domain.TaskManifest], error)

var TaskDeleteCleanupWarningMessage = "任务已移入" + systemtrash.Label() + "，部分工作区登记未清理"

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
		manifest, err := service.Archive(ctx, config, path, request.Cleanup)
		return OperationResult[domain.TaskManifest]{Message: "任务已归档", Data: manifest}, err
	})
}
func (a *App) RestoreTask(ctx context.Context, target TaskTarget) (OperationResult[domain.TaskManifest], error) {
	return a.mutateTask(ctx, target, "task.restore", nil, func(ctx context.Context, config domain.AppConfig, service *workspace.TaskApplicationService, _ domain.TaskManifest, path string) (OperationResult[domain.TaskManifest], error) {
		manifest, err := service.Restore(ctx, config, path)
		return OperationResult[domain.TaskManifest]{Message: "任务已恢复", Data: manifest}, err
	})
}

func (a *App) RestoreRecycledWorkspaces(ctx context.Context, target TaskTarget) (OperationResult[domain.TaskManifest], error) {
	return a.mutateTask(ctx, target, "task.restore-recycled-workspaces", nil, func(ctx context.Context, config domain.AppConfig, service *workspace.TaskApplicationService, _ domain.TaskManifest, path string) (OperationResult[domain.TaskManifest], error) {
		count, err := service.RestoreRecycledWorkspaces(ctx, config, path)
		if err != nil {
			if count > 0 {
				err = fmt.Errorf("已恢复 %d 个工作区，其他工作区仍待恢复：%w", count, err)
			}
			return OperationResult[domain.TaskManifest]{}, err
		}
		if count == 0 {
			return OperationResult[domain.TaskManifest]{Message: "未找到待恢复的工作区。请先将目录放回原路径。"}, nil
		}
		return OperationResult[domain.TaskManifest]{Message: fmt.Sprintf("已恢复 %d 个工作区", count)}, nil
	})
}

func (a *App) inspectTaskDeletion(ctx context.Context, action Action, config domain.AppConfig, service *workspace.TaskApplicationService, manifest domain.TaskManifest, path string) (workspace.TaskDeletePreview, error) {
	if err := a.validateDesktopTaskContext(action, config, manifest, path); err != nil {
		return workspace.TaskDeletePreview{}, err
	}
	preview, err := service.InspectDeletion(ctx, config, path)
	if _, scoped := action.Payload[desktopTaskContextPayloadKey]; !scoped || err != nil {
		return preview, err
	}
	current, err := a.Config.Load()
	if err != nil {
		return workspace.TaskDeletePreview{}, err
	}
	currentManifest, currentPath, err := a.taskInConfig(action, current)
	if err != nil {
		return workspace.TaskDeletePreview{}, err
	}
	if err := a.validateDesktopTaskContext(action, current, currentManifest, currentPath); err != nil {
		return workspace.TaskDeletePreview{}, err
	}
	return preview, ctx.Err()
}

func (a *App) DeleteTask(ctx context.Context, request TaskDeleteRequest) (OperationResult[struct{}], error) {
	payload := map[string]any{"forceDiscard": request.ForceDiscard, "forceGuard": request.ForceGuard, "deleteMaterials": request.DeleteMaterials, "materialsGuard": request.MaterialsGuard, "deleteGuard": request.Guard}
	result, err := a.mutateTask(ctx, request.Target, "task.delete", payload, func(ctx context.Context, config domain.AppConfig, service *workspace.TaskApplicationService, _ domain.TaskManifest, path string) (OperationResult[domain.TaskManifest], error) {
		err := service.DeleteWithOptions(ctx, config, path, workspace.TaskDeleteOptions{
			ForceDiscard: request.ForceDiscard, ForceGuard: request.ForceGuard,
			DeleteMaterials: request.DeleteMaterials, MaterialsGuard: request.MaterialsGuard, Guard: request.Guard,
		})
		result := OperationResult[domain.TaskManifest]{Message: "任务已移入" + systemtrash.Label()}
		var warning *workspace.TaskRecycleCleanupWarning
		if errors.As(err, &warning) {
			result.Text = warning.Error()
			result.Message = TaskDeleteCleanupWarningMessage
			return result, nil
		}
		return result, err
	})
	return OperationResult[struct{}]{Message: result.Message, Text: result.Text, Path: result.Path}, err
}
func (a *App) RetryTaskMaterials(ctx context.Context, target TaskTarget) (OperationResult[domain.TaskManifest], error) {
	return a.mutateTask(ctx, target, "task.materials-retry", nil, func(ctx context.Context, config domain.AppConfig, service *workspace.TaskApplicationService, _ domain.TaskManifest, path string) (OperationResult[domain.TaskManifest], error) {
		manifest, err := service.RetryRequirementMaterials(ctx, config, path)
		return OperationResult[domain.TaskManifest]{Message: "资料目录操作已返回，请查看状态", Data: manifest}, err
	})
}
func (a *App) LaunchTaskTools(ctx context.Context, request TaskToolsRequest) (OperationResult[domain.TaskManifest], error) {
	return a.mutateTask(ctx, request.Target, "task.tools-launch", map[string]any{"toolIds": request.ToolIDs}, func(ctx context.Context, _ domain.AppConfig, service *workspace.TaskApplicationService, manifest domain.TaskManifest, path string) (OperationResult[domain.TaskManifest], error) {
		updated, err := service.LaunchWorkspaceTools(ctx, path, manifest, request.ToolIDs)
		if err == nil {
			if failure := workspaceToolLaunchFailure(updated, request.ToolIDs); failure != "" {
				err = errors.New("任务工具打开失败：" + failure)
			}
		}
		return OperationResult[domain.TaskManifest]{Message: "工作区工具启动已返回", Data: updated}, err
	})
}
func (a *App) RetryTaskTool(ctx context.Context, request TaskToolRetryRequest) (OperationResult[domain.TaskManifest], error) {
	return a.mutateTask(ctx, request.Target, "task.tools-retry", map[string]any{"toolId": request.ToolID}, func(ctx context.Context, _ domain.AppConfig, service *workspace.TaskApplicationService, manifest domain.TaskManifest, path string) (OperationResult[domain.TaskManifest], error) {
		updated, err := service.RetryWorkspaceTool(ctx, path, manifest, request.ToolID)
		return OperationResult[domain.TaskManifest]{Message: "工作区工具重试已返回", Data: updated}, err
	})
}

type TaskRequirementLinkRequest struct {
	Target          TaskTarget
	RequirementLink string
}
type TaskRequirementLinker interface {
	LinkTaskRequirement(context.Context, TaskRequirementLinkRequest) (OperationResult[domain.TaskManifest], error)
}

var _ TaskRequirementLinker = (*App)(nil)

func (a *App) LinkTaskRequirement(ctx context.Context, request TaskRequirementLinkRequest) (OperationResult[domain.TaskManifest], error) {
	return a.mutateTask(ctx, request.Target, "task.requirement-link", map[string]any{"requirementLink": request.RequirementLink}, func(ctx context.Context, config domain.AppConfig, service *workspace.TaskApplicationService, _ domain.TaskManifest, path string) (OperationResult[domain.TaskManifest], error) {
		manifest, err := service.LinkRequirement(ctx, config, path, request.RequirementLink)
		return OperationResult[domain.TaskManifest]{Message: "需求已关联，本地资料保留在原位置", Data: manifest}, err
	})
}
func TaskRequirementLinkRequestFromAction(action Action) TaskRequirementLinkRequest {
	return TaskRequirementLinkRequest{Target: TaskTargetFromAction(action), RequirementLink: textPayload(action, "requirementLink")}
}
