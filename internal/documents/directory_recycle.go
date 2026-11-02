package documents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// DirectoryRecycler receives directories authorized by the task workflow.
// Ordinary materials operations retain their existing write policy.
type DirectoryRecycler interface {
	Recycle(context.Context, string) error
}

type SystemDirectoryRecycler struct{}

func DirectoryFingerprint(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || isUnsafeDirectory(info, path) || !sameResolvedPath(path) {
		return "", fmt.Errorf("目录包含不安全的链接或重解析点：%s", path)
	}
	key := fileIdentityKey(path)
	if key == "" {
		return "", errors.New("无法确认目录身份")
	}
	return key, nil
}

func (SystemDirectoryRecycler) Recycle(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	absolute, err := canonicalTrashPath(path)
	if err != nil {
		return err
	}
	if filepath.Dir(absolute) == absolute {
		return errors.New("不能回收磁盘根目录")
	}
	if _, err := DirectoryFingerprint(absolute); err != nil {
		return err
	}
	return newPlatformTrashBackend().moveToTrash(ctx, absolute)
}
