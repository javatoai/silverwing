//go:build windows

package integrations

import (
	"os"
	"syscall"
	"unsafe"
)

var requirementCacheGetFileAttributesW = syscall.NewLazyDLL("kernel32.dll").NewProc("GetFileAttributesW")

const requirementCacheReparseAttribute = 0x00000400

func requirementCacheReparsePoint(path string, info os.FileInfo) bool {
	if info.Mode()&os.ModeSymlink != 0 {
		return true
	}
	pathPointer, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return true
	}
	attributes, _, _ := requirementCacheGetFileAttributesW.Call(uintptr(unsafe.Pointer(pathPointer)))
	return attributes != ^uintptr(0) && attributes&requirementCacheReparseAttribute != 0
}
