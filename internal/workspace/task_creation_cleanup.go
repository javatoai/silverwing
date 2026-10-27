package workspace

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"silverwing-go/internal/storage"
)

type generatedTaskDocument struct {
	path      string
	directory bool
}

// removeGeneratedTaskDocuments removes only the fixed documents created by a
// task. Every existing path component is checked with Lstat so a link or
// Windows reparse point cannot redirect cleanup outside the owned task root.
func removeGeneratedTaskDocuments(taskPath string, owned os.FileInfo) error {
	paths := []generatedTaskDocument{
		{path: storage.TaskManifestFileName},
		{path: AgentRouterFileName},
		{path: filepath.Join(AgentWorkspaceDir, AgentDirectoryName, AgentContextFileName)},
		{path: filepath.Join(AgentWorkspaceDir, AgentDirectoryName, AgentRulesFileName)},
		{path: filepath.Join(AgentWorkspaceDir, AgentDirectoryName), directory: true},
		{path: filepath.Join(AgentWorkspaceDir, HandoffFileName)},
		{path: AgentWorkspaceDir, directory: true},
	}
	for _, generated := range paths {
		path, _, exists, err := inspectOwnedTaskPath(taskPath, owned, generated.path, generated.directory)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		// Revalidate immediately before removal. This also catches a replaced
		// task root between path inspection and the mutation.
		path, _, exists, err = inspectOwnedTaskPath(taskPath, owned, generated.path, generated.directory)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if err := os.Remove(path); err != nil {
			if generated.directory {
				if _, _, stillExists, checkErr := inspectOwnedTaskPath(taskPath, owned, generated.path, true); checkErr != nil {
					return checkErr
				} else if stillExists {
					entries, readErr := os.ReadDir(path)
					if readErr != nil {
						return fmt.Errorf("读取保留的生成目录失败：%s: %w", path, readErr)
					}
					if len(entries) == 0 {
						return fmt.Errorf("移除任务生成内容失败：%s: %w", path, err)
					}
					preserved := make([]string, 0, len(entries))
					for _, entry := range entries {
						preserved = append(preserved, filepath.Join(path, entry.Name()))
					}
					return fmt.Errorf("生成目录中仍有未能安全归属的内容，已保留：%s: %w", strings.Join(preserved, "，"), err)
				}
			}
			return fmt.Errorf("移除任务生成内容失败：%s: %w", path, err)
		}
	}
	return nil
}

func inspectOwnedTaskPath(taskPath string, owned os.FileInfo, relative string, wantDirectory bool) (string, os.FileInfo, bool, error) {
	if err := verifyOwnedTaskDirectory(taskPath, owned); err != nil {
		return "", nil, false, err
	}
	root, err := filepath.Abs(taskPath)
	if err != nil {
		return "", nil, false, err
	}
	path := filepath.Join(root, filepath.Clean(relative))
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", nil, false, fmt.Errorf("任务生成路径超出任务目录：%s", path)
	}
	current := root
	components := strings.Split(rel, string(filepath.Separator))
	for index, component := range components {
		current = filepath.Join(current, component)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return path, nil, false, nil
		}
		if err != nil {
			return "", nil, false, fmt.Errorf("检查任务生成路径失败：%s: %w", current, err)
		}
		reparse, err := bootstrapPathIsReparsePoint(current, info)
		if err != nil {
			return "", nil, false, fmt.Errorf("检查任务生成路径失败：%s: %w", current, err)
		}
		if reparse {
			return "", nil, false, fmt.Errorf("任务生成路径包含链接或重解析点，已保留现场：%s", current)
		}
		last := index == len(components)-1
		if !last && !info.IsDir() {
			return "", nil, false, fmt.Errorf("任务生成路径的父级不是目录，已保留现场：%s", current)
		}
		if last {
			if wantDirectory && !info.IsDir() {
				return "", nil, false, fmt.Errorf("任务生成目录类型已变化，已保留现场：%s", current)
			}
			if !wantDirectory && !info.Mode().IsRegular() {
				return "", nil, false, fmt.Errorf("任务生成文件类型已变化，已保留现场：%s", current)
			}
			return current, info, true, nil
		}
	}
	return path, nil, false, nil
}

func verifyOwnedTaskDirectory(taskPath string, owned os.FileInfo) error {
	if owned == nil {
		return fmt.Errorf("无法确认新建任务目录身份，已保留现场：%s", taskPath)
	}
	current, err := os.Lstat(taskPath)
	if err != nil {
		return fmt.Errorf("读取任务目录身份失败，已保留现场：%s: %w", taskPath, err)
	}
	reparse, err := bootstrapPathIsReparsePoint(taskPath, current)
	if err != nil {
		return fmt.Errorf("读取任务目录类型失败，已保留现场：%s: %w", taskPath, err)
	}
	if !current.IsDir() || reparse || !os.SameFile(owned, current) {
		return fmt.Errorf("任务目录已被替换或类型发生变化，已保留现场：%s", taskPath)
	}
	return nil
}

func removeEmptyOwnedTaskDirectory(taskPath string, owned os.FileInfo) error {
	if err := verifyOwnedTaskDirectory(taskPath, owned); err != nil {
		return err
	}
	entries, err := os.ReadDir(taskPath)
	if err != nil {
		return fmt.Errorf("检查任务目录内容失败，已保留现场：%w", err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, filepath.Join(taskPath, entry.Name()))
		}
		return fmt.Errorf("任务目录中仍有未能安全归属的内容，已保留现场：%s", strings.Join(names, "，"))
	}
	if err := verifyOwnedTaskDirectory(taskPath, owned); err != nil {
		return err
	}
	if err := os.Remove(taskPath); err != nil {
		return fmt.Errorf("移除空任务目录失败，已保留现场：%s: %w", taskPath, err)
	}
	return nil
}
