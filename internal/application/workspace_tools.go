package application

import (
	"context"

	"net/url"

	"path/filepath"

	"strings"

	"silverwing-go/internal/domain"

	"silverwing-go/internal/workspace"
)

type codexWorkspaceLauncher struct{}

func (codexWorkspaceLauncher) Descriptor() workspace.TaskWorkspaceToolDescriptor {
	return workspace.TaskWorkspaceToolDescriptor{ID: "codex", DisplayName: "Codex", Description: "任务创建完成后在 Codex 中打开任务目录"}
}
func (codexWorkspaceLauncher) Availability(ctx context.Context) workspace.TaskWorkspaceToolAvailability {
	return workspace.TaskWorkspaceToolAvailability{Available: true}
}
func (codexWorkspaceLauncher) Open(ctx context.Context, task workspace.TaskWorkspaceContext) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := filepath.Abs(task.TaskDirectory)
	if err != nil {
		return err
	}
	encoded := strings.ReplaceAll(url.QueryEscape(path), "+", "%20")
	return openNative("codex://threads/new?path="+encoded, false)
}

func (a *App) taskService(config domain.AppConfig) *workspace.TaskApplicationService {
	service := workspace.NewTaskApplicationService(a.Manifests, a.git(config), a.Paths)
	registry, _ := workspace.NewTaskWorkspaceToolRegistry([]workspace.TaskWorkspaceToolLauncher{codexWorkspaceLauncher{}})
	service.WorkspaceTools.Registry = registry
	return service
}
