//go:build !windows

package application

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"runtime"
	"time"
)

func (nativeSystemFileOpening) status(string) (FileOpeningStatus, error) {
	return FileOpeningStatus{ChooserAvailable: false}, nil
}

func (nativeSystemFileOpening) open(ctx context.Context, path string) (systemFileOpenOutcome, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	openContext, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := "xdg-open"
	if runtime.GOOS == "darwin" {
		command = "open"
	}
	cmd := exec.CommandContext(openContext, command, path)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if parentErr := ctx.Err(); parentErr != nil {
			return 0, parentErr
		}
		if errors.Is(openContext.Err(), context.DeadlineExceeded) {
			return 0, errors.New("系统打开文件超时，请重试")
		}
		return 0, fmt.Errorf("系统无法打开文件，请检查默认应用：%w", err)
	}
	return systemFileOpenSubmitted, nil
}

func (nativeSystemFileOpening) choose(string) (systemFileOpenOutcome, error) {
	return 0, errors.New("当前系统不支持应用选择窗口")
}
