package application

import (
	"context"
	"path/filepath"
	"sync"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func TaskCreateRequestFromAction(action Action) (TaskCreateRequest, error) {
	var request TaskCreateRequest
	if err := decodePayload(action.Payload, &request.CreateTaskRequest); err != nil {
		return request, err
	}
	if request.FolderName == "" {
		request.FolderName = textPayload(action, "title")
	}
	if request.RequirementLink == "" {
		request.RequirementLink = textPayload(action, "requirementURL")
	}
	if request.FeatureBranch == "" {
		request.FeatureBranch = textPayload(action, "branch")
	}
	if request.GroupID == "" {
		request.GroupID = domain.DefaultGroupID
	}
	if action.Payload["workspaceToolIds"] != nil {
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
	progress = orderedTaskProgress(progress)
	manifest, err := service.CreateTaskWithProgress(ctx, config, request.CreateTaskRequest, progress)
	if err != nil {
		return result, err
	}
	path := filepath.Join(value(config.TaskRoot), manifest.TaskDirectoryName)
	return operationResultFromAction[domain.TaskManifest](finishCreatedTask(ctx, service, path, manifest, ids, progress))
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

// Branch reuse is a developer capability and is not present in QA/Product.
type TaskCreationPreviewer interface {
	PreviewTaskCreation(context.Context, TaskCreateRequest, func(workspace.TaskCreationProgress)) (OperationResult[[]workspace.BranchReuseConflict], error)
}

var _ TaskCreationPreviewer = (*App)(nil)

func (a *App) PreviewTaskCreation(ctx context.Context, request TaskCreateRequest, progress func(workspace.TaskCreationProgress)) (result OperationResult[[]workspace.BranchReuseConflict], resultErr error) {
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
	conflicts, err := service.InspectCreateBranchReuseWithProgress(ctx, config, request.CreateTaskRequest, orderedTaskProgress(progress))
	return OperationResult[[]workspace.BranchReuseConflict]{Message: "任务创建预检已完成", Data: conflicts}, err
}
