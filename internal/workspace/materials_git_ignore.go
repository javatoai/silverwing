package workspace

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"silverwing-go/internal/storage"
)

func (s *MaterialsGitSyncService) migrateGeneratedIgnores(ctx context.Context, scope MaterialsGitScope) error {
	check := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if s.Check != nil {
			return s.Check()
		}
		return nil
	}
	if err := check(); err != nil {
		return err
	}
	if err := storage.RetireLegacyMaterialsLock(scope.Root); err != nil {
		return err
	}
	exclude, err := s.run(ctx, scope, true, "rev-parse", "--path-format=absolute", "--git-path", "info/exclude")
	if err != nil {
		return err
	}
	if err := check(); err != nil {
		return err
	}
	if err := storage.MigrateMaterialsGitExclude(strings.TrimSpace(exclude.Stdout)); err != nil {
		return fmt.Errorf("迁移旧资料 Git 忽略规则失败：%w", err)
	}
	return filepath.WalkDir(scope.Root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if strings.EqualFold(entry.Name(), ".git") {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() && path != scope.Root {
			if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
				return filepath.SkipDir // Do not edit a nested repository's rules.
			} else if !os.IsNotExist(err) {
				return err
			}
		}
		if entry.Name() != ".gitignore" || !strings.EqualFold(filepath.Base(filepath.Dir(path)), "temp") {
			return nil
		}
		if err := check(); err != nil {
			return err
		}
		if err := storage.MigrateMaterialsTemporaryIgnore(path); err != nil {
			return fmt.Errorf("迁移旧临时资料忽略规则失败：%w", err)
		}
		return nil
	})
}
