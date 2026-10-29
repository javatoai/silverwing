package application

import (
	"context"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

// TaskTarget holds task identity plus the private, transient menu guard. The
// guard is copied from the compatibility adapter and cannot be edited by UI
// consumers. It is revalidated after acquiring the task lock.
type TaskTarget struct {
	TaskID    string
	Path      string
	serviceID string
	moduleID  string
	guard     map[string]any
}

func NewTaskTarget(taskID, path string) TaskTarget {
	return TaskTarget{TaskID: taskID, Path: path}
}

func TaskTargetFromAction(action Action) TaskTarget {
	target := TaskTarget{TaskID: action.TaskID, Path: action.Path, serviceID: action.ServiceID, moduleID: action.ModuleID}
	for _, key := range []string{desktopTaskContextPayloadKey, "taskDeleteCreatedAt"} {
		if raw, present := action.Payload[key]; present {
			if target.guard == nil {
				target.guard = make(map[string]any)
			}
			if fields, ok := raw.(map[string]any); ok {
				owned := make(map[string]any, len(fields))
				for name, value := range fields {
					owned[name] = value
				}
				raw = owned
			}
			target.guard[key] = raw
		}
	}
	return target
}

func (target TaskTarget) action(kind string, payload map[string]any) Action {
	owned := make(map[string]any, len(payload)+len(target.guard))
	for key, value := range payload {
		owned[key] = value
	}
	for key, value := range target.guard {
		owned[key] = value
	}
	return Action{Kind: kind, TaskID: target.TaskID, Path: target.Path, ServiceID: target.serviceID, ModuleID: target.moduleID, Payload: owned}
}

type TaskCreateRequest struct {
	workspace.CreateTaskRequest
	// Nil uses configured defaults; a pointer to an empty slice disables tools.
	WorkspaceToolIDs *[]string
}

type TaskArchiveRequest struct {
	Target  TaskTarget
	Cleanup bool
}
type TaskDeleteRequest struct {
	Target       TaskTarget
	ForceDiscard bool
}
type TaskToolsRequest struct {
	Target  TaskTarget
	ToolIDs []string
}
type TaskToolRetryRequest struct {
	Target TaskTarget
	ToolID string
}

// These are consumer capabilities, rather than an interface for every service.
type TaskCreator interface {
	CreateTask(context.Context, TaskCreateRequest, func(workspace.TaskCreationProgress)) (OperationResult[domain.TaskManifest], error)
}
type TaskLifecycle interface {
	ArchiveTask(context.Context, TaskArchiveRequest) (OperationResult[domain.TaskManifest], error)
	RestoreTask(context.Context, TaskTarget) (OperationResult[domain.TaskManifest], error)
	DeleteTask(context.Context, TaskDeleteRequest) (OperationResult[struct{}], error)
}
type TaskMaterials interface {
	RetryTaskMaterials(context.Context, TaskTarget) (OperationResult[domain.TaskManifest], error)
}
type TaskTools interface {
	LaunchTaskTools(context.Context, TaskToolsRequest) (OperationResult[domain.TaskManifest], error)
	RetryTaskTool(context.Context, TaskToolRetryRequest) (OperationResult[domain.TaskManifest], error)
}

func TaskArchiveRequestFromAction(action Action) TaskArchiveRequest {
	return TaskArchiveRequest{Target: TaskTargetFromAction(action), Cleanup: boolPayload(action, "cleanup")}
}
func TaskDeleteRequestFromAction(action Action) TaskDeleteRequest {
	return TaskDeleteRequest{Target: TaskTargetFromAction(action), ForceDiscard: boolPayload(action, "forceDiscard")}
}
func TaskToolsRequestFromAction(action Action) (TaskToolsRequest, error) {
	request := TaskToolsRequest{Target: TaskTargetFromAction(action)}
	err := decodeField(action, "toolIds", &request.ToolIDs)
	return request, err
}
func TaskToolRetryRequestFromAction(action Action) TaskToolRetryRequest {
	return TaskToolRetryRequest{Target: TaskTargetFromAction(action), ToolID: textPayload(action, "toolId")}
}

var _ TaskCreator = (*App)(nil)
var _ TaskLifecycle = (*App)(nil)
var _ TaskMaterials = (*App)(nil)
var _ TaskTools = (*App)(nil)
