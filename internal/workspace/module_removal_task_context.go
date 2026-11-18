package workspace

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
)

// Only the generated task context depends on the attached workspace rows.
// Keep its original bytes so a failed recycle or restore can put them back
// without rewriting AGENTS.md, task rules, or user-edited catalog text.
type workspaceTaskContextUpdate struct {
	taskPath, taskIdentity, path string
	original, written            []byte
	previous, published          os.FileInfo
}

func captureWorkspaceTaskContext(taskPath string) (*workspaceTaskContextUpdate, error) {
	identity, err := documents.DirectoryFingerprint(taskPath)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(taskPath, AgentWorkspaceDir, AgentDirectoryName, AgentContextFileName)
	update := &workspaceTaskContextUpdate{taskPath: taskPath, taskIdentity: identity, path: path}
	if err := update.validatePath(); err != nil {
		return nil, err
	}
	file, err := openRecycleRegular(path)
	if os.IsNotExist(err) {
		return update, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取当前任务上下文：%w", err)
	}
	defer file.Close()
	update.previous, err = file.Stat()
	if err != nil {
		return nil, err
	}
	update.original, err = io.ReadAll(file)
	return update, err
}

func (update *workspaceTaskContextUpdate) write(docs *AgentDocumentService, manifest domain.TaskManifest, config domain.AppConfig) error {
	checkPrevious := func() error { return update.validateFile(update.previous) }
	if err := checkPrevious(); err != nil {
		return err
	}
	next := []byte(renderTaskContext(manifest, repositoryInfos(config), docs.Paths))
	if update.previous != nil && bytes.Equal(update.original, next) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(update.path), 0o755); err != nil {
		return err
	}
	if err := writeMaterialsCatalogFile(update.path, update.original, next, update.previous == nil, checkPrevious); err != nil {
		return fmt.Errorf("更新任务上下文：%w", err)
	}
	update.written = next
	file, err := openRecycleRegular(update.path)
	if err != nil {
		return err
	}
	defer file.Close()
	update.published, err = file.Stat()
	return err
}

func (update *workspaceTaskContextUpdate) rollback() error {
	if update.written == nil {
		return nil
	}
	if err := update.validatePath(); err != nil {
		return err
	}
	if update.previous != nil {
		checkPublished := func() error { return update.validateFile(update.published) }
		return writeMaterialsCatalogFile(update.path, update.written, update.original, false, checkPublished)
	}
	current, err := os.Lstat(update.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if update.published == nil || !os.SameFile(current, update.published) {
		return errModuleRemovalChanged
	}
	content, err := os.ReadFile(update.path)
	if err != nil {
		return err
	}
	if !bytes.Equal(content, update.written) {
		return errModuleRemovalChanged
	}
	return os.Remove(update.path)
}

func (update *workspaceTaskContextUpdate) validateFile(expected os.FileInfo) error {
	if err := update.validatePath(); err != nil {
		return err
	}
	current, err := os.Lstat(update.path)
	if expected == nil && os.IsNotExist(err) {
		return nil
	}
	if err != nil || expected == nil || !sameModuleFileState(expected, current) {
		return errors.New("任务上下文文件已变化")
	}
	return nil
}

func (update *workspaceTaskContextUpdate) validatePath() error {
	identity, err := documents.DirectoryFingerprint(update.taskPath)
	if err != nil || identity != update.taskIdentity {
		return errors.New("任务目录身份已变化，无法更新任务上下文")
	}
	if err := materialsGitSafePath(update.taskPath, update.path, false); err != nil {
		return fmt.Errorf("任务上下文路径不安全：%w", err)
	}
	return nil
}
