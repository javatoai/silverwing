//go:build !darwin || ios

package systemtrash

import (
	"context"
	"errors"
)

func move(context.Context, string) (string, error) {
	return "", errors.New("当前平台无法提供系统回收后的文件路径")
}
