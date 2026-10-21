package documents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// ResolveCopyFiles validates each selected materials item without inspecting
// directory contents, then removes children covered by selected directories.
func (s *Service) ResolveCopyFiles(ctx context.Context, root string, values []string) ([]string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	paths, err := s.ResolveCopyPaths(ctx, root, values)
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		if samePathName(path, root) {
			return nil, errors.New("任务资料根目录不是可选择的文件或文件夹")
		}
	}
	return nativeCopyFileSelection(ctx, paths)
}

// Source DesktopActions.copyFiles validates all normalized selections before
// collapsing descendants. A copied directory is a reference, so its contents
// do not undergo import or deletion validation.
func nativeCopyFileSelection(ctx context.Context, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return nil, errors.New("请先选择文件或文件夹")
	}
	seen := make(map[string]bool, len(paths))
	selected := make([]string, 0, len(paths))
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return nil, err
		}
		absolute = filepath.Clean(absolute)
		key := absolute
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if seen[key] {
			continue
		}
		info, err := os.Lstat(absolute)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || (!info.Mode().IsRegular() && !info.IsDir()) {
			return nil, fmt.Errorf("文件或文件夹不存在：%s", absolute)
		}
		seen[key] = true
		selected = append(selected, absolute)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return filterMaterialDescendants(selected), nil
}
