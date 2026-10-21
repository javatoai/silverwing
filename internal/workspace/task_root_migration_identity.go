package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"silverwing-go/internal/domain"
)

// Go journals persist each workspace identity. Validate it against the saved
// manifest before repairing ownership or deleting a task directory.
func migrationWorkspaceIdentities(task migrationJournalTask) ([]domain.ServiceWorkspace, error) {
	if len(task.Workspaces) != len(task.OriginalManifest.Services) {
		return nil, fmt.Errorf("迁移日志工作区与原任务清单不一致：%s", task.SourceDirectory)
	}
	result := make([]domain.ServiceWorkspace, 0, len(task.Workspaces))
	for i, w := range task.Workspaces {
		path := filepath.Join(task.SourceDirectory, filepath.FromSlash(w.RelativePath))
		ws := task.OriginalManifest.Services[i]
		if ws.WorktreePath == "" || ws.RepositoryPath == "" || !samePath(ws.WorktreePath, path) ||
			ws.Strategy != w.Strategy || !samePath(ws.RepositoryPath, w.RepositoryPath) ||
			w.RepositoryID != ws.RepositoryID || w.GroupServiceID != ws.GroupServiceID || w.ModuleID != ws.ModuleID {
			return nil, fmt.Errorf("迁移日志工作区身份与原任务清单冲突：%s", path)
		}
		result = append(result, ws)
	}
	return result, nil
}

func (s *TaskRootMigrationService) recoveryRepositoryLocks(j migrationJournal) ([]string, error) {
	return s.recoveryRepositoryLocksForState(j, j.Phase == "CONFIG_UPDATED")
}

func (s *TaskRootMigrationService) recoveryRepositoryLocksForState(j migrationJournal, committed bool) ([]string, error) {
	locks := append([]string{}, j.RepositoryLocks...)
	for _, task := range j.Tasks {
		workspaces, err := migrationWorkspaceIdentities(task)
		if err != nil {
			return nil, err
		}
		for i, ws := range workspaces {
			path := ws.RepositoryPath
			if ws.Strategy == domain.WorkspaceIndependentClone {
				path = filepath.Join(task.SourceDirectory, filepath.FromSlash(task.Workspaces[i].RelativePath))
				// A committed cleanup can leave the source clone partially
				// deleted. Only the target remains a usable Git repository.
				if committed || !fileExists(path) {
					path = filepath.Join(task.TargetDirectory, filepath.FromSlash(task.Workspaces[i].RelativePath))
				}
				if err := migrationRealPath(path); err != nil {
					return nil, err
				}
				// A clone changes its common-directory path when moved. Lock both
				// names, including a name currently absent during rollback.
				locks = append(locks, filepath.Join(ws.WorktreePath, ".git"), filepath.Join(task.TargetDirectory, filepath.FromSlash(task.Workspaces[i].RelativePath), ".git"))
			}
			common, err := s.Git.CommonDirectory(path)
			if err != nil {
				return nil, fmt.Errorf("无法恢复迁移仓库锁：%s: %w", path, err)
			}
			locks = append(locks, common)
		}
	}
	return uniqueSorted(locks), nil
}

func migrationRealPath(path string) error {
	for path = mustAbs(path); ; path = filepath.Dir(path) {
		info, err := os.Lstat(path)
		if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err == nil {
			reparse, err := bootstrapPathIsReparsePoint(path, info)
			if err != nil {
				return err
			}
			if reparse {
				return fmt.Errorf("迁移恢复路径包含链接或重解析点，已保留目录：%s", path)
			}
		}
		if filepath.Dir(path) == path {
			return nil
		}
	}
}

// Verify the committed target before resuming a cleanup interrupted between
// source removal and marker removal.
func (s *TaskRootMigrationService) requireCommittedMigrationTarget(task migrationJournalTask, allMappings ...[]TaskRootMigrationPathMapping) error {
	if err := migrationRealPath(task.TargetDirectory); err != nil {
		return err
	}
	manifest, err := s.Manifests.Load(task.TargetDirectory)
	if err != nil {
		return fmt.Errorf("无法验证迁移目标任务，旧目录已保留：%w", err)
	}
	mappings := []TaskRootMigrationPathMapping{{SourceDirectory: task.SourceDirectory, TargetDirectory: task.TargetDirectory}}
	if len(allMappings) > 0 {
		mappings = allMappings[0]
	}
	expected, err := migratedTaskManifest(task.OriginalManifest, mappings)
	if err != nil {
		return err
	}
	for i := range expected.Services {
		ws := expected.Services[i]
		if ws.Strategy == domain.WorkspaceIndependentClone {
			if err := requireOwnedClone(ws.WorktreePath, cloneOwnership(task.TargetDirectory, ws.RepositoryID, ws.GroupServiceID, ws.ModuleID)); err != nil {
				return err
			}
		}
	}
	expected.UpdatedAt = manifest.UpdatedAt
	expected.LifecycleStatus = manifest.LifecycleStatus
	if !reflect.DeepEqual(domain.NormalizeTaskManifest(expected), domain.NormalizeTaskManifest(manifest)) {
		return fmt.Errorf("迁移目标任务与日志不一致，旧目录已保留：%s", task.TargetDirectory)
	}
	return nil
}

func restoreMigrationCloneOwners(task migrationJournalTask, location string) error {
	workspaces, err := migrationWorkspaceIdentities(task)
	if err != nil {
		return err
	}
	for i, ws := range workspaces {
		if ws.Strategy != domain.WorkspaceIndependentClone {
			continue
		}
		path := filepath.Join(location, filepath.FromSlash(task.Workspaces[i].RelativePath))
		if err := migrationRealPath(path); err != nil {
			return err
		}
		if err := changeCloneOwner(path, cloneOwnership(task.TargetDirectory, ws.RepositoryID, ws.GroupServiceID, ws.ModuleID),
			cloneOwnership(task.SourceDirectory, ws.RepositoryID, ws.GroupServiceID, ws.ModuleID)); err != nil {
			return err
		}
	}
	return nil
}
