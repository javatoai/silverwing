package application

import (
	"errors"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

func workspaceHistoryBaseline(config domain.AppConfig, task domain.TaskManifest, item domain.ServiceWorkspace, action Action) (string, error) {
	if boolPayload(action, "allHistory") {
		return "", nil
	}
	branch, found, err := workspace.ResolveWorkspaceDiffMainBranch(config, task, item)
	if err != nil {
		return "", err
	}
	if !found {
		return "", errors.New("当前仓库或模块尚未配置主分支")
	}
	return branch.LocalRef, nil
}
