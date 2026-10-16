//go:build windows

package mygo

import (
	"os"
	"syscall"
	"unsafe"
)

func fileLinks(os.FileInfo) int { return 1 }

// fileWritable reports whether the file at path lacks the read-only
// attribute, as Notepad checks it.
func fileWritable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().Perm()&0o200 != 0
}

var procLockFileEx = syscall.NewLazyDLL("kernel32.dll").NewProc("LockFileEx")

// lockFile takes an exclusive lock on f, which fails while another
// process holds it, until f is closed.
func lockFile(f *os.File) error {
	const lockfileFailImmediately, lockfileExclusiveLock = 1, 2
	var ol syscall.Overlapped
	r, _, err := procLockFileEx.Call(f.Fd(), lockfileFailImmediately|lockfileExclusiveLock, 0, 1, 0, uintptr(unsafe.Pointer(&ol)))
	if r == 0 {
		return err
	}
	return nil
}
