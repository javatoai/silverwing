//go:build windows

package codexprotocol

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"silverwing-go/internal/process"
)

type commandRunner interface {
	Run(context.Context, process.Spec) (process.Result, error)
}

type nativeSystem struct {
	backups      string
	runner       commandRunner
	registryPath string
}

func Open(ctx context.Context, uri, backups string, runner commandRunner) error {
	return open(ctx, uri, nativeSystem{backups: backups, runner: runner, registryPath: `Software\Classes\codex`})
}

var (
	shell             = windows.NewLazySystemDLL("shell32.dll")
	shellExecute      = shell.NewProc("ShellExecuteExW")
	notifyAssociation = shell.NewProc("SHChangeNotify")
	com               = windows.NewLazySystemDLL("ole32.dll")
	initializeCOM     = com.NewProc("CoInitializeEx")
	uninitializeCOM   = com.NewProc("CoUninitialize")
)

type shellExecuteInfo struct {
	Size, Mask                        uint32
	Window                            uintptr
	Verb, File, Parameters, Directory *uint16
	Show                              int32
	Instance, IDList, Class, ClassKey uintptr
	HotKey                            uint32
	Monitor, Process                  uintptr
}

func (nativeSystem) activate(ctx context.Context, uri string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	initialized, _, _ := initializeCOM.Call(0, 0x6) // STA, disable OLE1 DDE.
	if int32(initialized) < 0 {
		return fmt.Errorf("初始化 Windows 协议服务失败（0x%08X）", uint32(initialized))
	}
	defer uninitializeCOM.Call()
	if err := ctx.Err(); err != nil {
		return err
	}
	file, err := windows.UTF16PtrFromString(uri)
	if err != nil {
		return err
	}
	verb, _ := windows.UTF16PtrFromString("open")
	info := shellExecuteInfo{Mask: 0x400 | 0x100, File: file, Verb: verb, Show: 1}
	info.Size = uint32(unsafe.Sizeof(info))
	accepted, _, callErr := shellExecute.Call(uintptr(unsafe.Pointer(&info)))
	runtime.KeepAlive(file)
	runtime.KeepAlive(verb)
	if accepted != 0 {
		return nil
	}
	code, _ := callErr.(syscall.Errno)
	if code == 0 {
		if info.Instance == 31 { // SE_ERR_NOASSOC, not Win32 ERROR_GEN_FAILURE.
			return fmt.Errorf("%w（ShellExecute 无可用关联）", errAssociationMissing)
		}
		code = syscall.Errno(info.Instance)
	}
	return activationError(code)
}

func activationError(code syscall.Errno) error {
	switch code {
	case 2, 3, 1155:
		return fmt.Errorf("%w（Windows 错误 %d）", errAssociationMissing, code)
	case 1223:
		return errors.New("Codex 打开操作已取消")
	default:
		return fmt.Errorf("Windows 未接受 Codex 打开请求（错误 %d）：%w", code, code)
	}
}

func (nativeSystem) lock(ctx context.Context) (func(), error) {
	runtime.LockOSThread()
	owned := false
	defer func() {
		if !owned {
			runtime.UnlockOSThread()
		}
	}()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	name, err := windows.UTF16PtrFromString(`Local\Silverwing-CodexProtocol-` + user.User.Sid.String())
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateMutex(nil, false, name)
	if err != nil && !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
		return nil, err
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if err := ctx.Err(); err != nil {
			windows.CloseHandle(handle)
			return nil, err
		}
		state, err := windows.WaitForSingleObject(handle, 100)
		if err != nil {
			windows.CloseHandle(handle)
			return nil, err
		}
		if state == windows.WAIT_OBJECT_0 || state == windows.WAIT_ABANDONED {
			owned = true
			return func() { windows.ReleaseMutex(handle); windows.CloseHandle(handle); runtime.UnlockOSThread() }, nil
		}
		if time.Now().After(deadline) {
			windows.CloseHandle(handle)
			return nil, errors.New("其他窗口正在修复 Codex 协议，请稍后重试")
		}
	}
}
