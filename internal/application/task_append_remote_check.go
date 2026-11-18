package application

import (
	"context"
	"errors"

	"silverwing-go/internal/workspace"
)

// TaskAppendRemoteCheckBackend is a read-only native desktop capability.
type TaskAppendRemoteCheckBackend interface {
	CheckTaskAppendRemotes(context.Context, Action) ([]workspace.WorktreeRemoteCheck, error)
}

func (a *App) CheckTaskAppendRemotes(ctx context.Context, action Action) ([]workspace.WorktreeRemoteCheck, error) {
	if err := operationContextError(ctx); err != nil {
		return nil, err
	}
	if action.Kind != "task.services-add-preview" && action.Kind != "task.modules-add-preview" {
		return nil, errors.New("仅支持检查将要添加的 Worktree 远端")
	}
	request, err := addWorkspaceRequest(action)
	if err != nil {
		return nil, err
	}
	config, err := a.Config.Load()
	if err != nil {
		return nil, err
	}
	manifest, _, err := a.taskInConfig(action, config)
	if err != nil {
		return nil, err
	}
	return workspace.CheckAppendWorktreeRemotes(ctx, a.git(config), config, manifest, request)
}
