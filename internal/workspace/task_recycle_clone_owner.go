package workspace

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"silverwing-go/internal/documents"
)

type cloneRecycleOwnership string

const (
	cloneRecycleCurrent cloneRecycleOwnership = "current"
	cloneRecycleLegacy  cloneRecycleOwnership = "legacy"
	cloneRecycleMissing cloneRecycleOwnership = "missing"
)

const (
	legacyCloneOwnerMarker = "awm-owner"
	maxCloneOwnerBytes     = 4096
	maxCloneReflogLine     = 64 * 1024
)

// recycleCloneOwner validates the clone's ownership metadata without changing
// it. A present current marker is authoritative: a mismatch or read failure
// must never fall back to an older marker.
func recycleCloneOwner(target, expected string) (cloneRecycleOwnership, error) {
	if strings.TrimSpace(expected) == "" {
		return "", errors.New("独立克隆所有权标识为空")
	}
	absTarget, err := filepath.Abs(filepath.Clean(target))
	if err != nil {
		return "", err
	}
	if _, err := documents.DirectoryFingerprint(absTarget); err != nil {
		return "", fmt.Errorf("独立克隆目录不安全或无法确认身份：%w", err)
	}
	gitDirectory := filepath.Join(absTarget, ".git")
	if _, err := documents.DirectoryFingerprint(gitDirectory); err != nil {
		return "", fmt.Errorf("独立克隆 Git 目录不安全或无法确认身份：%w", err)
	}

	currentPath := cloneMarker(absTarget)
	current, currentExists, err := readCloneOwnerMarker(currentPath)
	if err != nil {
		return "", err
	}
	if currentExists {
		if current != expected {
			return "", fmt.Errorf("Silverwing 独立克隆所有权标记与任务记录不匹配：%s", currentPath)
		}
		return cloneRecycleCurrent, nil
	}

	legacyPath := filepath.Join(gitDirectory, legacyCloneOwnerMarker)
	legacy, legacyExists, err := readCloneOwnerMarker(legacyPath)
	if err != nil {
		return "", err
	}
	if !legacyExists {
		return cloneRecycleMissing, nil
	}
	if legacy != expected {
		return "", fmt.Errorf("旧版独立克隆所有权标记与任务记录不匹配：%s", legacyPath)
	}
	return cloneRecycleLegacy, nil
}

// verifyUnmarkedArchivedCloneOrigin permits an unmarked clone to be considered
// only when its own HEAD reflog records the expected clone origin as its first
// event. The reflog is read with a strict line limit and is never rewritten.
func verifyUnmarkedArchivedCloneOrigin(target, origin string) error {
	if strings.TrimSpace(origin) == "" {
		return errors.New("缺少独立克隆 origin，无法确认来源")
	}
	absTarget, err := filepath.Abs(filepath.Clean(target))
	if err != nil {
		return err
	}
	if _, err := documents.DirectoryFingerprint(absTarget); err != nil {
		return fmt.Errorf("独立克隆目录不安全或无法确认身份：%w", err)
	}
	gitDirectory := filepath.Join(absTarget, ".git")
	if _, err := documents.DirectoryFingerprint(gitDirectory); err != nil {
		return fmt.Errorf("独立克隆 Git 目录不安全或无法确认身份：%w", err)
	}
	logsDirectory := filepath.Join(gitDirectory, "logs")
	if _, err := documents.DirectoryFingerprint(logsDirectory); err != nil {
		return fmt.Errorf("独立克隆 HEAD reflog 目录不安全或不存在：%w", err)
	}
	reflogPath := filepath.Join(logsDirectory, "HEAD")
	reflog, err := openCloneRegularFile(reflogPath)
	if err != nil {
		return fmt.Errorf("无法安全读取独立克隆 HEAD reflog：%w", err)
	}
	defer reflog.Close()

	scanner := bufio.NewScanner(reflog)
	scanner.Buffer(make([]byte, 4096), maxCloneReflogLine)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("独立克隆 HEAD reflog 首行超过安全限制或读取失败：%w", err)
		}
		return errors.New("独立克隆 HEAD reflog 为空")
	}
	line := scanner.Text()
	separator := strings.IndexByte(line, '\t')
	if separator <= 0 {
		return errors.New("独立克隆 HEAD reflog 首行格式无效")
	}
	metadata := strings.Fields(line[:separator])
	if len(metadata) < 2 || len(metadata[0]) != len(metadata[1]) || !zeroGitObjectID(metadata[0]) || !validGitObjectID(metadata[1]) {
		return errors.New("独立克隆 HEAD reflog 首行不是初始 clone 记录")
	}
	if line[separator+1:] != "clone: from "+origin {
		return errors.New("独立克隆 HEAD reflog 首条来源与任务记录 origin 不一致")
	}
	return nil
}

func readCloneOwnerMarker(path string) (value string, exists bool, err error) {
	file, err := openCloneRegularFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("无法安全读取独立克隆所有权标记：%w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxCloneOwnerBytes+1))
	if err != nil {
		return "", false, fmt.Errorf("读取独立克隆所有权标记失败：%w", err)
	}
	if len(data) > maxCloneOwnerBytes {
		return "", false, errors.New("独立克隆所有权标记超过安全大小限制")
	}
	return string(data), true, nil
}

func openCloneRegularFile(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("不是普通文件或是链接：%s", path)
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil || !samePath(resolved, path) {
		return nil, fmt.Errorf("文件路径包含链接或重定向：%s", path)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		_ = file.Close()
		return nil, fmt.Errorf("文件在检查期间发生变化：%s", path)
	}
	return file, nil
}

func zeroGitObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, char := range value {
		if char != '0' {
			return false
		}
	}
	return true
}
