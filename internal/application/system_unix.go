//go:build !windows

package application

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"silverwing-go/internal/executable"

	"silverwing-go/internal/domain"
)

func openNative(path string, reveal bool) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		if reveal {
			cmd = exec.Command(executable.SystemCommand("open"), "-R", path)
		} else {
			cmd = exec.Command(executable.SystemCommand("open"), path)
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
func launchTerminal(ctx context.Context, resolution SystemTerminalResolution, directory string) error {
	plan, err := PlanSystemTerminalLaunch(runtime.GOOS, resolution, directory)
	if err != nil {
		return err
	}
	return startUnixLaunchPlan(ctx, plan)
}
func launchSystemTerminalPlan(ctx context.Context, plan SystemLaunchPlan) error {
	return startUnixLaunchPlan(ctx, plan)
}
func launchTool(ctx context.Context, toolType domain.DevelopmentToolType, executable, path string) error {
	plan, err := PlanSystemToolLaunch(runtime.GOOS, toolType, executable, path)
	if err != nil {
		return err
	}
	return startUnixLaunchPlan(ctx, plan)
}
func startUnixLaunchPlan(ctx context.Context, plan SystemLaunchPlan) error {
	cmd, err := detachedToolCommand(ctx, plan.Executable, plan.Arguments)
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
func launchToolArguments(ctx context.Context, executable string, arguments ...string) error {
	cmd, err := detachedToolCommand(ctx, executable, arguments)
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	return nil
}
