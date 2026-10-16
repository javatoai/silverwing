package application

import (
	"context"
	"errors"

	"silverwing-go/internal/workspace"
)

// WorkspaceDiffPrefetchRequest is an internal read hint for one immutable
// comparison session. Candidates retain their full path/old-path/kind identity.
type WorkspaceDiffPrefetchRequest struct {
	TaskID, ServiceID, ModuleID, Path string
	SessionID                         string
	Compare                           workspace.CompareRequest
	Current                           workspace.CompareChange
	Candidates                        []workspace.CompareChange
}

type WorkspaceDiffPrefetchBackend interface {
	PrefetchWorkspaceDiff(context.Context, WorkspaceDiffPrefetchRequest) error
}

func (a *App) PrefetchWorkspaceDiff(ctx context.Context, request WorkspaceDiffPrefetchRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	config, err := a.Config.Load()
	if err != nil {
		return err
	}
	action := Action{TaskID: request.TaskID, ServiceID: request.ServiceID, ModuleID: request.ModuleID, Path: request.Path}
	manifest, taskPath, err := a.taskInConfig(action, config)
	if err != nil {
		return err
	}
	selected, err := selectedWorkspaces(manifest, action)
	if err != nil {
		return err
	}
	if len(selected) != 1 || request.SessionID == "" {
		return errors.New("Git 预读缺少唯一工作区或比较会话")
	}
	item := selected[0]
	if request.Path != "" && !sameTaskRefreshPath(request.Path, item.WorktreePath) {
		return errors.New("工作区路径已变化，请重新读取差异")
	}
	if request.Compare.Mode == "" {
		request.Compare.Mode = workspace.CompareMainline
	}
	mainBranch := workspace.WorkspaceMainBranch{}
	if request.Compare.Mode == workspace.CompareMainline || request.Compare.Mode == workspace.CompareCommits && request.Compare.SimulateOnMainline {
		var found bool
		mainBranch, found, err = workspace.ResolveWorkspaceDiffMainBranch(config, manifest, item)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("任务配置中没有可用的模块或服务主分支")
		}
	}
	owner, identity, err := workspaceGitDiffSessionIdentity(request.TaskID, taskPath, manifest, config)
	if err != nil {
		return err
	}
	service := a.taskService(config)
	diff := workspace.NewWorkspaceGitDiffService(service.Git, service.Locks)
	return diff.PrefetchSessionContext(ctx, a.workspaceGitDiffSessions(), request.SessionID, owner, identity, item, request.Compare, request.Current, request.Candidates, mainBranch)
}
