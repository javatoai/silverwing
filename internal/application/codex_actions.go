package application

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/platform"
	"silverwing-go/internal/workspace"
)

func (a *App) openCodex(ctx context.Context, action Action) (ActionResult, error) {
	config, err := a.Config.Load()
	if err != nil {
		return ActionResult{}, err
	}
	manifest, taskPath, err := a.taskInConfig(action, config)
	if err != nil {
		return ActionResult{}, err
	}
	if err := a.validateDesktopTaskContext(action, config, manifest, taskPath); err != nil {
		return ActionResult{}, err
	}
	ctx = a.desktopTaskMutationContext(ctx, action)
	forceNew := false
	if raw, exists := action.Payload["forceNew"]; exists {
		var ok bool
		forceNew, ok = raw.(bool)
		if !ok {
			return ActionResult{}, errors.New("forceNew 必须是布尔值")
		}
	}
	path := taskPath
	if !forceNew && (action.ServiceID != "" || action.ModuleID != "") {
		found := false
		for _, workspace := range manifest.Services {
			if workspace.GroupServiceID != action.ServiceID || action.ModuleID != "" && workspace.ModuleID != action.ModuleID {
				continue
			}
			if found {
				return ActionResult{}, errors.New("请选择具体任务模块")
			}
			found, path = true, workspace.WorktreePath
		}
		if !found {
			return ActionResult{}, errors.New("当前任务没有所选工作区")
		}
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return ActionResult{}, err
	}
	requestedPaths := []string{action.Path, textPayload(action, "path")}
	if raw, exists := action.Payload["workspacePath"]; exists {
		requested, ok := raw.(string)
		if !ok {
			return ActionResult{}, errors.New("workspacePath 必须是字符串")
		}
		requestedPaths = append(requestedPaths, requested)
	}
	for _, requested := range requestedPaths {
		requested = strings.TrimSpace(requested)
		if requested == "" {
			continue
		}
		requested, err = filepath.Abs(requested)
		if err != nil {
			return ActionResult{}, err
		}
		same := filepath.Clean(requested) == filepath.Clean(path)
		if runtime.GOOS == "windows" {
			same = strings.EqualFold(filepath.Clean(requested), filepath.Clean(path))
		}
		if !same {
			return ActionResult{}, errors.New("Codex 目录与任务清单不一致")
		}
	}
	if info, statErr := os.Stat(path); statErr != nil || !info.IsDir() {
		return ActionResult{}, errors.New("任务或工作区目录不存在")
	}
	if forceNew {
		return a.retryCodexWorkspaceTool(ctx, path, manifest, true, false)
	}
	if action.ServiceID == "" && action.ModuleID == "" {
		if a.Platform == nil {
			return ActionResult{}, errors.New("Codex 平台服务不可用")
		}
		boundThreadID, err := a.Platform.BoundCodexThread(path)
		if err != nil {
			return ActionResult{}, err
		}
		if boundThreadID == nil {
			return a.retryCodexWorkspaceTool(ctx, path, manifest, false, true)
		}
		if err := a.validateTaskContextFromCurrentSource(action); err != nil {
			return ActionResult{}, err
		}
		data, err := a.Platform.Execute(ctx, "codex.ai/open", map[string]any{
			"workspacePath": path,
			"threadId":      *boundThreadID,
			"forceNew":      false,
		})
		if err != nil {
			return ActionResult{}, err
		}
		return ActionResult{Message: "已请求 Codex 打开任务会话", Path: path, Data: data}, nil
	}
	data, err := a.Platform.Execute(ctx, "codex.ai/open", map[string]any{"workspacePath": path, "forceNew": forceNew})
	if err != nil {
		return ActionResult{}, err
	}
	return ActionResult{Message: "已请求 Codex 打开任务会话", Path: path, Data: data}, nil
}

func (a *App) retryCodexWorkspaceTool(ctx context.Context, path string, manifest domain.TaskManifest, forceNew, requireUnbound bool) (ActionResult, error) {
	config, err := a.Config.Load()
	if err != nil {
		return ActionResult{}, err
	}
	service := a.taskService(config)
	registry, err := workspace.NewTaskWorkspaceToolRegistry([]workspace.TaskWorkspaceToolLauncher{
		platformCodexWorkspaceLauncher{service: a.Platform, forceNew: forceNew, requireUnbound: requireUnbound},
	})
	if err != nil {
		return ActionResult{}, err
	}
	service.WorkspaceTools.Registry = registry
	updated, err := service.RetryWorkspaceTool(ctx, path, manifest, "codex")
	if err != nil {
		if updated.SchemaVersion == "" {
			updated = manifest
		}
		return ActionResult{Message: err.Error(), Path: path, Data: updated}, err
	}
	message := "已请求 Codex 为当前任务新建会话"
	for _, launch := range updated.WorkspaceToolLaunches {
		if launch.ToolID == "codex" && launch.Status == domain.WorkspaceToolFailed {
			message = "Codex 新会话启动失败，状态已记录到任务清单"
			if launch.Message != nil && strings.TrimSpace(*launch.Message) != "" {
				message += "：" + strings.TrimSpace(*launch.Message)
			}
			break
		}
	}
	return ActionResult{Message: message, Path: path, Data: updated}, nil
}

type platformCodexWorkspaceLauncher struct {
	service        *platform.Service
	forceNew       bool
	requireUnbound bool
}

func (l platformCodexWorkspaceLauncher) Descriptor() workspace.TaskWorkspaceToolDescriptor {
	return workspace.TaskWorkspaceToolDescriptor{ID: "codex", DisplayName: "Codex", Description: "为任务目录新建 Codex 会话"}
}

func (l platformCodexWorkspaceLauncher) Availability(ctx context.Context) workspace.TaskWorkspaceToolAvailability {
	return workspace.TaskWorkspaceToolAvailability{Available: l.service != nil}
}

func (l platformCodexWorkspaceLauncher) Open(ctx context.Context, task workspace.TaskWorkspaceContext) error {
	if l.service == nil {
		return errors.New("Codex 平台服务不可用")
	}
	_, err := l.service.Execute(ctx, "codex.ai/open", map[string]any{
		"workspacePath":  task.TaskDirectory,
		"forceNew":       l.forceNew,
		"requireUnbound": l.requireUnbound,
	})
	return err
}
