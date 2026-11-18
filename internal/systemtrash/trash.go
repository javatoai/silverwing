package systemtrash

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
)

// Label returns the system's display name for the trash.
func Label() string {
	if runtime.GOOS == "darwin" {
		return "废纸篓"
	}
	return "回收站"
}

// Move moves an absolute filesystem path into the system trash and returns
// the location reported by the operating system.
func Move(ctx context.Context, path string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return "", errors.New("系统回收操作需要有效的绝对路径")
	}
	path = filepath.Clean(path)
	if filepath.Dir(path) == path {
		return "", errors.New("不能回收磁盘根目录")
	}
	return move(ctx, path)
}
