package workspace

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"silverwing-go/internal/storage"
)

// OperationLocks serializes task and repository mutations between the desktop
// and CLI processes. The file lock is intentionally non-blocking, matching the
// source application's "busy, ask the caller to retry" behavior.
type OperationLocks struct{ Directory string }

func NewOperationLocks(directory string) *OperationLocks {
	return &OperationLocks{Directory: directory}
}

func (l *OperationLocks) WithTask(taskDirectory string, operation func() error) error {
	return l.with("task", taskDirectory, "任务正在被另一个操作修改："+taskDirectory, operation)
}

func (l *OperationLocks) WithRepository(repository string, operation func() error) error {
	return l.with("repository", repository, "仓库正在被另一个工作区操作占用："+repository, operation)
}

func (l *OperationLocks) WithTaskRoot(taskRoot string, operation func() error) error {
	return l.with("task-root", taskRoot, "任务目录正在被另一个操作占用："+taskRoot, operation)
}

func (l *OperationLocks) with(kind, path, failure string, operation func() error) error {
	if l == nil || strings.TrimSpace(l.Directory) == "" {
		return fmt.Errorf("应用锁目录未配置")
	}
	lockPath := filepath.Join(l.Directory, kind+"-"+stablePathHash(path)+".lock")
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
