package platform

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"silverwing-go/internal/commandruntime"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/executable"
	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
)

func commandEnvironment(config domain.AppConfig, target domain.CommandProxyTarget) (map[string]string, []string) {
	return commandruntime.ProxyEnvironment(config, target)
}

func cliCommandProxyTarget(tool executable.Tool) domain.CommandProxyTarget {
	if tool == executable.ToolCodex {
		return domain.ProxyTargetCodex
	}
	return ""
}

func (s *Service) runTool(ctx context.Context, config domain.AppConfig, tool executable.Tool, command string, args []string, directory string, timeout time.Duration) (process.Result, error) {
	return s.runToolWithEnv(ctx, config, tool, command, args, directory, timeout, nil)
}

func (s *Service) runToolWithEnv(ctx context.Context, config domain.AppConfig, tool executable.Tool, command string, args []string, directory string, timeout time.Duration, extraEnv map[string]string) (process.Result, error) {
	target := cliCommandProxyTarget(tool)
	// Keep the explicit policy for direct tools so inherited proxy variables are cleared.
	spec, err := commandruntime.Prepare(ctx, s.runner, &config, target, tool, command)
	if err != nil {
		return process.Result{}, err
	}
	for name, value := range extraEnv {
		spec.Env[name] = value
	}
	spec.Arguments, spec.Directory, spec.Timeout = args, directory, timeout
	result, err := s.runner.Run(ctx, spec)
	if err != nil {
		return result, wrapCommandDisplayError(fmt.Sprintf("%s：%s", command, redactProxy(config, err.Error())), err)
	}
	if result.ExitCode != 0 {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			message = strings.TrimSpace(result.Stdout)
		}
		if message == "" {
			message = fmt.Sprintf("退出码 %d", result.ExitCode)
		}
		message = redactProxy(config, message)
		if target != "" {
			return result, fmt.Errorf("%s 执行失败（%s）：%s", filepath.Base(command), proxyStatus(config, target), message)
		}
		return result, fmt.Errorf("%s 执行失败：%s", filepath.Base(command), message)
	}
	return result, nil
}

func (s *Service) runCodex(ctx context.Context, config domain.AppConfig, args ...string) (process.Result, error) {
	command, err := s.resolveCLIExecutable(ctx, config, executable.ToolCodex)
	if err != nil {
		return process.Result{}, err
	}
	return s.runToolWithEnv(ctx, config, executable.ToolCodex, command, args, "", 2*time.Minute, cliEnvironmentWithRunner(ctx, s.runner, executable.ToolCodex, command))
}

func (s *Service) runCodexOutput(ctx context.Context, config domain.AppConfig, args ...string) (string, error) {
	result, err := s.runCodex(ctx, config, args...)
	if err != nil {
		return "", err
	}
	return result.Stdout, nil
}

func (s *Service) gitExecutable(ctx context.Context, config domain.AppConfig) string {
	command, err := s.resolveCLIExecutable(ctx, config, executable.ToolGit)
	if err != nil {
		return "git"
	}
	return command
}

func proxyStatus(config domain.AppConfig, target domain.CommandProxyTarget) string {
	if config.CommandProxyURL == nil {
		return "代理未配置"
	}
	for _, candidate := range config.CommandProxyTargets {
		if candidate == target {
			return "代理已启用"
		}
	}
	return "代理未启用"
}

func redactProxy(config domain.AppConfig, message string) string {
	return commandruntime.Redact(config, message)
}

func atomicWrite(target string, data []byte, mode os.FileMode) error {
	parent := filepath.Dir(target)
	if err := ensureNoExistingSymlinks(parent); err != nil {
		return err
	}
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	if err := ensureNoExistingSymlinks(parent); err != nil {
		return err
	}
	if exists(target) && !isRegular(target) {
		return fmt.Errorf("写入目标必须是普通文件")
	}
	if mode == 0 {
		mode = 0o600
	}
	return storage.WriteFileAtomic(target, data, mode)
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
