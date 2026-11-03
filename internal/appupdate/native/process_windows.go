//go:build windows

package native

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

func hide(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}

func WaitParent(ctx context.Context, pid int) error {
	handle, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err == windows.ERROR_INVALID_PARAMETER {
		return nil
	}
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	for {
		status, err := windows.WaitForSingleObject(handle, 100)
		if err != nil {
			return err
		}
		if status == windows.WAIT_OBJECT_0 {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
}

func Lock(root string) (func(), error) {
	runtime.LockOSThread()
	digest := sha256.Sum256([]byte(strings.ToLower(root)))
	name, _ := windows.UTF16PtrFromString(fmt.Sprintf("Local\\Silverwing.Update.%x", digest[:16]))
	handle, err := windows.CreateMutex(nil, false, name)
	if err != nil && err != windows.ERROR_ALREADY_EXISTS {
		runtime.UnlockOSThread()
		return nil, err
	}
	status, e := windows.WaitForSingleObject(handle, 0)
	if e != nil || status != windows.WAIT_OBJECT_0 && status != windows.WAIT_ABANDONED {
		windows.CloseHandle(handle)
		runtime.UnlockOSThread()
		return nil, fmt.Errorf("另一个更新正在执行，请稍后重试")
	}
	return func() { windows.ReleaseMutex(handle); windows.CloseHandle(handle); runtime.UnlockOSThread() }, nil
}

func OtherInstances(executable string) error {
	handle, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	err = windows.Process32First(handle, &entry)
	for err == nil {
		process, e := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
		if e == nil {
			buffer := make([]uint16, 32768)
			size := uint32(len(buffer))
			if windows.QueryFullProcessImageName(process, 0, &buffer[0], &size) == nil && samePath(windows.UTF16ToString(buffer[:size]), executable) {
				windows.CloseHandle(process)
				return fmt.Errorf("请先关闭同一程序的其他实例")
			}
			windows.CloseHandle(process)
		} else if strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), filepath.Base(executable)) {
			return fmt.Errorf("无法检查同名程序是否仍在运行")
		}
		err = windows.Process32Next(handle, &entry)
	}
	if err != windows.ERROR_NO_MORE_FILES {
		return err
	}
	return nil
}
