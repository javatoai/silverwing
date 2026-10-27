package application

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/workspace"
)

func (a *App) workspaceAction(ctx context.Context, action Action, output func(process.StreamEvent)) (ActionResult, error) {
	return a.workspaceActionWithProgress(ctx, action, output, nil)
}
func (a *App) workspaceActionWithProgress(ctx context.Context, action Action, _ func(process.StreamEvent), progress func(workspace.TaskCreationProgress)) (ActionResult, error) {
	config, err := a.Config.Load()
	if err != nil {
		return ActionResult{}, err
	}
	service := a.taskService(config)
	if result, handled, e := taskCreationPreviewAction(ctx, action, config, service); handled {
		return result, e
	}
	if action.Kind == "task.create" {
		if action.ServiceID != "" || action.ModuleID != "" {
			return ActionResult{}, errors.New("产品任务不接受服务或模块参数")
		}
		for _, key := range []string{"featureBranch", "branch", "serviceIds", "serviceSelections", "baseOverrides", "confirmedBranchReuseKeys"} {
			if _, ok := action.Payload[key]; ok {
				return ActionResult{}, fmt.Errorf("产品任务不接受代码工作区参数：%s", key)
			}
		}
		var request workspace.CreateTaskRequest
		if err := decodePayload(action.Payload, &request); err != nil {
			return ActionResult{}, err
		}
		if request.FolderName == "" {
			request.FolderName = textPayload(action, "title")
		}
		if request.RequirementLink == "" {
			request.RequirementLink = textPayload(action, "requirementURL")
		}
		var ids []string
		if _, ok := action.Payload["workspaceToolIds"]; ok {
			if err := decodeField(action, "workspaceToolIds", &ids); err != nil {
				return ActionResult{}, err
			}
		} else {
			ids = config.DefaultWorkspaceToolIDs
		}
		for _, id := range ids {
			if id != "codex" {
				return ActionResult{}, errors.New("产品版只支持在 Codex 中打开任务")
			}
		}
		manifest, err := service.CreateTaskWithProgress(ctx, config, request, progress)
		if err != nil {
			return ActionResult{}, err
		}
		path := taskCreatedPath(config, manifest)
		result := ActionResult{Message: "任务已创建", Path: path, Data: manifest}
		if len(ids) > 0 {
			updated, e := service.LaunchWorkspaceTools(ctx, path, manifest, ids)
			result.Data = updated
			if e == nil {
				e = codexLaunchFailure(updated)
			}
			if e != nil {
				result.Text = "任务已保存，打开 Codex 失败：" + e.Error()
			}
		}
		return result, nil
	}
	manifest, path, err := a.taskInConfig(action, config)
	if err != nil {
		return ActionResult{}, err
	}
	if err := a.validateDesktopTaskContext(action, config, manifest, path); err != nil {
		return ActionResult{}, err
	}
	ctx = a.desktopTaskMutationContext(ctx, action)
	switch action.Kind {
	case "task.tools-launch":
		var ids []string
		if err := decodeField(action, "toolIds", &ids); err != nil {
			return ActionResult{}, err
		}
		for _, id := range ids {
			if id != "codex" {
				return ActionResult{}, errors.New("产品版只支持 Codex")
			}
		}
		m, e := service.LaunchWorkspaceTools(ctx, path, manifest, ids)
		if e == nil {
			e = codexLaunchFailure(m)
		}
		return ActionResult{Message: "Codex 打开请求已返回", Data: m}, e
	case "task.tools-retry":
		if textPayload(action, "toolId") != "codex" {
			return ActionResult{}, errors.New("产品版只支持 Codex")
		}
		m, e := service.RetryWorkspaceTool(ctx, path, manifest, "codex")
		if e == nil {
			e = codexLaunchFailure(m)
		}
		return ActionResult{Message: "Codex 打开请求已返回", Data: m}, e
	case "task.materials-retry":
		m, e := service.RetryRequirementMaterials(ctx, config, path)
		return ActionResult{Message: "资料目录操作已返回", Data: m}, e
	case "task.archive":
		m, e := service.Archive(ctx, config, path, false)
		result := ActionResult{Message: "任务已归档", Data: m}
		appendMaterialsCatalogWarnings(&result, service.Documents)
		return result, e
	case "task.restore":
		m, e := service.Restore(ctx, config, path)
		result := ActionResult{Message: "任务已恢复", Data: m}
		appendMaterialsCatalogWarnings(&result, service.Documents)
		return result, e
	case "task.delete":
		return ActionResult{Message: "任务已删除"}, service.Delete(ctx, config, path, false)
	}
	return ActionResult{}, fmt.Errorf("产品版不支持任务操作：%s", action.Kind)
}
func taskCreatedPath(config domain.AppConfig, m domain.TaskManifest) string {
	return filepath.Join(value(config.TaskRoot), m.TaskDirectoryName)
}
