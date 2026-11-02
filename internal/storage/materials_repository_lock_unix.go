//go:build !windows

package storage

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func retireUnusedMaterialsLock(path string, before os.FileInfo) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return fmt.Errorf("旧版本资料操作仍占用运行锁，请稍后重试：%w", ErrLockBusy)
		}
		return err
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, opened) || !os.SameFile(opened, current) {
		return fmt.Errorf("旧资料运行锁身份已变化，请重试：%s", path)
	}
	if opened.Size() != 0 {
		return nil // User content is retained, even at this historical filename.
	}
	return os.Remove(path) // Keep the old inode locked until removal completes.
}
