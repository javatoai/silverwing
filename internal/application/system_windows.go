//go:build windows

package application

import (
	"fmt"
	"runtime"
	"syscall"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
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
func launchTerminal(resolution SystemTerminalResolution, directory string) error {
	plan, err := PlanSystemTerminalLaunch(runtime.GOOS, resolution, directory)
	if err != nil {
		return err
	}
	return startWindowsLaunchPlan(plan, true)
}
func launchSystemTerminalPlan(plan SystemLaunchPlan) error {
	return startWindowsLaunchPlan(plan, true)
}
func launchTool(toolType domain.DevelopmentToolType, executable, path string) error {
	plan, err := PlanSystemToolLaunch(runtime.GOOS, toolType, executable, path)
	if err != nil {
		return err
	}
	return startWindowsLaunchPlan(plan, false)
}
func startWindowsLaunchPlan(plan SystemLaunchPlan, terminal bool) error {
	cmd := process.NewDetachedCommand(plan.Executable, plan.Arguments...)
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
func launchToolArguments(executable string, arguments ...string) error {
	cmd := process.NewDetachedCommand(executable, arguments...)
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
