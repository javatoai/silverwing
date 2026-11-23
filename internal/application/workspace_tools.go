package application

import (
	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/requirementprovider"
	"silverwing-go/internal/workspace"
)

func (a *App) taskService(config domain.AppConfig) *workspace.TaskApplicationService {
	dependencies := workspace.ComposeTaskApplicationDependencies(a.Manifests, a.git(config), a.Paths, a.materialsPreparation())
	service := workspace.NewTaskApplicationServiceWithDependencies(dependencies)
	service.Lifecycle.Recycler = a.directoryRecycler
	service.ModuleRemoval.Recycler = a.directoryRecycler
	registry, _ := workspace.NewTaskWorkspaceToolRegistry([]workspace.TaskWorkspaceToolLauncher{platformCodexWorkspaceLauncher{service: a.Platform, forceNew: true}})
	service.WorkspaceTools.Registry = registry
	return service
}

// materialsPreparation is the application composition root shared by task,
// delivery, and document preparation flows.
func (a *App) materialsPreparation() *documents.PreparationService {
	return documents.NewPreparationService(a.Paths, requirementprovider.NewMeegle(a.Runner))
}
