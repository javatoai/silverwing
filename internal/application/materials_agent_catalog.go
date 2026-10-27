package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/workspace"
)

// The caller owns the shared materials-root lock. This scans all manifests,
// including archived tasks, and never creates missing tasks or runs Git.
func (a *App) refreshMaterialsAgentCatalogs(ctx context.Context, expected workspace.MaterialsGitScope) error {
	config, err := a.Config.Load()
	if err != nil {
		return err
	}
	if MaterialsGitScope(a.Paths.Home, config) != expected {
		return workspace.ErrMaterialsGitScopeChanged
	}
	if config.TaskRoot == nil {
		return nil
	}
	scan, err := a.Manifests.Scan(*config.TaskRoot)
	if err != nil {
		return err
	}
	locks := workspace.NewOperationLocks(a.Paths.Locks)
	service := workspace.NewAgentDocumentService(a.Paths, a.Manifests)
	var failures []error
	failedPaths := make([]string, 0, len(scan.Failures))
	for path := range scan.Failures {
		failedPaths = append(failedPaths, path)
	}
	sort.Strings(failedPaths)
	for _, path := range failedPaths {
		failures = append(failures, fmt.Errorf("%s：任务记录读取失败，无法确认资料说明覆盖：%s", filepath.Base(path), scan.Failures[path]))
	}
	for _, path := range scan.UnsupportedPaths {
		failures = append(failures, fmt.Errorf("%s：任务记录版本不受支持，无法确认资料说明覆盖：%s", filepath.Base(path), scan.UnsupportedReasons[path]))
	}
	for _, entry := range scan.Current {
		scope, ok := documents.AnnualScope(value(entry.Manifest.RequirementMaterials.WriteRoot))
		if !ok || !sameTaskContextPath(scope.Base, expected.Root) || !strings.EqualFold(filepath.Base(scope.Owned), expected.Subdirectory) {
			continue
		}
		taskPath, original := entry.Path, entry.Manifest
		err = locks.WithTask(taskPath, func() error {
			check := func() error {
				if err := ctx.Err(); err != nil {
					return err
				}
				current, err := a.Config.Load()
				if err != nil {
					return err
				}
				if MaterialsGitScope(a.Paths.Home, current) != expected || value(current.TaskRoot) != value(config.TaskRoot) {
					return workspace.ErrMaterialsGitScopeChanged
				}
				fresh, err := a.Manifests.Load(taskPath)
				if err != nil {
					return err
				}
				if fresh.TaskDirectoryName != original.TaskDirectoryName || fresh.CreatedAt != original.CreatedAt || value(fresh.RequirementMaterials.WriteRoot) != value(original.RequirementMaterials.WriteRoot) || fresh.RequirementLink != original.RequirementLink {
					return errors.New("任务身份或资料关联已变化，旧说明更新已停止")
				}
				return validAnnualTaskMaterials(fresh, current)
			}
			if err := check(); err != nil {
				if os.IsNotExist(err) {
					return nil
				}
				return err
			}
			return service.RefreshMaterialsDirectories(taskPath, original, config, check)
		})
		if err != nil && !os.IsNotExist(err) {
			failures = append(failures, fmt.Errorf("%s：%w", filepath.Base(taskPath), err))
		}
	}
	return errors.Join(failures...)
}

func appendMaterialsCatalogWarnings(result *ActionResult, service *workspace.AgentDocumentService) {
	if warning := service.MaterialsDirectoryWarnings(); warning != nil {
		result.Message += "；警告：本地资料说明更新失败（刷新资料可单独重试，不重复 Git 同步）：" + warning.Error()
	}
}
