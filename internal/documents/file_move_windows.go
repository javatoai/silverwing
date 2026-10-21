//go:build windows

package documents

import (
	"fmt"
	"syscall"
	"unsafe"
)

var moveFileW = syscall.NewLazyDLL("kernel32.dll").NewProc("MoveFileW")

func renameNoReplace(source, destination string) error {
	sourcePath, err := syscall.UTF16PtrFromString(source)
	if err != nil {
		return err
	}
	destinationPath, err := syscall.UTF16PtrFromString(destination)
	if err != nil {
		return err
	}
	result, _, callErr := moveFileW.Call(uintptr(unsafe.Pointer(sourcePath)), uintptr(unsafe.Pointer(destinationPath)))
	if result == 0 {
		return fmt.Errorf("rename %s: %w", destination, callErr)
	}
	return nil
}
