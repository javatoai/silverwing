package storage

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type configBackupTrashError struct{ err error }

func (e *configBackupTrashError) Error() string {
	return fmt.Sprintf("无法移入废纸篓：%v", e.err)
}
func (e *configBackupTrashError) Unwrap() error { return e.err }

type configBackupExclusiveRename func(fromDir *os.File, from string, toDir *os.File, to string) error

// Stage the selected leaf under an unpredictable directory on the same volume,
// so the path-based trash operation cannot pick up a replacement at its old name.
func trashListedConfigBackup(ctx context.Context, root *os.Root, name string, expected os.FileInfo,
	rename configBackupExclusiveRename, move func(context.Context, string) (string, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := root.Lstat(name)
	if err != nil {
		return err
	}
	if !sameConfigBackup(expected, current) {
		return errors.New("备份身份已变化，请重新读取")
	}
	parent, err := root.Open(".")
	if err != nil {
		return err
	}
	defer parent.Close()
	parentInfo, err := parent.Stat()
	if err != nil {
		return err
	}
	var id [16]byte
	if _, err := rand.Read(id[:]); err != nil {
		return err
	}
	id[6] = id[6]&0x0f | 0x40
	id[8] = id[8]&0x3f | 0x80
	dir := fmt.Sprintf(".silverwing-trash-%x-%x-%x-%x-%x", id[:4], id[4:6], id[6:8], id[8:10], id[10:])
	if err := root.Mkdir(dir, 0o700); err != nil {
		return err
	}
	dirInfo, err := root.Lstat(dir)
	if err != nil || !dirInfo.IsDir() || configBackupReparse(dirInfo) {
		return errors.New("备份暂存目录已变化")
	}
	defer func() {
		if info, err := root.Lstat(dir); err == nil && os.SameFile(dirInfo, info) {
			_ = root.Remove(dir) // An interrupted operation can leave the ZIP here.
		}
	}()
	stage, err := root.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer stage.Close()
	stageDir, err := stage.Open(".")
	if err != nil {
		return err
	}
	defer stageDir.Close()
	if opened, err := stageDir.Stat(); err != nil || !os.SameFile(dirInfo, opened) {
		return errors.New("备份暂存目录已变化")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := rename(parent, name, stageDir, name); err != nil {
		return err
	}
	stagePath := filepath.Join(root.Name(), dir, name)
	staged, err := stage.Lstat(name)
	if err != nil {
		return fmt.Errorf("无法核对暂存文件，请检查 %s：%w", stagePath, err)
	}
	if !sameConfigBackup(expected, staged) {
		err = errors.New("备份在暂存期间发生变化，请重新读取")
	} else if !configBackupTrashDirectoryMatches(root.Name(), parentInfo) ||
		!configBackupTrashDirectoryMatches(filepath.Dir(stagePath), dirInfo) {
		err = errors.New("备份暂存路径已变化")
	} else if absoluteInfo, pathErr := os.Lstat(stagePath); pathErr != nil || !sameConfigBackup(expected, absoluteInfo) {
		err = errors.New("备份暂存文件已变化")
	} else {
		_, err = move(ctx, stagePath)
		if err == nil {
			if _, checkErr := stage.Lstat(name); errors.Is(checkErr, os.ErrNotExist) {
				return nil
			}
			err = errors.New("系统未确认暂存文件已移出")
		}
	}
	return restoreStagedConfigBackup(stage, staged, name, stagePath, stageDir, parent, rename, err)
}

func configBackupTrashDirectoryMatches(path string, expected os.FileInfo) bool {
	info, err := os.Lstat(path)
	return err == nil && info.IsDir() && !configBackupReparse(info) && os.SameFile(expected, info)
}

func restoreStagedConfigBackup(stage *os.Root, staged os.FileInfo, name, stagePath string, stageDir, parent *os.File,
	rename configBackupExclusiveRename, cause error) error {
	current, err := stage.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w。暂存文件已移出，请检查废纸篓", cause)
	}
	if err != nil || !os.SameFile(staged, current) {
		return fmt.Errorf("%w。无法核对暂存文件，请检查 %s", cause, stagePath)
	}
	if err := rename(stageDir, name, parent, name); err != nil {
		return fmt.Errorf("%w。无法移回原位（%v），文件保留在 %s", cause, err, stagePath)
	}
	return cause
}
