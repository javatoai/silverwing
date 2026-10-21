//go:build windows

package storage

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"unsafe"
)

var (
	kernel32LockFileEx   = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")
	kernel32UnlockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("UnlockFileEx")
)

const (
	lockfileExclusiveLock   = 0x00000002
	lockfileFailImmediately = 0x00000001
)

// AcquireLock uses an OS-managed exclusive byte-range lock. The lock is released
// by Windows when the process exits, even if cleanup code did not run.
func AcquireLock(path string) (func(), error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create lock directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	var overlapped syscall.Overlapped
	r1, _, callErr := kernel32LockFileEx.Call(
		uintptr(f.Fd()),
		uintptr(lockfileExclusiveLock|lockfileFailImmediately),
		0, 1, 0,
		uintptr(unsafe.Pointer(&overlapped)),
	)
	if r1 == 0 {
		_ = f.Close()
		if errors.Is(callErr, syscall.Errno(33)) || errors.Is(callErr, syscall.Errno(997)) {
			return nil, fmt.Errorf("lock %s: %w", path, ErrLockBusy)
		}
		return nil, fmt.Errorf("lock %s: %w", path, callErr)
	}
	var released bool
	release := func() {
		if released {
			return
		}
		released = true
		_, _, _ = kernel32UnlockFileEx.Call(uintptr(f.Fd()), 0, 1, 0, uintptr(unsafe.Pointer(&overlapped)))
		_ = f.Close()
	}
	return release, nil
}
