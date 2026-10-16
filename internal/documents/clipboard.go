package documents

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strings"
)

type clipboardBackend interface {
	read(context.Context) (ClipboardPayload, error)
	copyFiles(context.Context, []string) error
}

type ClipboardService struct{ backend clipboardBackend }

func NewClipboardService() *ClipboardService {
	return &ClipboardService{backend: newPlatformClipboardBackend()}
}

// Read returns native file-list data when available and only falls back to PNG
// image data when the clipboard does not contain file paths.
func (s *ClipboardService) Read(ctx context.Context) (ClipboardPayload, error) {
	if s == nil || s.backend == nil {
		return ClipboardPayload{}, errors.New("系统剪贴板不可用")
	}
	payload, err := s.backend.read(ctx)
	if err != nil {
		return ClipboardPayload{}, err
	}
	if len(payload.Files) > 0 {
		payload.Files = distinctImportSources(payload.Files)
		payload.ImagePNG = nil
	}
	return payload, nil
}

// CopyFiles places existing regular files or directories on the system
// clipboard as a native file-list payload, after validating every selection.
func (s *ClipboardService) CopyFiles(ctx context.Context, paths []string) error {
	if s == nil || s.backend == nil {
		return errors.New("系统剪贴板不可用")
	}
	selected, err := nativeCopyFileSelection(ctx, paths)
	if err != nil {
		return err
	}
	return s.backend.copyFiles(ctx, selected)
}

// ResolveDeleteItems validates every selected item before a trash operation
// and removes descendants already covered by a selected parent directory.
func (s *Service) ResolveDeleteItems(root string, relativePaths []string) ([]string, error) {
	validatedRoot, err := validatedMaterialsRoot(root)
	if err != nil {
		return nil, err
	}
	selected := make([]string, 0, len(relativePaths))
	seen := map[string]bool{}
	for _, value := range relativePaths {
		if value == "" {
			return nil, errors.New("待删除路径不能为空")
		}
		path, err := safePath(validatedRoot, value, false)
		if err != nil {
			return nil, err
		}
		if samePathName(path, validatedRoot) {
			return nil, errors.New("不能移除整个任务资料目录")
		}
		info, err := os.Lstat(path)
		if err != nil {
			return nil, err
		}
		if err := validateMaterialPath(validatedRoot, path, info); err != nil {
			return nil, err
		}
		if info.IsDir() {
			if err := validateMaterialsTree(validatedRoot, path, false); err != nil {
				return nil, err
			}
		}
		key := path
		if runtime.GOOS == "windows" {
			key = strings.ToLower(key)
		}
		if !seen[key] {
			seen[key] = true
			selected = append(selected, path)
		}
	}
	selected = slicesFilter(selected, func(item string) bool {
		for _, parent := range selected {
			if !samePathName(parent, item) && pathWithin(parent, item) {
				info, err := os.Lstat(parent)
				if err == nil && info.IsDir() {
					return false
				}
			}
		}
		return true
	})
	return selected, nil
}
