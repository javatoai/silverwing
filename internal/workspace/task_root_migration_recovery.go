package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"

	"silverwing-go/internal/domain"
)

func (s *TaskRootMigrationService) RecoverInterruptedMigration(config domain.AppConfig) ([]string, error) {
	journal, err := s.readJournal()
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	if err := validateMigrationJournal(journal); err != nil {
		return nil, err
	}
	target := mustAbs(journal.TargetRoot)
	committed, err := migrationConfigCommitted(config, journal)
	if err != nil {
		return nil, err
	}
	repositoryLocks, err := s.recoveryRepositoryLocksForState(journal, committed)
	if err != nil {
		return nil, err
	}
	var failures []string
	lockPlan := migrationPlan{Preview: TaskRootMigrationPreview{SourceRoot: mustAbs(journal.SourceRoot), TargetRoot: target}}
	for _, task := range journal.Tasks {
		lockPlan.Tasks = append(lockPlan.Tasks, migrationTask{Source: task.SourceDirectory, Target: task.TargetDirectory})
	}
	lockPlan.RepositoryLocks = repositoryLocks
	err = s.withMigrationLocks(lockPlan, func() error {
		current, err := s.ConfigStore.Load()
		if err != nil {
			return err
		}
		committed, err = migrationConfigCommitted(current, journal)
		if err != nil {
			return err
		}
		if committed {
			failures = s.cleanupMigrationSources(journal)
		} else {
			errs := s.rollbackMigration(journal, current)
			for _, e := range errs {
				failures = append(failures, e.Error())
			}
		}
		if len(failures) == 0 {
			return os.Remove(s.journalPath())
		}
		return nil
	})
	return failures, err
}

func (s *TaskRootMigrationService) rollbackMigration(j migrationJournal, config domain.AppConfig) []error {
	if err := s.recoverMigrationDrafts(j, true); err != nil {
		return []error{err}
	}
	failures := []error{}
	for i := len(j.Tasks) - 1; i >= 0; i-- {
		jt := j.Tasks[i]
		src, dst := mustAbs(jt.SourceDirectory), mustAbs(jt.TargetDirectory)
		if err := migrationRealPath(src); err != nil {
			failures = append(failures, err)
			continue
		}
		if err := migrationRealPath(dst); err != nil {
			failures = append(failures, err)
			continue
		}
		if !fileExists(src) && !fileExists(dst) {
			failures = append(failures, fmt.Errorf("迁移源与目标任务均不存在：%s", src))
			continue
		}
		if fileExists(dst) {
			if err := requireMigrationOwnedTarget(dst, j.ID); err != nil {
				failures = append(failures, err)
				continue
			}
			if fileExists(src) {

				if err := removeTree(dst); err != nil {
					failures = append(failures, err)
					continue
				}
			} else {

				if err := os.Rename(dst, src); err != nil {
					failures = append(failures, err)
					continue
				}
			}
		}

		if err := s.Manifests.Save(src, jt.OriginalManifest); err != nil {
			failures = append(failures, err)
			continue
		}
		if _, err := s.Documents.WriteTaskDocument(src, jt.OriginalManifest, config); err != nil {
			failures = append(failures, err)
			continue
		}

		marker := filepath.Join(src, taskRootMigrationMarker)
		if data, err := os.ReadFile(marker); err == nil {
			if string(data) != j.ID {
				failures = append(failures, fmt.Errorf("源任务迁移标记与日志不符：%s", src))
				continue
			}
			if err := os.Remove(marker); err != nil {
				failures = append(failures, err)
			}
		} else if !os.IsNotExist(err) {
			failures = append(failures, err)
		}
	}
	return failures
}

func (s *TaskRootMigrationService) cleanupMigrationSources(j migrationJournal) []string {
	failures := []string{}
	mappings := journalMigrationPathMappings(j)
	// Validate every target with the full shared-materials mapping before any
	// source deletion. A changed or missing target retains the whole checkpoint.
	for _, task := range j.Tasks {
		if err := s.requireCommittedMigrationTarget(task, mappings); err != nil {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) > 0 {
		return failures
	}
	if err := s.recoverMigrationDrafts(j, false); err != nil {
		return []string{err.Error()}
	}
	for _, task := range j.Tasks {
		dst, src := mustAbs(task.TargetDirectory), mustAbs(task.SourceDirectory)
		if err := migrationRealPath(src); err != nil {
			failures = append(failures, err.Error())
			continue
		}
		marker := filepath.Join(dst, taskRootMigrationMarker)
		if data, err := os.ReadFile(marker); err == nil {
			if string(data) != j.ID {
				failures = append(failures, "目标目录迁移标记与日志不符："+dst)
				continue
			}
		} else if !os.IsNotExist(err) {
			failures = append(failures, err.Error())
			continue
		}
		if fileExists(src) {
			original, err := s.Manifests.Load(src)
			var removalErr error
			if err != nil {
				// A directory handle may have blocked only the final rmdir after
				// every file, including the manifest, was already removed. An
				// empty residue needs no recursive deletion or identity repair.
				entries, readErr := os.ReadDir(src)
				if readErr != nil || len(entries) != 0 {
					failures = append(failures, "源任务已变化或无法读取，旧目录已保留："+src)
					continue
				}
				removalErr = os.Remove(src)
			} else if !reflect.DeepEqual(domain.NormalizeTaskManifest(original), domain.NormalizeTaskManifest(task.OriginalManifest)) {
				failures = append(failures, "源任务已变化或无法读取，旧目录已保留："+src)
				continue
			} else {
				removalErr = removeTree(src)
			}
			if removalErr != nil {
				failures = append(failures, removalErr.Error())
				continue
			}
		}
		// Keep the marker until source removal succeeds; a retry can still prove
		// ownership after a failed delete. Markerless old checkpoints use the
		// committed manifest and clone-owner validation above.
		if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
			failures = append(failures, err.Error())
		}
	}
	if len(failures) == 0 && j.SourceRoot != "" {
		if entries, err := os.ReadDir(j.SourceRoot); err == nil && len(entries) == 0 {
			_ = os.Remove(j.SourceRoot)
		}
	}
	return failures
}
