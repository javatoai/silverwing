//go:build windows

package documents

import (
	"context"
	"fmt"
	"syscall"
	"unsafe"
)

var shellFileOperationW = syscall.NewLazyDLL("shell32.dll").NewProc("SHFileOperationW")

type shellFileOperation struct {
	Window          uintptr
	Function        uint32
	From            *uint16
	To              *uint16
	Flags           uint16
	AnyAborted      int32
	NameMappings    uintptr
	ProgressMessage *uint16
}

func newPlatformTrashBackend() trashBackend { return windowsTrashBackend{} }

type windowsTrashBackend struct{}

func (windowsTrashBackend) moveToTrash(ctx context.Context, path string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	absolute, err := canonicalTrashPath(path)
	if err != nil {
		return err
	}
	from, err := syscall.UTF16FromString(absolute)
	if err != nil {
		return err
	}
	from = append(from, 0) // SHFileOperation requires a double-NUL list.
	operation := shellFileOperation{
		Function: 3, // FO_DELETE
		From:     &from[0],
		Flags:    0x0040 | 0x0010 | 0x0004 | 0x0400, // ALLOWUNDO, NOCONFIRMATION, SILENT, NOERRORUI
	}
	result, _, callErr := shellFileOperationW.Call(uintptr(unsafe.Pointer(&operation)))
	if result != 0 {
		if callErr != syscall.Errno(0) {
			return fmt.Errorf("Explorer 回收失败：%w", callErr)
		}
		return fmt.Errorf("Explorer 回收失败（错误码 %d）", result)
	}
	if operation.AnyAborted != 0 {
		return ErrTrashCancelled
	}
	return nil
}
