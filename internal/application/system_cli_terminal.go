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
	command := strings.TrimSpace(actionPath(action))
	if command == "" {
		return ActionResult{}, errors.New("CLI 命令路径不能为空")
	}
	tool := strings.ToLower(strings.TrimSpace(textPayload(action, "tool")))
	var check func(context.Context) error
	switch tool {
	case "lark":
		verified, checkScope, err := a.optionalCLIScope(ctx, tool)
		if err != nil {
			return ActionResult{}, err
		}
		if command != verified {
			return ActionResult{}, errors.New("CLI 命令路径已变化，请重新读取状态")
		}
		check = checkScope
	case "git", "meegle", "codex":
	default:
		return ActionResult{}, errors.New("请指定要在终端中运行的 CLI")
	}
	plan, err := PlanSystemCLITerminalLaunch(runtime.GOOS, command, false)
	if err != nil {
		return ActionResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	if check != nil {
		if err := check(ctx); err != nil {
			return ActionResult{}, err
		}
	}
	return ActionResult{Message: "CLI 终端已打开", Path: command}, launchSystemTerminalPlan(ctx, plan)
}
