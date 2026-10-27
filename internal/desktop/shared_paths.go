package desktop

import (
	"path/filepath"
	"runtime"
	"strings"
)

const (
	taskRepositoryChoicesCreateOwner = "task-create"
	taskRepositoryChoicesAppendOwner = "task-append"
)

func sameTaskRepositoryPath(left, right string) bool {
	left = filepath.Clean(strings.TrimSpace(left))
	right = filepath.Clean(strings.TrimSpace(right))
	if left == "." || right == "." || left == "" || right == "" {
		return false
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func sameTaskRepositoryPathOrEmpty(left, right string) bool {
	if strings.TrimSpace(left) == "" || strings.TrimSpace(right) == "" {
		return strings.TrimSpace(left) == strings.TrimSpace(right)
	}
	return sameTaskRepositoryPath(left, right)
}
