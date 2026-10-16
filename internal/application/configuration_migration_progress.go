package application

import (
	"context"
	"errors"

	"silverwing-go/internal/workspace"
)

// TaskRootMigrationDispatcher is an optional desktop contract for the existing
// confirmed migration action. Progress never bypasses preview validation.
type TaskRootMigrationDispatcher interface {
	DispatchTaskRootMigration(context.Context, Action, func(workspace.TaskRootMigrationProgress)) (ActionResult, error)
}

func (a *App) DispatchTaskRootMigration(ctx context.Context, action Action, onProgress func(workspace.TaskRootMigrationProgress)) (ActionResult, error) {
	if ctx == nil {
		return ActionResult{}, errors.New("请求上下文不能为空")
	}
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	if action.Kind != "migration.apply" {
		return ActionResult{}, errors.New("迁移进度入口仅支持已确认的任务根目录迁移")
	}
	var forward func(workspace.TaskRootMigrationProgress)
	if onProgress != nil {
		forward = func(progress workspace.TaskRootMigrationProgress) {
			if ctx.Err() == nil {
				onProgress(progress)
			}
		}
	}
	return a.configurationActionWithProgress(ctx, action, forward)
}
