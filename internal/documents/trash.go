package documents

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

var ErrTrashCancelled = errors.New("系统回收操作已取消")

type TrashItemResult struct {
	Path      string `json:"path"`
	Error     string `json:"error,omitempty"`
	Cancelled bool   `json:"cancelled,omitempty"`
}

type trashBackend interface {
	moveToTrash(context.Context, string) error
}

type TrashService struct{ backend trashBackend }

func NewTrashService() *TrashService { return &TrashService{backend: newPlatformTrashBackend()} }

// MoveToTrash prevalidates the whole selection, then moves items one by one.
// Each outcome is returned independently; permanent deletion is never used.
func (s *TrashService) MoveToTrash(ctx context.Context, root string, relativePaths []string) (results []TrashItemResult, resultErr error) {
	if s == nil || s.backend == nil {
		return nil, errors.New("系统回收站不可用")
	}
	scope, err := newMaterialsOperationScope(root)
	if err != nil {
		return nil, err
	}
	defer func() {
		scope.finish(&resultErr)
		for i := range results {
			results[i].Path = scope.public(results[i].Path)
		}
	}()
	physical := make([]string, 0, len(relativePaths))
	for _, value := range relativePaths {
		path, err := scope.resolve(value, false, true, false)
		if err != nil {
			return nil, err
		}
		physical = append(physical, path)
	}
	root = scope.root
	paths, err := (&Service{}).ResolveDeleteItems(root, physical)
	if err != nil {
		return nil, err
	}
	identities := make(map[string]*FileIdentity, len(paths))
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, fmt.Errorf("删除目标发生变化，请刷新后重试：%w", err)
		}
		identities[path] = fileIdentity(path, info)
	}
	results = make([]TrashItemResult, 0, len(paths))
	for index, path := range paths {
		if err := ctx.Err(); err != nil {
			for _, pending := range paths[index:] {
				results = append(results, TrashItemResult{Path: pending, Cancelled: true})
			}
			return results, nil
		}
		// Confirm that the selected path still identifies the same item and has
		// not been redirected through a junction while the UI was waiting.
		rootPath, rootErr := validatedMaterialsRoot(root)
		if rootErr != nil {
			results = append(results, TrashItemResult{Path: path, Error: rootErr.Error()})
			continue
		}
		current, pathErr := safePath(rootPath, path, false)
		if pathErr != nil {
			results = append(results, TrashItemResult{Path: path, Error: pathErr.Error()})
			continue
		}
		info, infoErr := os.Lstat(current)
		if infoErr != nil || !sameResolvedPath(current) {
			failure := infoErr
			if failure == nil {
				failure = errors.New("文件路径已变化或越过资料目录")
			}
			results = append(results, TrashItemResult{Path: path, Error: failure.Error()})
			continue
		}
		if err := validateMaterialPath(rootPath, current, info); err != nil {
			results = append(results, TrashItemResult{Path: path, Error: err.Error()})
			continue
		}
		if !sameFileIdentity(fileIdentity(current, info), identities[path]) {
			results = append(results, TrashItemResult{Path: path, Error: "删除目标在确认后发生变化，请刷新后重试"})
			continue
		}
		if info.IsDir() {
			if err := validateMaterialsTree(rootPath, current, false); err != nil {
				results = append(results, TrashItemResult{Path: path, Error: err.Error()})
				continue
			}
		}
		if err := scope.revalidate(); err != nil {
			results = append(results, TrashItemResult{Path: path, Error: err.Error()})
			continue
		}
		if err := s.backend.moveToTrash(ctx, current); err != nil {
			if errors.Is(err, ErrTrashCancelled) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				results = append(results, TrashItemResult{Path: path, Cancelled: true})
				for _, pending := range paths[index+1:] {
					results = append(results, TrashItemResult{Path: pending, Cancelled: true})
				}
				break
			}
			results = append(results, TrashItemResult{Path: path, Error: fmt.Sprintf("无法移入系统回收站：%v", err)})
			continue
		}
		results = append(results, TrashItemResult{Path: path})
	}
	return results, nil
}

// canonicalTrashPath is shared by native backends that require an absolute
// path and helps keep diagnostics stable across platforms.
func canonicalTrashPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}
