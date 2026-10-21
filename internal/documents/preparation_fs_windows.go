//go:build windows

package documents

import (
	"os"
	"syscall"
	"unsafe"
)

var kernel32GetFileAttributesW = syscall.NewLazyDLL("kernel32.dll").NewProc("GetFileAttributesW")

const fileAttributeReparsePoint = 0x00000400

func isUnsafeDirectory(info os.FileInfo, path string) bool {
	if info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	pathPtr, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return true
	}
	attributes, _, _ := kernel32GetFileAttributesW.Call(uintptr(unsafe.Pointer(pathPtr)))
	if attributes == ^uintptr(0) {
		return false
	}
	return attributes&fileAttributeReparsePoint != 0
}
