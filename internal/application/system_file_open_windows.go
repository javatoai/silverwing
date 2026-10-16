//go:build windows

package application

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

const (
	windowsNoAssociation = 1155
	windowsFileNotFound  = 2
	windowsCancelled     = 1223
	assocProgid          = 20
	assocFriendlyAppName = 4
	assocQueryFlags      = 0x120 // ASSOCF_NOTRUNCATE | ASSOCF_NOFIXUPS
	seeMaskFlagNoUI      = 0x400
	seeMaskNoAsync       = 0x100
	openAsExecute        = 0x4 // OAIF_EXEC: open this file without changing the default association.
)

var (
	shellExecuteExW = syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteExW")
	openWithDialog  = syscall.NewLazyDLL("shell32.dll").NewProc("SHOpenWithDialog")
	assocQueryW     = syscall.NewLazyDLL("shlwapi.dll").NewProc("AssocQueryStringW")
	coInitializeEx  = syscall.NewLazyDLL("ole32.dll").NewProc("CoInitializeEx")
	coUninitialize  = syscall.NewLazyDLL("ole32.dll").NewProc("CoUninitialize")
)

func (nativeSystemFileOpening) status(path string) (FileOpeningStatus, error) {
	return withWindowsSTA(func() (FileOpeningStatus, error) {
		fileName := filepath.Base(path)
		dot := strings.LastIndex(fileName, ".")
		if dot < 0 || dot == len(fileName)-1 {
			return FileOpeningStatus{AssociationMissing: true, ChooserAvailable: true}, nil
		}
		extension := fileName[dot:]
		progid, queryError := queryWindowsAssociation(extension, assocProgid)
		missing := strings.EqualFold(progid, "Unknown") || queryError == windowsNoAssociation || queryError == windowsFileNotFound
		nameAssociation := progid
		if nameAssociation == "" {
			nameAssociation = extension
		}
		appName, _ := queryWindowsAssociation(nameAssociation, assocFriendlyAppName)
		if missing {
			appName = ""
		}
		return FileOpeningStatus{DisplayName: appName, AssociationMissing: missing, ChooserAvailable: true}, nil
	})
}

func (nativeSystemFileOpening) open(ctx context.Context, path string) (systemFileOpenOutcome, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return withWindowsSTA(func() (systemFileOpenOutcome, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		verb, err := syscall.UTF16PtrFromString("open")
		if err != nil {
			return 0, err
		}
		file, err := syscall.UTF16PtrFromString(path)
		if err != nil {
			return 0, err
		}
		info := windowsShellExecuteInfo{
			fMask:  seeMaskFlagNoUI | seeMaskNoAsync,
			lpVerb: verb,
			lpFile: file,
			nShow:  1,
		}
		info.cbSize = uint32(unsafe.Sizeof(info))
		result, _, callErr := shellExecuteExW.Call(uintptr(unsafe.Pointer(&info)))
		runtime.KeepAlive(verb)
		runtime.KeepAlive(file)
		if result != 0 {
			return systemFileOpenSubmitted, nil
		}
		code := uint32(0)
		if lastError, ok := callErr.(syscall.Errno); ok {
			code = uint32(lastError)
		}
		switch code {
		case windowsNoAssociation:
			return systemFileOpenNeedsChooser, nil
		case windowsCancelled:
			return systemFileOpenCancelled, nil
		default:
			return 0, fmt.Errorf("系统无法打开文件（错误 %d），请尝试其他应用", code)
		}
	})
}

func (nativeSystemFileOpening) choose(path string) (systemFileOpenOutcome, error) {
	return withWindowsSTA(func() (systemFileOpenOutcome, error) { return chooseWindowsFileOnSTA(path) })
}

func chooseWindowsFileOnSTA(path string) (systemFileOpenOutcome, error) {
	file, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	info := windowsOpenAsInfo{file: file, flags: openAsExecute}
	result, _, _ := openWithDialog.Call(0, uintptr(unsafe.Pointer(&info)))
	runtime.KeepAlive(file)
	if uint32(result) == 0 {
		return systemFileOpenSubmitted, nil
	}
	if uint32(result) == 0x800704C7 {
		return systemFileOpenCancelled, nil
	}
	return 0, fmt.Errorf("无法选择打开应用（错误 0x%08X）", uint32(result))
}

func withWindowsSTA[T any](operation func() (T, error)) (result T, err error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	initialized, _, _ := coInitializeEx.Call(0, 0x2|0x4) // COINIT_APARTMENTTHREADED | COINIT_DISABLE_OLE1DDE
	if int32(uint32(initialized)) < 0 {
		return result, fmt.Errorf("无法初始化系统文件打开服务（HRESULT 0x%08X）", uint32(initialized))
	}
	defer coUninitialize.Call()
	return operation()
}

func queryWindowsAssociation(association string, kind uint32) (string, uint32) {
	name, err := syscall.UTF16PtrFromString(association)
	if err != nil {
		return "", 0
	}
	length := uint32(260)
	for attempt := 0; attempt < 4; attempt++ {
		buffer := make([]uint16, length)
		outputLength := length
		result, _, _ := assocQueryW.Call(
			uintptr(assocQueryFlags),
			uintptr(kind),
			uintptr(unsafe.Pointer(name)),
			0,
			uintptr(unsafe.Pointer(&buffer[0])),
			uintptr(unsafe.Pointer(&outputLength)),
		)
		runtime.KeepAlive(name)
		runtime.KeepAlive(buffer)
		if result == 0 {
			return syscall.UTF16ToString(buffer), 0
		}
		code := uint32(result) & 0xFFFF
		if outputLength > length && outputLength <= 32768 {
			length = outputLength
			continue
		}
		if uint32(result) == 1 && length < 32768 {
			length *= 2
			if length > 32768 {
				length = 32768
			}
			continue
		}
		return "", code
	}
	return "", 0
}

type windowsShellExecuteInfo struct {
	cbSize       uint32
	fMask        uint32
	hwnd         uintptr
	lpVerb       *uint16
	lpFile       *uint16
	lpParameters *uint16
	lpDirectory  *uint16
	nShow        int32
	hInstApp     uintptr
	lpIDList     uintptr
	lpClass      *uint16
	hkeyClass    uintptr
	dwHotKey     uint32
	hMonitor     uintptr
	hProcess     uintptr
}

type windowsOpenAsInfo struct {
	file  *uint16
	class *uint16
	flags uint32
}
