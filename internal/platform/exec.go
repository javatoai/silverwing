package platform

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/executable"
	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
)

var proxyEnvironmentNames = []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"}

func commandEnvironment(config domain.AppConfig, target domain.CommandProxyTarget) (map[string]string, []string) {
	remove := append([]string(nil), proxyEnvironmentNames...)
	if config.CommandProxyURL == nil {
		return map[string]string{}, remove
	}
	normalized, err := domain.NormalizeCommandProxyURL(config.CommandProxyURL)
	if err != nil || normalized == nil {
		return map[string]string{}, remove
	}
	enabled := false
	for _, candidate := range config.CommandProxyTargets {
		if candidate == target {
			enabled = true
			break
		}
	}
	if !enabled {
		return map[string]string{}, remove
	}
	endpoint := *normalized
	username := ""
	password := ""
	if config.CommandProxyUsername != nil {
		username = strings.TrimSpace(*config.CommandProxyUsername)
	}
	if config.CommandProxyPassword != nil {
		password = *config.CommandProxyPassword
	}
	if username != "" && password != "" {
		parsed, parseErr := url.Parse(endpoint)
		if parseErr == nil {
			parsed.User = url.UserPassword(username, password)
			endpoint = parsed.String()
		}
	}
	values := map[string]string{
		"HTTP_PROXY": endpoint, "HTTPS_PROXY": endpoint, "ALL_PROXY": endpoint,
		"http_proxy": endpoint, "https_proxy": endpoint, "all_proxy": endpoint,
	}
	if config.CommandProxyNoProxy != nil {
		if noProxy, err := domain.NormalizeCommandProxyNoProxy(config.CommandProxyNoProxy); err == nil && noProxy != nil {
			values["NO_PROXY"], values["no_proxy"] = *noProxy, *noProxy
		}
	}
	return values, remove
}

func (s *Service) runTool(ctx context.Context, config domain.AppConfig, target domain.CommandProxyTarget, executable string, args []string, directory string, timeout time.Duration) (process.Result, error) {
	return s.runToolWithEnv(ctx, config, target, executable, args, directory, timeout, nil)
}

func (s *Service) runToolWithEnv(ctx context.Context, config domain.AppConfig, target domain.CommandProxyTarget, executable string, args []string, directory string, timeout time.Duration, extraEnv map[string]string) (process.Result, error) {
	env, removals := commandEnvironment(config, target)
	for name, value := range extraEnv {
		env[name] = value
	}
	result, err := s.runner.Run(ctx, process.Spec{Executable: executable, Arguments: args, Directory: directory, Env: env, RemoveEnv: removals, Timeout: timeout})
	if err != nil {
		return result, wrapCommandDisplayError(fmt.Sprintf("%s：%s", executable, redactProxy(config, err.Error())), err)
	}
	if result.ExitCode != 0 {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			message = strings.TrimSpace(result.Stdout)
		}
		if message == "" {
			message = fmt.Sprintf("退出码 %d", result.ExitCode)
		}
		return result, fmt.Errorf("%s 执行失败（%s）：%s", filepath.Base(executable), proxyStatus(config, target), redactProxy(config, message))
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

func (s *Service) gitExecutable(config domain.AppConfig) string {
	_, resolution, _, err := s.resolveCLIExecutableWithSource(context.Background(), config, string(executable.ToolGit))
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
	if config.CommandProxyURL == nil {
		return message
	}
	endpoint, err := domain.NormalizeCommandProxyURL(config.CommandProxyURL)
	if err != nil || endpoint == nil {
		return message
	}
	message = strings.ReplaceAll(message, *endpoint, "[代理地址已隐藏]")
	username, password := "", ""
	if config.CommandProxyUsername != nil {
		username = strings.TrimSpace(*config.CommandProxyUsername)
	}
	if config.CommandProxyPassword != nil {
		password = *config.CommandProxyPassword
	}
	if username != "" && password != "" {
		parsed, parseErr := url.Parse(*endpoint)
		if parseErr == nil {
			parsed.User = url.UserPassword(username, password)
			message = strings.ReplaceAll(message, parsed.String(), "[代理地址已隐藏]")
		}
	}
	if username != "" {
		message = strings.ReplaceAll(message, username, "[已隐藏]")
	}
	if password != "" {
		message = strings.ReplaceAll(message, password, "[已隐藏]")
	}
	return message
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
