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

var proxyEnvironmentNames = []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"}

func commandEnvironment(config domain.AppConfig, target domain.CommandProxyTarget) (map[string]string, []string) {
	return commandruntime.ProxyEnvironment(config, target)
}

func (s *Service) runTool(ctx context.Context, config domain.AppConfig, target domain.CommandProxyTarget, executable string, args []string, directory string, timeout time.Duration) (process.Result, error) {
	return s.runToolWithEnv(ctx, config, target, executable, args, directory, timeout, nil)
}

func (s *Service) runToolWithEnv(ctx context.Context, config domain.AppConfig, target domain.CommandProxyTarget, command string, args []string, directory string, timeout time.Duration, extraEnv map[string]string) (process.Result, error) {
	spec, err := commandruntime.Prepare(ctx, s.runner, &config, target, executable.Tool(strings.ToLower(string(target))), command)
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
		return result, fmt.Errorf("%s 执行失败（%s）：%s", filepath.Base(command), proxyStatus(config, target), redactProxy(config, message))
	}
	return result, nil
}

func (s *Service) runCodex(ctx context.Context, config domain.AppConfig, args ...string) (process.Result, error) {
	_, resolution, target, err := s.resolveCLIExecutableWithSource(ctx, config, string(executable.ToolCodex))
	if err != nil {
		return process.Result{}, err
	}
	return s.runToolWithEnv(ctx, config, target, resolution.Command, args, "", 2*time.Minute, cliEnvironmentWithRunner(ctx, s.runner, string(executable.ToolCodex), resolution.Command))
}

func (s *Service) runCodexOutput(ctx context.Context, config domain.AppConfig, args ...string) (string, error) {
	result, err := s.runCodex(ctx, config, args...)
	if err != nil {
		return "", err
	}
	return result.Stdout, nil
}

func (s *Service) gitExecutable(ctx context.Context, config domain.AppConfig) string {
	_, resolution, _, err := s.resolveCLIExecutableWithSource(ctx, config, string(executable.ToolGit))
	if err != nil {
		return "git"
	}
	return resolution.Command
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
