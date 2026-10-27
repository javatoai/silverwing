package workspace

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var errMaterialsCatalogChanged = errors.New("说明文件内容已被外部修改，原文已保留；请刷新资料单独重试")
var errMaterialsCatalogLinked = errors.New("说明文件存在多个硬链接，自动更新会修改其他用户文件，原文已保留；请解除硬链接后刷新资料单独重试")

func readMaterialsCatalogFile(file *os.File) ([]byte, error) {
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(file)
}

// Windows denies external writers and replacement for this handle's lifetime.
// Unix uses an inode advisory lock; non-cooperating external writers can bypass
// flock. Recheck both content and path immediately before writing on every OS,
// and retain a durable original for recovery rather than claiming portable CAS.
func writeMaterialsCatalogFile(path string, before, next []byte, missing bool, check func() error) error {
	if missing {
		return createMaterialsCatalogFile(path, next, check)
	}
	file, release, err := openMaterialsCatalogForUpdate(path)
	if err != nil {
		return fmt.Errorf("无法独占更新说明，原文已保留，请重试：%w", err)
	}
	defer release()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return errors.Join(errors.New("说明不是安全的普通文件"), err)
	}
	verify := func() error {
		if err := checkMaterialsCatalogScope(check); err != nil {
			return err
		}
		currentInfo, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if !currentInfo.Mode().IsRegular() || !os.SameFile(info, currentInfo) {
			return errMaterialsCatalogChanged
		}
		current, err := readMaterialsCatalogFile(file)
		if err != nil {
			return err
		}
		if !bytes.Equal(current, before) {
			return errMaterialsCatalogChanged
		}
		links, err := materialsCatalogLinkCount(file)
		if err != nil {
			return fmt.Errorf("无法核对说明文件硬链接，原文已保留：%w", err)
		}
		if links > 1 {
			return errMaterialsCatalogLinked
		}
		return nil
	}
	if err := verify(); err != nil {
		return err
	}
	return replaceMaterialsCatalogContents(path, file, before, next, verify)
}

// Publish a complete new document without reserving the target with an empty
// file. Linking never replaces a file an external editor created in the
// meantime, and failure cleanup only removes this operation's temporary path.
func createMaterialsCatalogFile(path string, next []byte, check func() error) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".silverwing-materials-*.new")
	if err != nil {
		return err
	}
	defer file.Close()
	defer os.Remove(file.Name())
	if err := writeMaterialsCatalogContents(file, next); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := checkMaterialsCatalogScope(check); err != nil {
		return err
	}
	if err := os.Link(file.Name(), path); err != nil {
		if os.IsExist(err) {
			return errMaterialsCatalogChanged
		}
		return err
	}
	return nil
}

type materialsCatalogWritable interface {
	WriteAt([]byte, int64) (int, error)
	Truncate(int64) error
	Sync() error
}

func writeMaterialsCatalogContents(file materialsCatalogWritable, content []byte) error {
	n, err := file.WriteAt(content, 0)
	if err != nil {
		return err
	}
	if n != len(content) {
		return io.ErrShortWrite
	}
	if err := file.Truncate(int64(len(content))); err != nil {
		return err
	}
	return file.Sync()
}

// The backup is complete and durable before the first byte of the original is
// touched. An update failure rolls back under the same document lock and keeps
// that backup even if restoring the original also fails.
func replaceMaterialsCatalogContents(path string, file materialsCatalogWritable, before, next []byte, verify func() error) error {
	backup, err := os.CreateTemp(filepath.Dir(path), ".silverwing-materials-*.bak")
	if err != nil {
		return err
	}
	backupPath := backup.Name()
	n, err := backup.Write(before)
	if err == nil && n != len(before) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = backup.Sync()
	}
	err = errors.Join(err, backup.Close())
	if err != nil {
		_ = os.Remove(backupPath)
		return err
	}
	if err := verify(); err != nil {
		_ = os.Remove(backupPath)
		return err
	}
	if err := writeMaterialsCatalogContents(file, next); err != nil {
		restoreErr := writeMaterialsCatalogContents(file, before)
		return fmt.Errorf("更新说明失败；已尝试恢复原文，恢复副本保留于 %s：%w", backupPath, errors.Join(err, restoreErr))
	}
	_ = os.Remove(backupPath)
	return nil
}
