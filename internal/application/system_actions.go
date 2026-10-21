package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"silverwing-go/internal/domain"
)

func (a *App) systemAction(ctx context.Context, action Action) (ActionResult, error) {
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	if action.Kind == "system.logs" {
		path, err := a.prepareLogDirectory(ctx)
		if err != nil {
			return ActionResult{}, err
		}
		return ActionResult{Message: "已打开日志目录", Path: path}, openNative(path, false)
	}
	if action.Kind == "system.cli-terminal" {
		return a.systemCLITerminal(ctx, action)
	}
	if action.Kind == "system.ide-test" {
		config, err := a.Config.Load()
		if err != nil {
			return ActionResult{}, err
		}
		plan, target, err := planSystemDevelopmentToolTest(config, action)
		if err != nil {
			return ActionResult{}, err
		}
		if err := ctx.Err(); err != nil {
			return ActionResult{}, err
		}
		return ActionResult{Message: "开发工具已打开", Path: target}, launchToolArguments(ctx, plan.Executable, plan.Arguments...)
	}
	path := actionPath(action)
	if path == "" {
		return ActionResult{}, errors.New("路径不能为空")
	}
	if action.Kind == "system.file-opening-status" || action.Kind == "system.open-with" {
		return runSystemFileOpening(ctx, action.Kind, path, nativeSystemFileOpening{})
	}
	if action.Kind == "system.open" {
		if err := a.validateTaskContextFromCurrentSource(action); err != nil {
			return ActionResult{}, err
		}
		link, err := systemOpenLink(path)
		if err != nil {
			return ActionResult{}, err
		}
		if link {
			return ActionResult{Message: "已打开链接", Path: path}, openNative(path, false)
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return ActionResult{}, err
	}
	if directory, _ := action.Payload["desktopSystemOpenDirectory"].(bool); action.Kind == "system.open" && directory && !info.IsDir() {
		return ActionResult{}, errors.New("目录已变化，请重新选择后重试。")
	}
	switch action.Kind {
	case "system.open":
		if !info.IsDir() {
			return runSystemFileOpening(ctx, action.Kind, path, nativeSystemFileOpening{})
		}
		return ActionResult{Message: "已打开", Path: path}, openNative(path, false)
	case "system.reveal":
		return ActionResult{Message: "已定位", Path: path}, openNative(path, true)
	case "system.terminal":
		if !info.IsDir() {
			path = filepath.Dir(path)
		}
		config, err := a.Config.Load()
		if err != nil {
			return ActionResult{}, err
		}
		resolution, err := a.resolveTerminalForLaunch(ctx, config.TerminalExecutable)
		if err != nil {
			return ActionResult{}, err
		}
		return ActionResult{Message: "终端已打开", Path: path}, launchTerminal(ctx, resolution, path)
	case "system.ide":
		config, err := a.Config.Load()
		if err != nil {
			return ActionResult{}, err
		}
		if !info.IsDir() {
			return ActionResult{}, fmt.Errorf("项目目录不存在：%s", path)
		}
		tool := textPayload(action, "tool")
		if tool == "" {
			tool = string(config.DefaultDevelopmentTool)
		}
		for _, configured := range config.DevelopmentTools {
			if strings.EqualFold(string(configured.Type), tool) {
				if _, err := os.Stat(configured.Path); err != nil {
					return ActionResult{}, fmt.Errorf("开发工具路径不存在：%s", configured.Path)
				}
				return ActionResult{Message: "开发工具已打开", Path: path}, launchTool(ctx, configured.Type, configured.Path, path)
			}
		}
		return ActionResult{}, errors.New("开发工具尚未配置")
	}
	return ActionResult{}, errors.New("不支持的系统操作")
}

func (a *App) prepareLogDirectory(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if strings.TrimSpace(a.Paths.Logs) == "" {
		return "", errors.New("日志目录尚未初始化")
	}
	if err := os.MkdirAll(a.Paths.Logs, 0o700); err != nil {
		return "", fmt.Errorf("无法创建日志目录：%w", err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return a.Paths.Logs, nil
}

// The settings test uses the current path draft without saving it, and opens
// the configured task root just like Source DesktopApplication.testDevelopmentTool.
func planSystemDevelopmentToolTest(config domain.AppConfig, action Action) (SystemLaunchPlan, string, error) {
	target := value(config.TaskRoot)
	if info, err := os.Stat(target); target == "" || err != nil || !info.IsDir() {
		return SystemLaunchPlan{}, "", errors.New("请先配置有效的任务根目录，再测试打开开发工具")
	}
	if action.Path != "" && filepath.Clean(action.Path) != filepath.Clean(target) {
		return SystemLaunchPlan{}, "", errors.New("任务根目录已变化，请重新测试打开开发工具")
	}
	tool := domain.DevelopmentToolType(textPayload(action, "tool"))
	switch tool {
	case domain.DevelopmentToolIntelliJIdea, domain.DevelopmentToolWebStorm, domain.DevelopmentToolPyCharm,
		domain.DevelopmentToolVisualStudioCode, domain.DevelopmentToolAndroidStudio, domain.DevelopmentToolDevEcoStudio:
	default:
		return SystemLaunchPlan{}, "", errors.New("请选择开发工具")
	}
	configuredPath := textPayload(action, "configuredPath")
	if _, err := os.Stat(configuredPath); err != nil {
		return SystemLaunchPlan{}, "", fmt.Errorf("开发工具路径不存在：%s", configuredPath)
	}
	plan, err := PlanSystemToolLaunch(runtime.GOOS, tool, configuredPath, target)
	return plan, target, err
}
