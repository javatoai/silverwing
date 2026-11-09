package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"silverwing-go/internal/executable"
	"time"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/requirementprovider"
	"silverwing-go/internal/workspace"
)

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
	dependencies := workspace.ComposeTaskApplicationDependencies(a.Manifests, a.git(config), a.Paths, a.materialsPreparation())
	service := workspace.NewTaskApplicationServiceWithDependencies(dependencies)
	service.Lifecycle.Recycler = a.directoryRecycler
	registry, _ := workspace.NewTaskWorkspaceToolRegistry([]workspace.TaskWorkspaceToolLauncher{platformCodexWorkspaceLauncher{service: a.Platform, forceNew: true}, cursorWorkspaceLauncher{runner: a.Runner}})
	service.WorkspaceTools.Registry = registry
	return service
}

// materialsPreparation is the application composition root shared by task,
// delivery, and document preparation flows.
func (a *App) materialsPreparation() *documents.PreparationService {
	return documents.NewPreparationService(a.Paths, requirementprovider.NewMeegle(a.Runner))
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
