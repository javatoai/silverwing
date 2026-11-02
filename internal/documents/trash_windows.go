//go:build windows

package documents

import (
	"context"
	"errors"
	"fmt"
	"golang.org/x/sys/windows"
	"runtime"
	"sync/atomic"
	"syscall"
	"unsafe"
)

var (
	trashOle             = windows.NewLazySystemDLL("ole32.dll")
	trashInitialize      = trashOle.NewProc("CoInitializeEx")
	trashUninitialize    = trashOle.NewProc("CoUninitialize")
	trashCreateInstance  = trashOle.NewProc("CoCreateInstance")
	trashCreateShellItem = windows.NewLazySystemDLL("shell32.dll").NewProc("SHCreateItemFromParsingName")
	trashClass           = mustTrashGUID("{3AD05575-8857-4850-9277-11B85BDB8E09}")
	trashOperationIID    = mustTrashGUID("{947AAB5F-0A5C-4C13-B4D6-4BF7836FC9F8}")
	trashShellItemIID    = mustTrashGUID("{43826D1E-E718-42EE-BC55-A1E261C37BFE}")
	trashSinkIID         = mustTrashGUID("{04B0F1A7-9490-44BC-96E1-4296A31252E2}")
	trashUnknownIID      = mustTrashGUID("{00000000-0000-0000-C000-000000000046}")
)

func mustTrashGUID(value string) windows.GUID {
	guid, err := windows.GUIDFromString(value)
	if err != nil {
		panic(err)
	}
	return guid
}

type trashCOM struct{ methods *[23]uintptr }

func (object *trashCOM) call(method int, args ...uintptr) uintptr {
	values := append([]uintptr{uintptr(unsafe.Pointer(object))}, args...)
	result, _, _ := syscall.SyscallN(object.methods[method], values...)
	return result
}
func trashHRESULT(result uintptr) error {
	if uint32(result)&0x80000000 == 0 {
		return nil
	}
	if uint32(result) == 0x800704c7 || uint32(result) == 0x80270000 {
		return ErrTrashCancelled
	}
	return fmt.Errorf("系统回收失败（HRESULT 0x%08X）", uint32(result))
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
	name, err := windows.UTF16PtrFromString(absolute)
	if err != nil {
		return err
	}
	sink := &trashProgressSink{methods: &trashProgressMethods, ctx: ctx}
	sink.references.Store(1)
	defer runtime.KeepAlive(sink)
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	hr, _, _ := trashInitialize.Call(0, 2)
	if err := trashHRESULT(hr); err != nil {
		return err
	}
	defer trashUninitialize.Call()
	var operation, item *trashCOM
	hr, _, _ = trashCreateInstance.Call(uintptr(unsafe.Pointer(&trashClass)), 0, 1, uintptr(unsafe.Pointer(&trashOperationIID)), uintptr(unsafe.Pointer(&operation)))
	if err := trashHRESULT(hr); err != nil {
		return err
	}
	defer operation.call(2)
	hr, _, _ = trashCreateShellItem.Call(uintptr(unsafe.Pointer(name)), 0, uintptr(unsafe.Pointer(&trashShellItemIID)), uintptr(unsafe.Pointer(&item)))
	if err := trashHRESULT(hr); err != nil {
		return err
	}
	defer item.call(2)
	// Force recycle, fail on errors, and veto all non-recycle deletes in the sink.
	if err := trashHRESULT(operation.call(5, 0x80000|0x100000|0x4000|0x40|0x10|0x4|0x400)); err != nil {
		return err
	}
	if err := trashHRESULT(operation.call(18, uintptr(unsafe.Pointer(item)), uintptr(unsafe.Pointer(sink)))); err != nil {
		return err
	}
	performErr := trashHRESULT(operation.call(21))
	var aborted int32
	abortErr := trashHRESULT(operation.call(22, uintptr(unsafe.Pointer(&aborted))))
	if sink.failure != nil {
		return sink.failure
	}
	if performErr != nil {
		return performErr
	}
	if abortErr != nil {
		return abortErr
	}
	if aborted != 0 {
		return ErrTrashCancelled
	}
	if !sink.recycled {
		return errors.New("系统未确认项目已进入回收站，操作已停止")
	}
	return nil
}

type trashProgressSink struct {
	methods    *[19]uintptr
	references atomic.Int32
	ctx        context.Context
	failure    error
	recycled   bool
}

var trashProgressMethods = [19]uintptr{
	syscall.NewCallback(func(sink *trashProgressSink, iid *windows.GUID, result **trashProgressSink) uintptr {
		guid := *iid
		if guid != trashSinkIID && guid != trashUnknownIID {
			*result = nil
			return 0x80004002
		}
		*result = sink
		sink.references.Add(1)
		return 0
	}),
	syscall.NewCallback(func(sink *trashProgressSink) uintptr { return uintptr(sink.references.Add(1)) }),
	syscall.NewCallback(func(sink *trashProgressSink) uintptr { return uintptr(sink.references.Add(-1)) }),
	syscall.NewCallback(func(self uintptr) uintptr { return 0 }),
	syscall.NewCallback(func(self, result uintptr) uintptr { return 0 }),
	syscall.NewCallback(func(self, flags, item, name uintptr) uintptr { return 0 }),
	syscall.NewCallback(func(self, flags, item, name, result, created uintptr) uintptr { return 0 }),
	syscall.NewCallback(func(self, flags, item, dest, name uintptr) uintptr { return 0 }),
	syscall.NewCallback(func(self, flags, item, dest, name, result, created uintptr) uintptr { return 0 }),
	syscall.NewCallback(func(self, flags, item, dest, name uintptr) uintptr { return 0 }),
	syscall.NewCallback(func(self, flags, item, dest, name, result, created uintptr) uintptr { return 0 }),
	syscall.NewCallback(func(sink *trashProgressSink, flags, item uintptr) uintptr {
		if err := sink.ctx.Err(); err != nil {
			sink.failure = err
			return 0x800704c7
		}
		if flags&0x80 == 0 {
			sink.failure = errors.New("该位置不支持系统回收，已阻止永久删除")
			return 0x80004004
		}
		return 0
	}),
	syscall.NewCallback(func(sink *trashProgressSink, flags, item, result, created uintptr) uintptr {
		if err := trashHRESULT(result); err != nil {
			sink.failure = err
			return 0x80004004
		}
		if created == 0 {
			sink.failure = errors.New("系统未返回回收站项目")
			return 0x80004004
		}
		sink.recycled = true
		return 0
	}),
	syscall.NewCallback(func(self, flags, dest, name uintptr) uintptr { return 0 }),
	syscall.NewCallback(func(self, flags, dest, name, template, attributes, result, created uintptr) uintptr { return 0 }),
	syscall.NewCallback(func(self, total, completed uintptr) uintptr { return 0 }),
	syscall.NewCallback(func(self uintptr) uintptr { return 0 }),
	syscall.NewCallback(func(self uintptr) uintptr { return 0 }),
	syscall.NewCallback(func(self uintptr) uintptr { return 0 }),
}
