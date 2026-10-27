package application

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/workspace"
)

// refreshTasks is the explicit Source refresh: validate catalog repositories,
// update their metadata, then regenerate task Agent documents before reloading.
// Ordinary Snapshot reads do not perform this work.
func (a *App) refreshTasks(ctx context.Context) (actionResult ActionResult, actionErr error) {
	if ctx == nil {
		ctx = context.Background()
	}
	config, err := a.Config.Load()
	if err != nil {
		return ActionResult{}, err
	}
	inspector := workspace.NewRepositoryInspector(a.git(config))
	repositories, err := refreshTaskRepositories(ctx, config.Repositories, func(path string) (domain.RepositoryConfig, error) {
		return inspector.InspectContext(ctx, path)
	})
	if err != nil {
		return ActionResult{}, err
	}
	refreshed, err := a.Config.Update(func(current domain.AppConfig) (domain.AppConfig, error) {
		if err := ctx.Err(); err != nil {
			return current, err
		}
		return applyRefreshedTaskRepositories(current, config.Repositories, repositories)
	})
	if err != nil {
		return ActionResult{}, err
	}
	if refreshed.TaskRoot != nil {
		scan, err := a.Manifests.Scan(*refreshed.TaskRoot)
		if err != nil {
			return ActionResult{}, err
		}
		service := a.taskService(refreshed)
		defer func() { appendMaterialsCatalogWarnings(&actionResult, service.Documents) }()
		for _, entry := range scan.Current {
			if err := ctx.Err(); err != nil {
				return ActionResult{}, err
			}
			if filepath.Base(entry.Path) != entry.Manifest.TaskDirectoryName {
				return ActionResult{}, fmt.Errorf("任务清单的目录标识与实际路径不一致：%s", entry.Path)
			}
			if _, err := service.RefreshAgents(refreshed, entry.Path); err != nil {
				return ActionResult{}, err
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	snapshot, err := a.Snapshot(ctx)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ActionResult{}, ctxErr
	}
	return ActionResult{Message: "状态刷新完成", Data: snapshot}, err
}

func refreshTaskRepositories(ctx context.Context, repositories []domain.RepositoryConfig, inspect func(string) (domain.RepositoryConfig, error)) ([]domain.RepositoryConfig, error) {
	refreshed := make([]domain.RepositoryConfig, 0, len(repositories))
	for _, current := range repositories {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		actual, err := inspect(current.RootPath)
		if err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !sameTaskRefreshPath(current.GitCommonDirectory, actual.GitCommonDirectory) {
			return nil, fmt.Errorf("仓库身份已变化：%s 当前指向 %s，与配置中的 %s 不一致", current.Name, actual.GitCommonDirectory, current.GitCommonDirectory)
		}
		actual.ID = current.ID
		refreshed = append(refreshed, actual)
	}
	return refreshed, ctx.Err()
}

func applyRefreshedTaskRepositories(current domain.AppConfig, expected, refreshed []domain.RepositoryConfig) (domain.AppConfig, error) {
	before := make(map[string]domain.RepositoryConfig, len(expected))
	after := make(map[string]domain.RepositoryConfig, len(refreshed))
	for _, item := range expected {
		before[item.ID] = item
	}
	for _, item := range refreshed {
		after[item.ID] = item
	}
	if len(current.Repositories) != len(before) || len(after) != len(before) {
		return current, errors.New("刷新期间仓库配置已经变化，请重新刷新")
	}
	updated := make([]domain.RepositoryConfig, 0, len(current.Repositories))
	for _, item := range current.Repositories {
		previous, ok := before[item.ID]
		metadata, inspected := after[item.ID]
		if !ok || !inspected || !sameTaskRefreshPath(previous.RootPath, item.RootPath) || !sameTaskRefreshPath(previous.GitCommonDirectory, item.GitCommonDirectory) {
			return current, errors.New("刷新期间仓库配置已经变化，请重新刷新")
		}
		updated = append(updated, metadata)
	}
	current.Repositories = updated
	return current, nil
}

func sameTaskRefreshPath(expected, actual string) bool {
	left, leftErr := filepath.Abs(expected)
	right, rightErr := filepath.Abs(actual)
	if leftErr != nil || rightErr != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return filepath.Clean(left) == filepath.Clean(right)
}
