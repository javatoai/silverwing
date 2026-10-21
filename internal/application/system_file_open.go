package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// FileOpeningStatus mirrors the source desktop's default-association response.
// Non-Windows platforms intentionally report no chooser and no association error.
type FileOpeningStatus struct {
	DisplayName        string `json:"displayName,omitempty"`
	AssociationMissing bool   `json:"associationMissing"`
	ChooserAvailable   bool   `json:"chooserAvailable"`
}

// FileOpeningResult distinguishes a submitted open/chooser action from a user
// cancel without turning cancellation into an application error.
type FileOpeningResult struct {
	Status string `json:"status"`
}

type systemFileOpenOutcome uint8

const (
	systemFileOpenSubmitted systemFileOpenOutcome = iota + 1
	systemFileOpenCancelled
	systemFileOpenNeedsChooser
)

type systemFileOpening interface {
	status(path string) (FileOpeningStatus, error)
	open(ctx context.Context, path string) (systemFileOpenOutcome, error)
	choose(path string) (systemFileOpenOutcome, error)
}

type nativeSystemFileOpening struct{}

// All desktop entry points, including Codex's own worker, share the same
// native file chooser. Links, directories and association reads bypass it.
var systemFileOpeningMutex sync.Mutex

func runSystemFileOpening(ctx context.Context, kind, path string, opener systemFileOpening) (ActionResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	if kind == "system.open" || kind == "system.open-with" {
		if !systemFileOpeningMutex.TryLock() {
			return ActionResult{Data: FileOpeningResult{Status: "CANCELLED"}}, nil
		}
		defer systemFileOpeningMutex.Unlock()
	}
	if path == "" {
		return ActionResult{}, errors.New("路径不能为空")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return ActionResult{}, err
	}
	absolute = filepath.Clean(absolute)
	info, err := os.Lstat(absolute)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return ActionResult{}, fmt.Errorf("文件不存在或不是普通文件：%s", absolute)
	}
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}

	if kind == "system.file-opening-status" {
		status, statusErr := opener.status(absolute)
		return ActionResult{Message: "文件打开状态已读取", Path: absolute, Data: status}, statusErr
	}

	var outcome systemFileOpenOutcome
	switch kind {
	case "system.open-with":
		outcome, err = opener.choose(absolute)
	case "system.open":
		outcome, err = opener.open(ctx, absolute)
		if err == nil && outcome == systemFileOpenNeedsChooser {
			if err := ctx.Err(); err != nil {
				return ActionResult{}, err
			}
			outcome, err = opener.choose(absolute)
		}
	default:
		return ActionResult{}, fmt.Errorf("不支持的文件打开操作：%s", kind)
	}
	if ctx.Err() != nil {
		return ActionResult{}, errors.Join(ctx.Err(), err)
	}
	if err != nil {
		return ActionResult{}, err
	}
	switch outcome {
	case systemFileOpenSubmitted:
		return ActionResult{Message: "已交给系统打开", Path: absolute, Data: FileOpeningResult{Status: "SUBMITTED"}}, nil
	case systemFileOpenCancelled:
		return ActionResult{Path: absolute, Data: FileOpeningResult{Status: "CANCELLED"}}, nil
	default:
		return ActionResult{}, errors.New("文件打开服务返回了未知状态")
	}
}

// revealDirectoryTarget implements Source DesktopIntegration.reveal for
// non-Windows platforms: reveal a directory itself, or a file's parent.
func revealDirectoryTarget(path string, info os.FileInfo) string {
	if info.IsDir() {
		return path
	}
	return filepath.Dir(path)
}
