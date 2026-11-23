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

// Keep the original AGENTS.md bytes and file identity for rollback. The
// document service renders the new workspace rows inside its managed block.
type workspaceTaskAgentsUpdate struct {
	taskPath, taskIdentity, path string
	original, written            []byte
	previous, published          os.FileInfo
}

func captureWorkspaceTaskAgents(taskPath string) (*workspaceTaskAgentsUpdate, error) {
	identity, err := documents.DirectoryFingerprint(taskPath)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(taskPath, AgentRouterFileName)
	update := &workspaceTaskAgentsUpdate{taskPath: taskPath, taskIdentity: identity, path: path}
	if err := update.validatePath(); err != nil {
		return nil, err
	}
	file, err := openRecycleRegular(path)
	if os.IsNotExist(err) {
		return update, nil
	}
	if err != nil {
		return nil, fmt.Errorf("读取任务 AGENTS.md：%w", err)
	}
	defer file.Close()
	update.previous, err = file.Stat()
	if err != nil {
		return nil, err
	}
	update.original, err = io.ReadAll(file)
	return update, err
}

func (update *workspaceTaskAgentsUpdate) write(docs *AgentDocumentService, manifest domain.TaskManifest, config domain.AppConfig) error {
	checkPrevious := func() error {
		if err := update.validatePath(); err != nil {
			return err
		}
		current, err := os.Lstat(update.path)
		if update.previous == nil && os.IsNotExist(err) {
			return nil
		}
		if err == nil && update.previous != nil && sameModuleFileState(update.previous, current) {
			return nil
		}
		return errors.New("任务 AGENTS.md 已变化")
	}
	if err := checkPrevious(); err != nil {
		return err
	}
	content, err := docs.renderTaskAgentsUpdate(string(update.original), manifest, config, true)
	if err != nil {
		return err
	}
	next := []byte(content)
	if update.previous != nil && bytes.Equal(update.original, next) {
		return nil
	}
	if update.previous == nil {
		update.published, err = createMaterialsCatalogFile(update.path, next, checkPrevious)
	} else {
		err = writeMaterialsCatalogFile(update.path, update.original, next, false, checkPrevious)
	}
	if err != nil {
		return fmt.Errorf("更新任务 AGENTS.md：%w", err)
	}
	update.written = next
	return nil
}

func (update *workspaceTaskAgentsUpdate) rollback() error {
	if update.written == nil {
		return nil
	}
	if err := update.validatePath(); err != nil {
		return err
	}
	if update.previous != nil {
		checkPublished := func() error {
			if err := update.validatePath(); err != nil {
				return err
			}
			current, err := os.Lstat(update.path)
			if err != nil || !os.SameFile(current, update.previous) {
				return errModuleRemovalChanged
			}
			return nil
		}
		return writeMaterialsCatalogFile(update.path, update.written, update.original, false, checkPublished)
	}
	current, err := os.Lstat(update.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !os.SameFile(update.published, current) {
		return errModuleRemovalChanged
	}
	content, err := readRecycleRegular(update.path, int64(len(update.written)))
	if err != nil {
		return err
	}
	if !bytes.Equal(content, update.written) {
		return errModuleRemovalChanged
	}
	if check, err := os.Lstat(update.path); err != nil || !sameModuleFileState(current, check) {
		return errModuleRemovalChanged
	}
	return os.Remove(update.path)
}

func (update *workspaceTaskAgentsUpdate) validatePath() error {
	identity, err := documents.DirectoryFingerprint(update.taskPath)
	if err != nil || identity != update.taskIdentity {
		return errors.New("任务目录身份已变化，无法更新 AGENTS.md")
	}
	if err := materialsGitSafePath(update.taskPath, update.path, false); err != nil {
		return fmt.Errorf("任务 AGENTS.md 路径不安全：%w", err)
	}
	return nil
}
