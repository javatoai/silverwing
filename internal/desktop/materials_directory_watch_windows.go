//go:build windows

package desktop

import (
	"context"
	"encoding/binary"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"unsafe"
)

var materialsWatchKernel = syscall.NewLazyDLL("kernel32.dll")
var materialsWatchCreateEvent = materialsWatchKernel.NewProc("CreateEventW")
var materialsWatchGetResult = materialsWatchKernel.NewProc("GetOverlappedResult")

func watchMaterialsDirectoryHandle(ctx context.Context, root string, recursive bool, changed func(string), ready func()) error {
	return watchMaterialsDirectoryHandleInstalled(ctx, root, recursive, changed, ready, nil)
}

func watchMaterialsDirectoryHandleInstalled(ctx context.Context, root string, recursive bool, changed func(string), ready func(), installed func(syscall.ByHandleFileInformation)) error {
	var readyOnce sync.Once
	signalReady := func() { readyOnce.Do(ready) }
	defer signalReady()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := materialsWindowsWatchPathSafe(root); err != nil {
		return err
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	if normalizedReadingPath(resolved) != normalizedReadingPath(root) {
		return errors.New("资料观察目录及祖先不能是符号链接或 junction")
	}
	path := root
	if len(path) >= 248 && filepath.IsAbs(path) && !strings.HasPrefix(path, `\\?\`) {
		path = `\\?\` + path
	}
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	handle, err := syscall.CreateFile(name, syscall.FILE_LIST_DIRECTORY,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE, nil,
		syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS|syscall.FILE_FLAG_OVERLAPPED|syscall.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return err
	}
	defer syscall.CloseHandle(handle)
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(handle, &info); err != nil {
		return err
	}
	if info.FileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return errors.New("资料观察根不是目录")
	}
	if info.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("资料观察目录不能是 reparse point")
	}
	if err := materialsWindowsWatchHandlePathSafe(handle, root); err != nil {
		return err
	}
	event, _, createErr := materialsWatchCreateEvent.Call(0, 0, 0, 0)
	if event == 0 {
		return createErr
	}
	defer syscall.CloseHandle(syscall.Handle(event))
	buffer := make([]byte, 64*1024)
	mask := uint32(syscall.FILE_NOTIFY_CHANGE_FILE_NAME | syscall.FILE_NOTIFY_CHANGE_DIR_NAME | syscall.FILE_NOTIFY_CHANGE_ATTRIBUTES)
	if recursive {
		mask |= syscall.FILE_NOTIFY_CHANGE_SIZE | syscall.FILE_NOTIFY_CHANGE_LAST_WRITE
	}
	first := true
	for ctx.Err() == nil {
		overlap := syscall.Overlapped{HEvent: syscall.Handle(event)}
		var count uint32
		err := syscall.ReadDirectoryChanges(handle, &buffer[0], uint32(len(buffer)), recursive, mask, &count, &overlap, 0)
		if err != nil && err != syscall.ERROR_IO_PENDING {
			return err
		}
		if first {
			if installed != nil {
				installed(info)
			}
			signalReady()
			first = false
		}
		for {
			if ctx.Err() != nil {
				_ = syscall.CancelIoEx(handle, &overlap)
				// Complete cancellation before releasing buffer/OVERLAPPED memory.
				materialsWatchGetResult.Call(uintptr(handle), uintptr(unsafe.Pointer(&overlap)), uintptr(unsafe.Pointer(&count)), 1)
				return ctx.Err()
			}
			status, waitErr := syscall.WaitForSingleObject(syscall.Handle(event), 200)
			if waitErr != nil {
				_ = syscall.CancelIoEx(handle, &overlap)
				materialsWatchGetResult.Call(uintptr(handle), uintptr(unsafe.Pointer(&overlap)), uintptr(unsafe.Pointer(&count)), 1)
				return waitErr
			}
			if status == syscall.WAIT_TIMEOUT {
				continue
			}
			ok, _, resultErr := materialsWatchGetResult.Call(uintptr(handle), uintptr(unsafe.Pointer(&overlap)), uintptr(unsafe.Pointer(&count)), 0)
			if ok == 0 {
				return resultErr
			}
			break
		}
		if count == 0 || count > uint32(len(buffer)) {
			changed("")
			continue
		}
		for offset := uint32(0); offset+12 <= count; {
			next := binary.LittleEndian.Uint32(buffer[offset : offset+4])
			length := binary.LittleEndian.Uint32(buffer[offset+8 : offset+12])
			if length%2 != 0 || length > count-offset-12 {
				changed("")
				break
			}
			runes := make([]uint16, length/2)
			for i := range runes {
				runes[i] = binary.LittleEndian.Uint16(buffer[offset+12+uint32(i)*2:])
			}
			changed(syscall.UTF16ToString(runes))
			if next == 0 {
				break
			}
			if next < 12 || next > count-offset {
				changed("")
				break
			}
			offset += next
		}
	}
	return ctx.Err()
}
