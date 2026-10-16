package platform

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/executable"
)

func (s *Service) resolveCLIExecutable(ctx context.Context, cfg domain.AppConfig, tool string) (domain.AppConfig, string, domain.CommandProxyTarget, error) {
	updated, resolution, target, err := s.resolveCLIExecutableWithSource(ctx, cfg, tool)
	return updated, resolution.Command, target, err
}

func (s *Service) resolveCLIExecutableWithSource(ctx context.Context, cfg domain.AppConfig, tool string) (domain.AppConfig, executable.Resolution, domain.CommandProxyTarget, error) {
	return s.resolveCLIExecutableWithSourceForce(ctx, cfg, tool, false)
}

func (s *Service) resolveCLIExecutableWithSourceForce(ctx context.Context, cfg domain.AppConfig, tool string, force bool) (domain.AppConfig, executable.Resolution, domain.CommandProxyTarget, error) {
	tool = strings.ToLower(strings.TrimSpace(tool))
	configured := ""
	if path := cliConfiguredPath(cfg, tool); path != nil {
		configured = strings.TrimSpace(*path)
	}
	target, ok := cliProxyTarget(tool)
	if !ok {
		return cfg, executable.Resolution{}, "", fmt.Errorf("不支持的 CLI：%s", tool)
	}
	if configured != "" {
		source := executable.SourceConfigured
		if tool == "genbu" && cfg.GenbuExecutableAutoDetected {
			source = executable.SourceProbed
		}
		return cfg, executable.Resolution{Command: configured, Source: source}, target, nil
	}
	resolver := executable.NewResolver(s.runner)
	var resolution executable.Resolution
	if force {
		resolution = resolver.ResolveFresh(ctx, executable.Tool(tool), configured)
	} else {
		resolution = resolver.Resolve(ctx, executable.Tool(tool), configured)
	}
	if resolution.Command == "" {
		return cfg, executable.Resolution{}, "", fmt.Errorf("不支持的 CLI：%s", tool)
	}
	return cfg, resolution, target, nil
}

// A candidate is persisted only after its version command succeeds. Re-read
// under the config lock so a concurrently entered path always takes precedence.
func (s *Service) persistDiscoveredCLIExecutable(ctx context.Context, cfg domain.AppConfig, tool string, resolution executable.Resolution) (domain.AppConfig, error) {
	if path := cliConfiguredPath(cfg, tool); path != nil && strings.TrimSpace(*path) != "" {
		return cfg, nil
	}
	if resolution.Source != executable.SourceProbed && resolution.Source != executable.SourceDesktopApp {
		return cfg, nil
	}
	info, err := os.Stat(resolution.Command)
	if !filepath.IsAbs(resolution.Command) || err != nil || info.IsDir() {
		return cfg, fmt.Errorf("检测到的 CLI 路径已不可用，请手动填写可执行文件路径")
	}
	if err := ctx.Err(); err != nil {
		return cfg, err
	}
	updated, err := s.config.Update(func(current domain.AppConfig) (domain.AppConfig, error) {
		if err := ctx.Err(); err != nil {
			return current, err
		}
		if path := cliConfiguredPath(current, tool); path == nil || strings.TrimSpace(*path) == "" {
			setCLIConfiguredPath(&current, tool, resolution.Command)
		}
		return current, nil
	})
	if err != nil {
		return cfg, err
	}
	if path := cliConfiguredPath(updated, tool); path == nil || strings.TrimSpace(*path) != resolution.Command {
		return updated, fmt.Errorf("CLI 命令路径已变化，请重新读取状态")
	}
	return updated, nil
}

func setCLIConfiguredPath(config *domain.AppConfig, tool, path string) {
	switch tool {
	case "git":
		config.GitExecutablePath = &path
	case "meegle":
		config.MeegleExecutablePath = &path
	case "lark":
		config.LarkExecutablePath = &path
	case "codex":
		config.CodexExecutablePath = &path
	case "genbu":
		config.GenbuExecutablePath = &path
		config.GenbuExecutableAutoDetected = true
	}
}

func cliConfiguredPath(cfg domain.AppConfig, tool string) *string {
	switch tool {
	case "git":
		return cfg.GitExecutablePath
	case "meegle":
		return cfg.MeegleExecutablePath
	case "lark":
		return cfg.LarkExecutablePath
	case "codex":
		return cfg.CodexExecutablePath
	case "genbu":
		return cfg.GenbuExecutablePath
	default:
		return nil
	}
}

func cliProxyTarget(tool string) (domain.CommandProxyTarget, bool) {
	switch tool {
	case "git":
		return domain.ProxyTargetGit, true
	case "meegle":
		return domain.ProxyTargetMeegle, true
	case "lark":
		return domain.ProxyTargetLark, true
	case "genbu":
		return domain.ProxyTargetGenbu, true
	case "codex":
		return domain.ProxyTargetCodex, true
	default:
		return "", false
	}
}

func cliEnvironment(tool, command string) map[string]string {
	return cliEnvironmentContext(context.Background(), tool, command)
}

func cliEnvironmentContext(ctx context.Context, tool, command string) map[string]string {
	return cliEnvironmentWithRunner(ctx, nil, tool, command)
}

func cliEnvironmentWithRunner(ctx context.Context, runner executable.Runner, tool, command string) map[string]string {
	var resolvedTool executable.Tool
	switch tool {
	case "codex":
		resolvedTool = executable.ToolCodex
	case "git":
		resolvedTool = executable.ToolGit
	case "genbu":
		resolvedTool = executable.ToolGenbu
	case "lark":
		resolvedTool = executable.ToolLark
	case "meegle":
		resolvedTool = executable.ToolMeegle
	default:
		return nil
	}
	environment := executable.NewResolver(runner).Environment(ctx, resolvedTool, command)
	if tool == "lark" {
		if environment == nil {
			environment = map[string]string{}
		}
		environment["LARKSUITE_CLI_NO_UPDATE_NOTIFIER"] = "1"
		environment["LARKSUITE_CLI_NO_SKILLS_NOTIFIER"] = "1"
	}
	return environment
}
