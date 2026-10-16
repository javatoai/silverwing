//go:build !windows

package application

import (
	"os"
	"os/exec"
	"runtime"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

func openNative(path string, reveal bool) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		if reveal {
			cmd = exec.Command("open", "-R", path)
		} else {
			cmd = exec.Command("open", path)
		}
	} else {
		if reveal {
			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			path = revealDirectoryTarget(path, info)
		}
		cmd = exec.Command("xdg-open", path)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
func launchTerminal(resolution SystemTerminalResolution, directory string) error {
	plan, err := PlanSystemTerminalLaunch(runtime.GOOS, resolution, directory)
	if err != nil {
		return err
	}
	return startUnixLaunchPlan(plan)
}
func launchSystemTerminalPlan(plan SystemLaunchPlan) error {
	return startUnixLaunchPlan(plan)
}
func launchTool(toolType domain.DevelopmentToolType, executable, path string) error {
	plan, err := PlanSystemToolLaunch(runtime.GOOS, toolType, executable, path)
	if err != nil {
		return err
	}
	return startUnixLaunchPlan(plan)
}
func startUnixLaunchPlan(plan SystemLaunchPlan) error {
	cmd := process.NewDetachedCommand(plan.Executable, plan.Arguments...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
func launchToolArguments(executable string, arguments ...string) error {
	cmd := process.NewDetachedCommand(executable, arguments...)
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
