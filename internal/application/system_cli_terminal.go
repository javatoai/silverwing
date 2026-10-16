package application

import (
	"context"
	"errors"
	"path"
	"runtime"
	"strings"
)

// PlanSystemCLITerminalLaunch opens an OS terminal and invokes one executable
// without arguments, using the same OS default as workspace terminals.
// Source: DesktopIntegration.kt:117-150,220-264,282-310.
func PlanSystemCLITerminalLaunch(osName, command string, _ bool) (SystemLaunchPlan, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return SystemLaunchPlan{}, errors.New("CLI 命令路径不能为空")
	}
	if strings.ContainsRune(command, 0) {
		return SystemLaunchPlan{}, errors.New("CLI 命令路径无效")
	}
	directory, err := cliTerminalCommandDirectory(osName, command)
	if err != nil {
		return SystemLaunchPlan{}, err
	}
	switch {
	case isWindowsOS(osName):
		script := "& " + powerShellLiteral(command)
		if directory != "" {
			script = "Set-Location -LiteralPath " + powerShellLiteral(directory) + "; " + script
		}
		return SystemLaunchPlan{Executable: "powershell.exe", Arguments: []string{"-NoProfile", "-NoExit", "-Command", script}}, nil
	case isMacOS(osName):
		script := posixTerminalLiteral(command)
		if directory != "" {
			script = "cd " + posixTerminalLiteral(directory) + " && " + script
		}
		script = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\r", `\r`, "\n", `\n`).Replace(script)
		return SystemLaunchPlan{Executable: "osascript", Arguments: []string{"-e", `tell application "Terminal" to do script "` + script + `"`}}, nil
	default:
		script := posixTerminalLiteral(command)
		if directory != "" {
			script = "cd " + posixTerminalLiteral(directory) + " && " + script
		}
		script += `; printf '\nPress Enter to close...'; read -r`
		return SystemLaunchPlan{Executable: "x-terminal-emulator", Arguments: []string{"-e", "/bin/sh", "-c", script}}, nil
	}
}

func cliTerminalCommandDirectory(osName, command string) (string, error) {
	if isWindowsOS(osName) && isWindowsAbsolutePath(command) {
		at := strings.LastIndexAny(command, `/\`)
		parent := command[:at]
		if at == 2 && command[1] == ':' {
			parent = command[:at+1]
		}
		return absoluteLaunchPath(osName, parent)
	}
	if !isWindowsOS(osName) && strings.HasPrefix(command, "/") {
		return path.Dir(path.Clean(command)), nil
	}
	return "", nil
}

func posixTerminalLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func (a *App) systemCLITerminal(ctx context.Context, action Action) (ActionResult, error) {
	if strings.TrimSpace(actionPath(action)) == "" {
		return ActionResult{}, errors.New("CLI 命令路径不能为空")
	}
	plan, err := PlanSystemCLITerminalLaunch(runtime.GOOS, actionPath(action), false)
	if err != nil {
		return ActionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Message: "CLI 终端已打开", Path: actionPath(action)}, launchSystemTerminalPlan(plan)
}
