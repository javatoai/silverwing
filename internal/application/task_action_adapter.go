package application

import (
	"context"
	"silverwing-go/internal/workspace"
)

// dispatchTypedTaskAction is the JSON adapter shared by Dispatch and the old
// progress entry. Each supported action delegates to a typed use case.
func (a *App) dispatchTypedTaskAction(ctx context.Context, action Action, progress func(workspace.TaskCreationProgress)) (ActionResult, bool, error) {
	switch action.Kind {
	case "task.create":
		request, err := TaskCreateRequestFromAction(action)
		if err != nil {
			return ActionResult{}, true, err
		}
		result, err := a.CreateTask(ctx, request, progress)
		return result.ToActionResult(), true, err
	case "task.create-preview":
		request, err := TaskCreateRequestFromAction(action)
		if err != nil {
			return ActionResult{}, true, err
		}
		result, err := a.PreviewTaskCreation(ctx, request, progress)
		return result.ToActionResult(), true, err
	case "task.archive":
		result, err := a.ArchiveTask(ctx, TaskArchiveRequestFromAction(action))
		return result.ToActionResult(), true, err
	case "task.restore":
		result, err := a.RestoreTask(ctx, TaskTargetFromAction(action))
		return result.ToActionResult(), true, err
	case "task.delete":
		result, err := a.DeleteTask(ctx, TaskDeleteRequestFromAction(action))
		return result.ToActionResult(), true, err
	case "task.materials-retry":
		result, err := a.RetryTaskMaterials(ctx, TaskTargetFromAction(action))
		return result.ToActionResult(), true, err
	case "task.tools-launch":
		request, err := TaskToolsRequestFromAction(action)
		if err != nil {
			return ActionResult{}, true, err
		}
		result, err := a.LaunchTaskTools(ctx, request)
		return result.ToActionResult(), true, err
	case "task.tools-retry":
		result, err := a.RetryTaskTool(ctx, TaskToolRetryRequestFromAction(action))
		return result.ToActionResult(), true, err
	case "task.requirement-link":
		result, err := a.LinkTaskRequirement(ctx, TaskRequirementLinkRequestFromAction(action))
		return result.ToActionResult(), true, err
	}
	return ActionResult{}, false, nil
}
