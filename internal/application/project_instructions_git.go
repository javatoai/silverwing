package application

import (
	"context"
	"errors"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

type ProjectInstructionsGitBackend interface {
	ProjectInstructionsGitStatus(context.Context, workspace.MaterialsGitScope) (workspace.MaterialsGitSyncStatus, error)
	SyncProjectInstructionsGit(context.Context, workspace.MaterialsGitScope, bool, func(string)) (workspace.MaterialsGitSyncStatus, error)
}

func ProjectInstructionsGitScope(home string, config domain.AppConfig) workspace.MaterialsGitScope {
	settings := config.ProjectInstructionsGitSettings()
	return workspace.MaterialsGitScope{
		Home:           home,
		Root:           domain.ProjectInstructionsRepositoryRoot(home, settings),
		Subdirectory:   domain.ProjectInstructionsDirectory,
		RepositoryRole: domain.ProjectInstructionsRepositoryRole,
		URL:            settings.RepositoryURL,
		Branch:         settings.Branch,
		EnvironmentID:  documentsGitEnvironmentID(config.GitExecutablePath, settings.Enabled),
		RemoteName:     settings.RemoteName,
		BindingID:      settings.BindingID,
	}
}

func (a *App) projectInstructionsGitService(expected workspace.MaterialsGitScope) (*workspace.MaterialsGitSyncService, error) {
	config, err := a.Config.Load()
	if err != nil {
		return nil, err
	}
	resolve := func(config domain.AppConfig) workspace.MaterialsGitScope {
		return ProjectInstructionsGitScope(a.Paths.Home, config)
	}
	if resolve(config) != expected {
		return nil, workspace.ErrMaterialsGitScopeChanged
	}
	if err := validateDocumentsGitScopeURL(expected); err != nil {
		return nil, err
	}
	return a.configuredDocumentsGitService(config, resolve, expected), nil
}

func (a *App) ProjectInstructionsGitStatus(ctx context.Context, expected workspace.MaterialsGitScope) (workspace.MaterialsGitSyncStatus, error) {
	if err := ctx.Err(); err != nil {
		return workspace.MaterialsGitSyncStatus{}, err
	}
	service, err := a.projectInstructionsGitService(expected)
	if err != nil || expected.URL == "" {
		return workspace.MaterialsGitSyncStatus{}, err
	}
	return service.Status(expected)
}

func (a *App) SyncProjectInstructionsGit(ctx context.Context, expected workspace.MaterialsGitScope, resume bool, progress func(string)) (workspace.MaterialsGitSyncStatus, error) {
	if err := ctx.Err(); err != nil {
		return workspace.MaterialsGitSyncStatus{}, err
	}
	if !a.materialsGitGate.TryLock() {
		return workspace.MaterialsGitSyncStatus{}, errors.New("资料保存或 Git 同步正在执行，请稍后重试")
	}
	defer a.materialsGitGate.Unlock()
	service, err := a.projectInstructionsGitService(expected)
	if err != nil {
		return workspace.MaterialsGitSyncStatus{}, err
	}
	if expected.URL == "" {
		return workspace.MaterialsGitSyncStatus{}, errors.New("请先保存项目资料 Git 仓库地址")
	}
	config, err := a.Config.Load()
	if err != nil {
		return workspace.MaterialsGitSyncStatus{}, err
	}
	status, syncErr := service.Sync(ctx, expected, resume, progress)
	if storage.SharedDocumentsCheckout(a.Paths.Home, config) {
		return a.finishMaterialsGitSync(ctx, MaterialsGitScope(a.Paths.Home, config), status, syncErr, service.Check)
	}
	return status, syncErr
}
