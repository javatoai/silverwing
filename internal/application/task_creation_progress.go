package application

import (
	"context"
	"errors"

	"silverwing-go/internal/workspace"
)

// TaskCreationProgressBackend is an optional capability for callers that need
// milestones from the existing synchronous task.create workflow, including
// its branch-reuse preflight.
type TaskCreationProgressBackend interface {
	DispatchTaskCreation(context.Context, Action, func(workspace.TaskCreationProgress)) (ActionResult, error)
}

var _ TaskCreationProgressBackend = (*App)(nil)

func (a *App) DispatchTaskCreation(ctx context.Context, action Action, onProgress func(workspace.TaskCreationProgress)) (ActionResult, error) {
	if action.Kind != "task.create" && action.Kind != "task.create-preview" {
		return ActionResult{}, errors.New("任务创建进度入口仅支持 task.create 和 task.create-preview")
	}
	if ctx == nil {
		return ActionResult{}, errors.New("请求上下文不能为空")
	}
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	return a.workspaceActionWithProgress(ctx, action, nil, onProgress)
}
