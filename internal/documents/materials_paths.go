package documents

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

// ValidateLocalDocumentsBranch is an inexpensive check for document reads and
// preparation. Callers performing writes hold the repository lock as well.
func ValidateLocalDocumentsBranch(root, branch string) error {
	metadata := filepath.Join(root, ".git")
	info, err := os.Lstat(metadata)
	if err != nil || !info.IsDir() || isUnsafeDirectory(info, metadata) || !sameResolvedPath(metadata) {
		return errors.New("资料仓库的 Git 元数据缺失或已变化，请恢复本地仓库")
	}
	headPath := filepath.Join(metadata, "HEAD")
	info, err = os.Lstat(headPath)
	if err != nil || !info.Mode().IsRegular() || !sameResolvedPath(headPath) {
		return errors.New("无法读取资料仓库当前分支")
	}
	head, err := os.Open(headPath)
	if err != nil {
		return err
	}
	defer head.Close()
	const maxHeadBytes = 4096
	data, err := io.ReadAll(io.LimitReader(head, maxHeadBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxHeadBytes || strings.TrimRight(string(data), "\r\n") != "ref: refs/heads/"+branch {
		return errors.New("资料仓库尚未检出当前配置的分支，请先同步")
	}
	return nil
}

func lockConfiguredMaterialsRepository(config domain.AppConfig, paths domain.MaterialsPaths) (func(), error) {
	settings := config.MaterialsGitSettings()
	if settings.LocalRoot == "" {
		return storage.AcquireMaterialsRepositoryLock(paths.Root)
	}
	release, err := storage.AcquireDocumentsRepositoryLock(paths.Root)
	if err != nil {
		return nil, err
	}
	if err := ValidateLocalDocumentsBranch(paths.Root, settings.Branch); err != nil {
		release()
		return nil, err
	}
	return release, nil
}

// ResolveBoundTaskMaterials projects the stored year and requirement directory
// onto a selected clone that already contains that requirement. The manifest
// and the old checkout are left intact, including any drafts scoped to them.
func ResolveBoundTaskMaterials(manifest domain.TaskManifest, config domain.AppConfig) domain.TaskManifest {
	if config.MaterialsGitSettings().LocalRoot == "" || manifest.RequirementMaterials.WriteRoot == nil || config.RequirementMaterialsSubdirectory == nil {
		return manifest
	}
	original, ok := AnnualScope(*manifest.RequirementMaterials.WriteRoot)
	if !ok || !strings.EqualFold(filepath.Base(original.Base), domain.RequirementsDirectory) ||
		!strings.EqualFold(filepath.Base(original.Owned), *config.RequirementMaterialsSubdirectory) {
		return manifest
	}
	paths, err := config.MaterialsPaths()
	if err != nil || samePathName(paths.Requirements, original.Base) {
		return manifest
	}
	root := filepath.Join(paths.Requirements, filepath.Base(filepath.Dir(original.Root)), filepath.Base(original.Root))
	info, err := os.Lstat(root)
	if err != nil || !info.IsDir() || isUnsafeDirectory(info, root) || !sameResolvedPath(root) {
		return manifest
	}
	owned := filepath.Join(root, *config.RequirementMaterialsSubdirectory)
	manifest.RequirementMaterials.WriteRoot = &owned
	return manifest
}

// ValidateMaterialsPaths performs the same layout checks as preparation, but
// never creates directories. A caller that mutates the repository must hold
// the repository lock while validating and mutating.
func ValidateMaterialsPaths(configured string) (domain.MaterialsPaths, error) {
	paths, err := domain.ResolveMaterialsPaths(configured)
	if err != nil {
		return paths, err
	}
	return paths, validateMaterialsPaths(paths)
}

// EnsureMaterialsPaths prepares a configured repository and its collection.
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
	return paths, ensureMaterialsPaths(paths)
}

// The caller holds the lock for paths.Root.
func ensureMaterialsPaths(paths domain.MaterialsPaths) error {
	if err := ensureMaterialsDirectory(filepath.VolumeName(paths.Root)+string(filepath.Separator), paths.Root); err != nil {
		return err
	}
	if err := validateMaterialsPaths(paths); err != nil {
		return err
	}
	return ensureMaterialsDirectory(paths.Root, paths.Requirements)
}

func validateMaterialsPaths(paths domain.MaterialsPaths) error {
	rootInfo, err := os.Lstat(paths.Root)
	if err != nil || !rootInfo.IsDir() || isUnsafeDirectory(rootInfo, paths.Root) || !sameResolvedPath(paths.Root) {
		return fmt.Errorf("资料根路径必须是已存在的普通目录：%s", paths.Root)
	}
	collectionInfo, err := os.Lstat(paths.Requirements)
	if !os.IsNotExist(err) {
		if err != nil || !collectionInfo.IsDir() || isUnsafeDirectory(collectionInfo, paths.Requirements) || !sameResolvedPath(paths.Requirements) {
			return fmt.Errorf("需求资料路径必须是普通目录：%s", paths.Requirements)
		}
		marker := filepath.Join(paths.Requirements, ".git")
		if _, err := os.Lstat(marker); err == nil {
			return fmt.Errorf("需求资料子目录包含独立的 Git 元数据，请处理该路径后重试：%s", marker)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
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
			if MaterialsRequirementName(child.Name()) && (child.IsDir() || child.Type()&os.ModeSymlink != 0) {
				return fmt.Errorf("发现未归入 requirements 的年度需求资料，请检查后重试：%s", filepath.Join(year, child.Name()))
			}
		}
	}
	return nil
}
