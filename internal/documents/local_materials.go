package documents

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const LocalMaterialsSubdirectory = "docs"

// LocalTaskMaterials is kept inside its owning task, outside the shared
// requirement-materials repository. It needs neither a requirement nor Git.
func LocalTaskMaterials(taskPath string) AnnualMaterialsScope {
	root := filepath.Join(taskPath, ".workspace", "materials")
	return AnnualMaterialsScope{Base: filepath.Clean(taskPath), Root: root, Owned: filepath.Join(root, LocalMaterialsSubdirectory), Temporary: filepath.Join(root, TemporaryMaterialsDirectory)}
}

func LocalScope(owned string) (AnnualMaterialsScope, bool) {
	owned = filepath.Clean(owned)
	root := filepath.Dir(owned)
	workspace := filepath.Dir(root)
	if !filepath.IsAbs(owned) || filepath.Base(owned) != LocalMaterialsSubdirectory || filepath.Base(root) != "materials" || filepath.Base(workspace) != ".workspace" {
		return AnnualMaterialsScope{}, false
	}
	return LocalTaskMaterials(filepath.Dir(workspace)), true
}

func resolveMaterialsScope(owned string) (MaterialsScope, MaterialsKind, bool) {
	if scope, ok := AnnualScope(owned); ok {
		return scope, MaterialsAnnual, true
	}
	if scope, ok := LocalScope(owned); ok {
		return scope, MaterialsLocal, true
	}
	return MaterialsScope{}, MaterialsNone, false
}

func PrepareLocalTaskMaterials(taskPath string) (AnnualMaterialsScope, error) {
	scope := LocalTaskMaterials(taskPath)
	if _, err := validatedLocalTaskPath(taskPath); err != nil {
		return scope, err
	}
	return scope, scope.EnsureLocal()
}

// The configured TaskRoot may have linked ancestors. The task directory itself
// must be real, and its canonical subtree is checked by the ordinary materials
// functions so links beneath the task never become writable targets.
func validatedLocalTaskPath(taskPath string) (string, error) {
	info, err := os.Lstat(taskPath)
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(taskPath) || !info.IsDir() || isUnsafeDirectory(info, taskPath) {
		return "", fmt.Errorf("任务目录不是安全的本地目录：%s", taskPath)
	}
	canonical, err := canonicalLocalTaskPath(taskPath)
	if err != nil {
		return "", fmt.Errorf("解析任务目录实际路径 %s：%w", taskPath, err)
	}
	if err := validateRealAncestors(canonical, false); err != nil {
		return "", err
	}
	resolvedInfo, err := os.Lstat(canonical)
	if err != nil || !os.SameFile(info, resolvedInfo) {
		return "", fmt.Errorf("任务目录身份已变化：%s", taskPath)
	}
	return canonical, nil
}

func (s MaterialsScope) canonicalLocalScope() (MaterialsScope, error) {
	canonical, err := validatedLocalTaskPath(s.Base)
	if err != nil {
		return MaterialsScope{}, err
	}
	return LocalTaskMaterials(canonical), nil
}

// Creation rollback removes only empty directories and our exact ignore file.
// Unexpected user content is retained and reported instead of recursively removed.
func CleanupLocalTaskMaterials(taskPath string) error {
	canonicalTask, err := validatedLocalTaskPath(taskPath)
	if err != nil {
		return err
	}
	taskPath = canonicalTask
	scope := LocalTaskMaterials(canonicalTask)
	for _, dir := range []string{scope.Owned, scope.Temporary} {
		if _, err := os.Lstat(dir); os.IsNotExist(err) {
			continue
		}
		if _, err := safePath(taskPath, dir, false); err != nil {
			return err
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if dir != scope.Temporary || entry.Name() != ".gitignore" {
				return fmt.Errorf("本地资料中仍有内容，已保留：%s", dir)
			}
		}
	}
	for _, path := range []string{scope.Owned, filepath.Join(scope.Temporary, ".gitignore"), scope.Temporary, scope.Root} {
		if _, err := os.Lstat(path); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		if _, err := safePath(taskPath, path, false); err != nil {
			return err
		}
		if filepath.Base(path) == ".gitignore" {
			data, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(data, []byte("# Silverwing local temporary files\n*\n")) {
				return fmt.Errorf("临时忽略文件已变化，保留本地资料：%s", path)
			}
		}
		if err := os.Remove(path); err != nil {
			return fmt.Errorf("本地资料中仍有内容，已保留：%s: %w", path, err)
		}
	}
	return nil
}

func (s MaterialsScope) local() bool {
	local, ok := LocalScope(s.Owned)
	return ok && strings.EqualFold(local.Root, s.Root)
}
