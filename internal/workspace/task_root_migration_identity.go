package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/productprofile"
)

func migrationWorkspaceIdentities(task migrationJournalTask) ([]string, error) {
	if !productprofile.AcceptsEdition(task.OriginalManifest.Edition) {
		return nil, fmt.Errorf("%s", productprofile.MigrationEditionError())
	}
	return nil, nil
}

func (s *TaskRootMigrationService) recoveryRepositoryLocks(j migrationJournal) ([]string, error) {
	return s.recoveryRepositoryLocksForState(j, j.Phase == "CONFIG_UPDATED")
}

func (s *TaskRootMigrationService) recoveryRepositoryLocksForState(j migrationJournal, _ bool) ([]string, error) {
	for _, task := range j.Tasks {
		if _, err := migrationWorkspaceIdentities(task); err != nil {
			return nil, err
		}
	}
	return nil, nil
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

	expected.UpdatedAt = manifest.UpdatedAt
	expected.LifecycleStatus = manifest.LifecycleStatus
	if !reflect.DeepEqual(domain.NormalizeTaskManifest(expected), domain.NormalizeTaskManifest(manifest)) {
		return fmt.Errorf("迁移目标任务与日志不一致，旧目录已保留：%s", task.TargetDirectory)
	}
	return nil
}
