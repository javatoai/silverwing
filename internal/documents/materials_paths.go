package documents

import (
	"fmt"
	"os"
	"path/filepath"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

// ValidateMaterialsPaths performs the same layout checks as preparation, but
// never creates directories or changes the selected configuration.
func ValidateMaterialsPaths(configured string) (domain.MaterialsPaths, error) {
	paths, err := domain.ResolveMaterialsPaths(configured)
	if err != nil {
		return paths, err
	}
	_, err = configuredRequirementsRoot(&configured, false)
	return paths, err
}

// EnsureMaterialsPaths is used only by explicit configuration saves and actual
// preparation. Read-only previews resolve planned paths without creating them.
func EnsureMaterialsPaths(configured string) (domain.MaterialsPaths, error) {
	paths, err := domain.ResolveMaterialsPaths(configured)
	if err != nil {
		return paths, err
	}
	release, err := storage.AcquireMaterialsRepositoryLock(paths.Root)
	if err != nil {
		return paths, err
	}
	defer release()
	if err := ensureMaterialsDirectory(filepath.VolumeName(paths.Root)+string(filepath.Separator), paths.Root); err != nil {
		return paths, err
	}
	if err := checkMaterialsCollectionLayout(paths); err != nil {
		return paths, err
	}
	if err := ensureMaterialsDirectory(paths.Root, paths.Requirements); err != nil {
		return paths, err
	}
	return paths, nil
}

func configuredRequirementsRoot(configured *string, create bool) (string, error) {
	if configured == nil {
		return "", fmt.Errorf("请先配置任务资料根目录")
	}
	paths, err := domain.ResolveMaterialsPaths(*configured)
	if err != nil {
		return "", err
	}
	if create {
		paths, err = EnsureMaterialsPaths(*configured)
		return paths.Requirements, err
	}
	for _, path := range []string{paths.Root, paths.Requirements} {
		info, statErr := os.Lstat(path)
		if os.IsNotExist(statErr) && path == paths.Requirements {
			continue
		}
		if statErr != nil || !info.IsDir() || isUnsafeDirectory(info, path) || !sameResolvedPath(path) {
			return "", fmt.Errorf("资料根路径必须是已存在的普通目录：%s", path)
		}
	}
	if err := checkMaterialsCollectionLayout(paths); err != nil {
		return "", err
	}
	return paths.Requirements, nil
}

func checkMaterialsCollectionLayout(paths domain.MaterialsPaths) error {
	entries, err := os.ReadDir(paths.Root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !MaterialsYear(entry.Name()) {
			continue
		}
		year := filepath.Join(paths.Root, entry.Name())
		if !sameResolvedPath(year) {
			return fmt.Errorf("年份目录包含不安全的链接：%s", year)
		}
		children, err := os.ReadDir(year)
		if err != nil {
			return err
		}
		for _, child := range children {
			if child.IsDir() && MaterialsRequirementName(child.Name()) {
				return fmt.Errorf("请选择包含 requirements 的总目录，年度需求资料应位于 requirements 内：%s", paths.Root)
			}
		}
	}
	return nil
}
