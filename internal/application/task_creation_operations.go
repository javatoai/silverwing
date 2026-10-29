package application

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/productprofile"
	"silverwing-go/internal/workspace"
)

func TaskCreateRequestFromAction(action Action) (TaskCreateRequest, error) {
	var request TaskCreateRequest
	if action.ServiceID != "" || action.ModuleID != "" {
		return request, errors.New(productprofile.TaskKindLabel + "不接受服务或模块参数")
	}
	for _, key := range []string{"featureBranch", "branch", "serviceIds", "serviceSelections", "baseOverrides", "confirmedBranchReuseKeys"} {
		if _, present := action.Payload[key]; present {
			return request, fmt.Errorf("%s不接受代码工作区参数：%s", productprofile.TaskKindLabel, key)
		}
	}
	if err := decodePayload(action.Payload, &request.CreateTaskRequest); err != nil {
		return request, err
	}
	if request.FolderName == "" {
		request.FolderName = textPayload(action, "title")
	}
	if request.RequirementLink == "" {
		request.RequirementLink = textPayload(action, "requirementURL")
	}

	if func() bool { _, present := action.Payload["workspaceToolIds"]; return present }() {
		var ids []string
		if err := decodeField(action, "workspaceToolIds", &ids); err != nil {
			return request, err
		}
		request.WorkspaceToolIDs = &ids
	}
	return request, nil
}

// The use case accepts typed input directly; Action only decodes into it.
func (a *App) CreateTask(ctx context.Context, request TaskCreateRequest, progress func(workspace.TaskCreationProgress)) (result OperationResult[domain.TaskManifest], resultErr error) {
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
	ids := append([]string(nil), config.DefaultWorkspaceToolIDs...)
	if request.WorkspaceToolIDs != nil {
		ids = append([]string(nil), (*request.WorkspaceToolIDs)...)
	}
	for _, id := range ids {
		if id != "codex" {
			return result, errors.New(productprofile.ActionEditionLabel + "只支持在 Codex 中打开任务")
		}
	}
	progress = orderedTaskProgress(progress)
	manifest, err := service.CreateTaskWithProgress(ctx, config, request.CreateTaskRequest, progress)
	if err != nil {
		return result, err
	}
	path := filepath.Join(value(config.TaskRoot), manifest.TaskDirectoryName)
	result = OperationResult[domain.TaskManifest]{Message: "任务已创建", Path: path, Data: manifest}
	if len(ids) > 0 {
		updated, launchErr := service.LaunchWorkspaceTools(ctx, path, manifest, ids)
		result.Data = updated
		if launchErr == nil {
			launchErr = codexLaunchFailure(updated)
		}
		if launchErr != nil {
			result.Text = "任务已保存，打开 Codex 失败：" + launchErr.Error()
		}
	}
	return result, nil
}

func orderedTaskProgress(progress func(workspace.TaskCreationProgress)) func(workspace.TaskCreationProgress) {
	if progress == nil {
		return nil
	}
	var sequence uint64
	var mutex sync.Mutex
	return func(event workspace.TaskCreationProgress) {
		mutex.Lock()
		sequence++
		event.Sequence = sequence
		mutex.Unlock()
		progress(event)
	}
}
