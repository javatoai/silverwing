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

func (s *Service) resolveCLIExecutable(ctx context.Context, cfg domain.AppConfig, tool executable.Tool) (string, error) {
	resolution, err := s.resolveCLIExecutableWithSource(ctx, cfg, tool)
	return resolution.Command, err
}

func (s *Service) resolveCLIExecutableWithSource(ctx context.Context, cfg domain.AppConfig, tool executable.Tool) (executable.Resolution, error) {
	return s.resolveCLIExecutableWithSourceForce(ctx, cfg, tool, false)
}

func (s *Service) resolveCLIExecutableWithSourceForce(ctx context.Context, cfg domain.AppConfig, tool executable.Tool, force bool) (executable.Resolution, error) {
	if !isSupportedCLI(tool) {
		return executable.Resolution{}, fmt.Errorf("不支持的 CLI：%s", tool)
	}
	configured := ""
	if path := cliConfiguredPath(cfg, tool); path != nil {
		configured = strings.TrimSpace(*path)
	}
	if configured != "" {
		return executable.Resolution{Command: configured, Source: executable.SourceConfigured}, nil
	}
	resolver := executable.NewResolver(s.runner)
	var resolution executable.Resolution
	if force {
		resolution = resolver.ResolveFresh(ctx, tool, configured)
	} else {
		resolution = resolver.Resolve(ctx, tool, configured)
	}
	if resolution.Command == "" {
		return executable.Resolution{}, fmt.Errorf("不支持的 CLI：%s", tool)
	}
	return resolution, nil
}

func (s *Service) clearFailedCodexExecutable(ctx context.Context, failed string) (domain.AppConfig, error) {
	return s.config.Update(func(current domain.AppConfig) (domain.AppConfig, error) {
		if err := ctx.Err(); err != nil {
			return current, err
		}
		if current.CodexExecutablePath == nil || strings.TrimSpace(*current.CodexExecutablePath) != failed {
			return current, fmt.Errorf("CLI 命令路径已变化，请重新读取状态")
		}
		current.CodexExecutablePath = nil
		return current, nil
	})
}

// Re-read under the config lock so a concurrent settings change takes priority
// over a candidate whose version check has just succeeded.
func (s *Service) persistDiscoveredCLIExecutable(ctx context.Context, cfg domain.AppConfig, tool executable.Tool, resolution executable.Resolution) (domain.AppConfig, error) {
	if path := cliConfiguredPath(cfg, tool); path != nil && strings.TrimSpace(*path) != "" {
		return cfg, nil
	}
	discoveredSystemPath := tool == executable.ToolCodex && resolution.Source == executable.SourcePathFallback && filepath.IsAbs(resolution.Command)
	if resolution.Source != executable.SourceProbed && resolution.Source != executable.SourceDesktopApp && !discoveredSystemPath {
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
		if isOptionalCLI(tool) {
			name := string(tool)
			if !current.OptionalCLIEnabled(name) {
				return current, fmt.Errorf("CLI 已停用，请重新读取状态")
			}
			path := current.OptionalCLIPath(name)
			if path != cfg.OptionalCLIPath(name) && path != resolution.Command {
				return current, fmt.Errorf("CLI 命令路径已变化，请重新读取状态")
			}
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

// SaveOptionalCLIDiscovery is called while the application still owns the
// version probe's generation. The store then checks the current saved intent.
func (s *Service) SaveOptionalCLIDiscovery(ctx context.Context, cfg domain.AppConfig, tool, command, source string) (domain.AppConfig, error) {
	cliTool := executable.Tool(tool)
	if !isOptionalCLI(cliTool) || !cfg.OptionalCLIEnabled(tool) {
		return cfg, fmt.Errorf("CLI 已停用，请重新读取状态")
	}
	return s.persistDiscoveredCLIExecutable(ctx, cfg, cliTool, executable.Resolution{Command: command, Source: executable.Source(source)})
}

func setCLIConfiguredPath(config *domain.AppConfig, tool executable.Tool, path string) {
	switch tool {
	case executable.ToolLark:
		config.LarkExecutablePath = &path
	case executable.ToolGit:
		config.GitExecutablePath = &path
	case executable.ToolMeegle:
		config.MeegleExecutablePath = &path
	case executable.ToolCodex:
		config.CodexExecutablePath = &path
	}
}

func cliConfiguredPath(cfg domain.AppConfig, tool executable.Tool) *string {
	switch tool {
	case executable.ToolLark:
		return cfg.LarkExecutablePath
	case executable.ToolGit:
		return cfg.GitExecutablePath
	case executable.ToolMeegle:
		return cfg.MeegleExecutablePath
	case executable.ToolCodex:
		return cfg.CodexExecutablePath
	default:
		return nil
	}
}

func isSupportedCLI(tool executable.Tool) bool {
	switch tool {
	case executable.ToolLark, executable.ToolGit, executable.ToolMeegle, executable.ToolCodex:
		return true
	default:
		return false
	}
}

func isOptionalCLI(tool executable.Tool) bool {
	switch tool {
	case executable.ToolLark:
		return true
	default:
		return false
	}
}

func cliEnvironmentWithRunner(ctx context.Context, runner executable.Runner, tool executable.Tool, command string) map[string]string {
	return executable.NewResolver(runner).Environment(ctx, tool, command)
}
