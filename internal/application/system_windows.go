//go:build windows

package application

import (
	"context"
	"fmt"
	"runtime"
	"syscall"
)

var shellExecute = syscall.NewLazyDLL("shell32.dll").NewProc("ShellExecuteW")

func openNative(path string, reveal bool) error {
	file := path
	params := ""
	if reveal {
		file = "explorer.exe"
		params = `/select,"` + path + `"`
	}
	verb, _ := syscall.UTF16PtrFromString("open")
	name, e := syscall.UTF16PtrFromString(file)
	if e != nil {
		return e
	}
	args, e := syscall.UTF16PtrFromString(params)
	if e != nil {
		return e
	}
	result, _, _ := shellExecute.Call(0, uintptr(unsafePointer(verb)), uintptr(unsafePointer(name)), uintptr(unsafePointer(args)), 0, 1)
	if result <= 32 {
		return fmt.Errorf("系统打开失败：%d", result)
	}
	return nil
}
func launchTerminal(ctx context.Context, resolution SystemTerminalResolution, directory string) error {
	plan, err := PlanSystemTerminalLaunch(runtime.GOOS, resolution, directory)
	if err != nil {
		return err
	}
	return startWindowsLaunchPlan(ctx, plan, true)
}
func launchSystemTerminalPlan(ctx context.Context, plan SystemLaunchPlan) error {
	return startWindowsLaunchPlan(ctx, plan, true)
}

func startWindowsLaunchPlan(ctx context.Context, plan SystemLaunchPlan, terminal bool) error {
	cmd, err := detachedToolCommand(ctx, plan.Executable, plan.Arguments)
	if err != nil {
		return err
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	if terminal {
		cmd.SysProcAttr.CreationFlags = 0x00000010
	} else {
		cmd.SysProcAttr.HideWindow = true
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
