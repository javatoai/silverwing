package storage

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

// ValidateTaskRootPath validates and cleans a task root while retaining its
// configured spelling. Existing aliases and ancestors participate in the
// overlap check. The caller checks that the directory is available for use.
func ValidateTaskRootPath(confDir, taskRoot string) (string, error) {
	return validateTaskRootPath(confDir, taskRoot, false)
}

func validateTaskRootPath(confDir, taskRoot string, allowUnavailable bool) (string, error) {
	if !filepath.IsAbs(taskRoot) || strings.ContainsRune(taskRoot, '\x00') {
		return "", errors.New("任务存放目录必须是绝对路径")
	}
	root := filepath.Clean(taskRoot)
	home, err := filepath.Abs(confDir)
	if err != nil {
		return "", fmt.Errorf("解析应用数据目录失败：%w", err)
	}
	if directoryPathsOverlap(home, root) {
		return "", errors.New("任务存放目录必须与应用数据目录分开")
	}
	actualHome, err := canonicalPathWithMissingTail(home, canonicalExistingTaskRootPath)
	if err != nil {
		return "", fmt.Errorf("解析应用数据目录实际路径失败：%w", err)
	}
	actualRoot, err := canonicalPathWithMissingTail(root, canonicalExistingTaskRootPath)
	if err != nil {
		if allowUnavailable {
			return root, nil
		}
		return "", fmt.Errorf("解析任务存放目录实际路径失败：%w", err)
	}
	if directoryPathsOverlap(actualHome, actualRoot) {
		return "", errors.New("任务存放目录必须与应用数据目录分开")
	}
	return root, nil
}

func directoryPathsOverlap(first, second string) bool {
	return isUnderDirectory(first, second) || isUnderDirectory(second, first)
}
