//go:build windows

package storage

import (
	"errors"
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

func retireUnusedMaterialsLock(path string, before os.FileInfo) error {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	// Permit deleting our own probe handle while its byte-range lock is held.
	// An old process's handle still denies deletion and/or owns that same lock.
	handle, err := syscall.CreateFile(name, syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil,
		syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL|0x00200000, 0)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(handle), path)
	defer file.Close()
	var overlapped syscall.Overlapped
	locked, _, callErr := kernel32LockFileEx.Call(uintptr(handle), uintptr(lockfileExclusiveLock|lockfileFailImmediately), 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	if locked == 0 {
		if errors.Is(callErr, syscall.Errno(33)) || errors.Is(callErr, syscall.Errno(997)) {
			return fmt.Errorf("旧版本资料操作仍占用运行锁，请稍后重试：%w", ErrLockBusy)
		}
		return callErr
	}
	defer kernel32UnlockFileEx.Call(uintptr(handle), 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
	opened, err := file.Stat()
	if err != nil {
		return err
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, opened) || !os.SameFile(opened, current) {
		return fmt.Errorf("旧资料运行锁身份已变化，请重试：%s", path)
	}
	if opened.Size() != 0 {
		return nil
	}
	return os.Remove(path)
}
