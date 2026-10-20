package application

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"silverwing-go/internal/executable"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/workspace"
)

type codexWorkspaceLauncher struct{}

func (codexWorkspaceLauncher) Descriptor() workspace.TaskWorkspaceToolDescriptor {
	return workspace.TaskWorkspaceToolDescriptor{ID: "codex", DisplayName: "Codex", Description: "任务创建完成后在 Codex 中打开任务目录"}
}
func (codexWorkspaceLauncher) Availability(ctx context.Context) workspace.TaskWorkspaceToolAvailability {
	return workspace.TaskWorkspaceToolAvailability{Available: true}
}
func (codexWorkspaceLauncher) Open(ctx context.Context, task workspace.TaskWorkspaceContext) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := filepath.Abs(task.TaskDirectory)
	if err != nil {
		return err
	}
	encoded := strings.ReplaceAll(url.QueryEscape(path), "+", "%20")
	return openNative("codex://threads/new?path="+encoded, false)
}

type cursorWorkspaceLauncher struct{ runner *process.Runner }

func (cursorWorkspaceLauncher) Descriptor() workspace.TaskWorkspaceToolDescriptor {
	return workspace.TaskWorkspaceToolDescriptor{ID: "cursor", DisplayName: "Cursor", Description: "独立窗口打开任务目录；在 Cursor 中启动一次 Agent Chat 后会显示到 Workspaces"}
}
func (c cursorWorkspaceLauncher) Availability(ctx context.Context) workspace.TaskWorkspaceToolAvailability {
	if locateCursor(ctx, c.runner) == "" {
		return workspace.TaskWorkspaceToolAvailability{Reason: "未找到 Cursor，请先安装并确保系统可以启动 Cursor"}
	}
	return workspace.TaskWorkspaceToolAvailability{Available: true}
}
func (c cursorWorkspaceLauncher) Open(ctx context.Context, task workspace.TaskWorkspaceContext) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	command := locateCursor(ctx, c.runner)
	if command == "" {
		return errors.New("当前系统未找到 Cursor")
	}
	return launchToolArguments(ctx, command, "--new-window", task.TaskDirectory)
}

func (a *App) taskService(config domain.AppConfig) *workspace.TaskApplicationService {
	service := workspace.NewTaskApplicationService(a.Manifests, a.git(config), a.Paths)
	registry, _ := workspace.NewTaskWorkspaceToolRegistry([]workspace.TaskWorkspaceToolLauncher{codexWorkspaceLauncher{}, cursorWorkspaceLauncher{runner: a.Runner}})
	service.WorkspaceTools.Registry = registry
	return service
}

func locateCursor(ctx context.Context, runner *process.Runner) string {
	if runtime.GOOS == "windows" {
		pattern := regexp.MustCompile(`(?i)"([^"\r\n]+\.exe)"`)
		for _, key := range []string{`HKCU\Software\Classes\cursor\shell\open\command`, `HKCR\cursor\shell\open\command`} {
			result, err := runner.Run(ctx, process.Spec{Executable: "reg.exe", Arguments: []string{"query", key, "/ve"}, Timeout: 2 * time.Second, MaxStdoutBytes: 64 * 1024, MaxStderrBytes: 64 * 1024})
			if err == nil && result.Successful() {
				if matched := pattern.FindStringSubmatch(result.Stdout); len(matched) > 1 {
					if info, e := os.Stat(matched[1]); e == nil && info.Mode().IsRegular() {
						return matched[1]
					}
				}
			}
		}
	}
	for _, name := range []string{"cursor.exe", "cursor.cmd", "cursor.bat", "cursor"} {
		path, _, err := executable.NewResolver(runner).PrepareCommand(ctx, name)
		if err == nil && filepath.IsAbs(path) {
			if info, e := os.Stat(path); e == nil && info.Mode().IsRegular() {
				return path
			}
		}
	}
	return ""
}
