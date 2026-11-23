package workspace

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

// OperationLocks serializes task and repository mutations between the desktop
// and CLI processes. The file lock is intentionally non-blocking, matching the
// source application's "busy, ask the caller to retry" behavior.
type OperationLocks struct {
	Directory string
	Paths     storage.Paths
}

func NewOperationLocks(directory string) *OperationLocks {
	return &OperationLocks{Directory: directory}
}

func NewTaskOperationLocks(paths storage.Paths) *OperationLocks {
	return &OperationLocks{Directory: paths.Locks, Paths: paths}
}

func (l *OperationLocks) TaskRoot(config domain.AppConfig) string {
	return stringValue(config.TaskRoot)
}

func (l *OperationLocks) ValidateWorkspaceDirectory(config domain.AppConfig, taskPath string, manifest domain.TaskManifest) error {
	if manifest.WorkspaceDirectory != "" && !sameRecyclePath(manifest.WorkspaceDirectory, taskPath) {
		return fmt.Errorf("清单记录的工作区目录与任务目录不一致：%s", manifest.WorkspaceDirectory)
	}
	if l == nil || l.Paths.ProfileID == "" {
		return nil
	}
	root, err := taskRoot(l.TaskRoot(config))
	if err != nil {
		return err
	}
	taskPath = filepath.FromSlash(taskPath)
	parent, taskName := filepath.Dir(taskPath), filepath.Base(taskPath)
	if name, err := RequireValidTaskDirectoryName(taskName); err != nil || name != taskName || !filepath.IsAbs(taskPath) || filepath.Clean(taskPath) != taskPath ||
		(!sameRecyclePath(parent, root) && !samePath(parent, root)) ||
		taskName != manifest.TaskDirectoryName {
		return fmt.Errorf("任务记录目录与当前配置不匹配：%s", taskPath)
	}
	for _, ws := range manifest.Services {
		path := filepath.FromSlash(ws.WorktreePath)
		parent := filepath.Dir(path)
		if !filepath.IsAbs(path) || filepath.Clean(path) != path || (!sameRecyclePath(parent, taskPath) && !samePath(parent, taskPath)) {
			return fmt.Errorf("工作区路径不在任务目录的直接子级：%s", ws.WorktreePath)
		}
	}
	return requireOrdinaryDirectoryIfPresent(taskPath)
}

func requireOrdinaryDirectoryIfPresent(path string) error {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	reparse, err := bootstrapPathIsReparsePoint(path, info)
	if err != nil {
		return err
	}
	if !info.IsDir() || reparse {
		return fmt.Errorf("路径必须是普通目录：%s", path)
	}
	return nil
}

func (l *OperationLocks) WithTask(taskDirectory string, operation func() error) error {
	return l.with("task", taskDirectory, "任务正在被另一个操作修改："+taskDirectory, operation)
}

// Clone locks stay outside the checkout so it can be moved to the recycle bin
// while the lock remains held on Windows.
func (l *OperationLocks) WithIndependentCheckout(path string, operation func() error) error {
	return l.with("checkout", path, "独立克隆正在被另一个操作占用："+path, operation)
}

func (l *OperationLocks) WithRepository(commonDirectory string, operation func() error) error {
	if !filepath.IsAbs(commonDirectory) {
		return fmt.Errorf("Git 公共目录必须使用绝对路径：%s", commonDirectory)
	}
	common, err := filepath.EvalSymlinks(commonDirectory)
	if err != nil {
		return fmt.Errorf("解析 Git 公共目录失败：%w", err)
	}
	info, err := os.Stat(common)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("Git 公共目录不是目录：%s", common)
	}
	return withOperationLock(filepath.Join(common, "silverwing-operation.lock"), "仓库正在被另一个工作区操作占用："+common, operation)
}

func (l *OperationLocks) WithTaskRoot(taskRoot string, operation func() error) error {
	return l.with("task-root", taskRoot, "任务目录正在被另一个操作占用："+taskRoot, operation)
}

func (l *OperationLocks) with(kind, path, failure string, operation func() error) error {
	if l == nil || strings.TrimSpace(l.Directory) == "" {
		return fmt.Errorf("应用锁目录未配置")
	}
	lockPath := filepath.Join(l.Directory, kind+"-"+stablePathHash(path)+".lock")
	return withOperationLock(lockPath, failure, operation)
}

func withOperationLock(lockPath, failure string, operation func() error) error {
	release, err := storage.AcquireLock(lockPath)
	if err != nil {
		if errors.Is(err, storage.ErrLockBusy) {
			return fmt.Errorf("%s: %w", failure, err)
		}
		return err
	}
	defer release()
	return operation()
}

func stablePathHash(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		absolute = filepath.Clean(path)
	}
	normalized := filepath.Clean(absolute)
	existing := normalized
	for {
		if _, err := os.Stat(existing); err == nil {
			break
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			break
		}
		existing = parent
	}
	if real, err := filepath.EvalSymlinks(existing); err == nil {
		if existing == normalized {
			normalized = real
		} else if rel, err := filepath.Rel(existing, normalized); err == nil {
			normalized = filepath.Join(real, rel)
		}
	}
	identity := strings.ToLower(filepath.Clean(normalized))
	digest := sha256.Sum256([]byte(identity))
	return fmt.Sprintf("%x", digest[:8])
}
