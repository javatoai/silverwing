package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/executable"
	"silverwing-go/internal/process"
)

type commandError struct {
	Operation string `json:"operation"`
	ExitCode  int    `json:"exitCode"`
	Message   string `json:"message"`
}

func (e *commandError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("%s 失败（退出码 %d）", e.Operation, e.ExitCode)
	}
	return fmt.Sprintf("%s 失败：%s", e.Operation, e.Message)
}

// Keep the cause available to cancellation/timeout checks without printing
// the original diagnostic, which may contain proxy credentials.
type cliRunFailure struct {
	message string
	cause   error
}

func (e *cliRunFailure) Error() string { return e.message }
func (e *cliRunFailure) Unwrap() error { return e.cause }

func (s *Service) commandPath(config domain.AppConfig, target domain.CommandProxyTarget) string {
	command, _, err := s.resolveCommand(context.Background(), config, target)
	if err != nil {
		return ""
	}
	return command
}

func (s *Service) resolveCommand(ctx context.Context, config domain.AppConfig, target domain.CommandProxyTarget) (string, *executable.Resolver, error) {
	var tool executable.Tool
	var configured *string
	switch target {
	case domain.ProxyTargetMeegle:
		tool, configured = executable.ToolMeegle, config.MeegleExecutablePath
	case domain.ProxyTargetLark:
		tool, configured = executable.ToolLark, config.LarkExecutablePath
	default:
		return "", nil, fmt.Errorf("不支持的 CLI 目标：%s", target)
	}
	value := ""
	if configured != nil {
		value = *configured
	}
	resolver := executable.NewResolver(s.runner)
	resolution := resolver.Resolve(ctx, tool, value)
	if resolution.Command == "" {
		return "", nil, fmt.Errorf("未能解析 %s CLI", tool)
	}
	return resolution.Command, resolver, nil
}

func (s *Service) childEnvironment(config domain.AppConfig, target domain.CommandProxyTarget) (map[string]string, []string) {
	removals := []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"}
	enabled := config.CommandProxyURL != nil && strings.TrimSpace(*config.CommandProxyURL) != ""
	if enabled {
		found := false
		for _, current := range config.CommandProxyTargets {
			found = found || current == target
		}
		enabled = found
	}
	if !enabled {
		return nil, removals
	}
	endpoint := strings.TrimSpace(*config.CommandProxyURL)
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, removals
	}
	if config.CommandProxyUsername != nil && *config.CommandProxyUsername != "" {
		password := ""
		if config.CommandProxyPassword != nil {
			password = *config.CommandProxyPassword
		}
		parsed.User = url.UserPassword(*config.CommandProxyUsername, password)
	}
	endpoint = parsed.String()
	values := map[string]string{
		"HTTP_PROXY": endpoint, "HTTPS_PROXY": endpoint, "ALL_PROXY": endpoint,
		"http_proxy": endpoint, "https_proxy": endpoint, "all_proxy": endpoint,
	}
	if config.CommandProxyNoProxy != nil && strings.TrimSpace(*config.CommandProxyNoProxy) != "" {
		values["NO_PROXY"] = *config.CommandProxyNoProxy
		values["no_proxy"] = *config.CommandProxyNoProxy
	}
	return values, removals
}

func (s *Service) runCLI(ctx context.Context, config domain.AppConfig, target domain.CommandProxyTarget, args []string, stdin []byte, timeout time.Duration, operation string, workingDirectory string) (process.Result, error) {
	executablePath, resolver, err := s.resolveCommand(ctx, config, target)
	if err != nil {
		return process.Result{}, err
	}
	env, remove := s.childEnvironment(config, target)
	extraEnvironment := resolver.Environment(ctx, executable.Tool(targetToolName(target)), executablePath)
	if len(extraEnvironment) > 0 && env == nil {
		env = map[string]string{}
	}
	for name, value := range extraEnvironment {
		env[name] = value
	}
	result, err := s.runner.Run(ctx, process.Spec{
		Executable: executablePath,
		Arguments:  args,
		Directory:  workingDirectory,
		Env:        env,
		RemoveEnv:  remove,
		Stdin:      stdin,
		Timeout:    timeout,
	})
	if err != nil {
		return result, &cliRunFailure{message: fmt.Sprintf("%s：%s", operation, s.safeDiagnostic(config, err.Error())), cause: err}
	}
	if !result.Successful() {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			message = strings.TrimSpace(result.Stdout)
		}
		if message == "" {
			message = fmt.Sprintf("退出码 %d", result.ExitCode)
		}
		message = truncateRunes(s.safeDiagnostic(config, message), 500)
		return result, &commandError{Operation: operation, ExitCode: result.ExitCode, Message: message}
	}
	return result, nil
}

func targetToolName(target domain.CommandProxyTarget) string {
	switch target {
	case domain.ProxyTargetMeegle:
		return string(executable.ToolMeegle)
	case domain.ProxyTargetLark:
		return string(executable.ToolLark)
	default:
		return ""
	}
}

func (s *Service) runMeegle(ctx context.Context, config domain.AppConfig, args []string, timeout time.Duration, operation string) (process.Result, error) {
	return s.runCLI(ctx, config, domain.ProxyTargetMeegle, args, nil, timeout, operation, "")
}

func (s *Service) runLark(ctx context.Context, config domain.AppConfig, args []string, input []byte, timeout time.Duration, operation, workingDirectory string) (process.Result, error) {
	return s.runCLI(ctx, config, domain.ProxyTargetLark, args, input, timeout, operation, workingDirectory)
}

func (s *Service) safeDiagnostic(config domain.AppConfig, message string) string {
	for _, secret := range []*string{config.CommandProxyPassword, config.CommandProxyUsername, config.CommandProxyURL} {
		if secret != nil && *secret != "" {
			message = strings.ReplaceAll(message, *secret, "[已隐藏]")
		}
	}
	if config.CommandProxyURL != nil {
		if parsed, err := url.Parse(*config.CommandProxyURL); err == nil && parsed.Host != "" {
			message = strings.ReplaceAll(message, parsed.Host, "[代理地址已隐藏]")
		}
	}
	return message
}

func (s *Service) runJSON(ctx context.Context, config domain.AppConfig, target domain.CommandProxyTarget, args []string, timeout time.Duration, operation string) (any, process.Result, error) {
	result, err := s.runCLI(ctx, config, target, args, nil, timeout, operation, "")
	if err != nil {
		return nil, result, err
	}
	var value any
	if err := json.Unmarshal([]byte(result.Stdout), &value); err != nil {
		return nil, result, fmt.Errorf("%s 返回的 JSON 无法解析：%w", operation, err)
	}
	return value, result, nil
}

// writeJSONArgument creates a UTF-8 JSON file for CLIs that parse @file
// arguments. It always removes the temporary file and returns its absolute path.
func (s *Service) writeJSONArgument(value any) (string, func(), error) {
	data, err := json.Marshal(value)
	if err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(s.paths.Temp, 0o700); err != nil {
		return "", nil, err
	}
	file, err := os.CreateTemp(s.paths.Temp, "silverwing-cli-*.json")
	if err != nil {
		return "", nil, err
	}
	name := file.Name()
	if err = file.Chmod(0o600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		_ = os.Remove(name)
		return "", nil, err
	}
	absolute, err := filepath.Abs(name)
	if err != nil {
		_ = os.Remove(name)
		return "", nil, err
	}
	return absolute, func() { _ = os.Remove(name) }, nil
}

func runInputFile(ctx context.Context, operation string, run func([]string) (process.Result, error), args []string, path string, cleanup func()) (process.Result, error) {
	defer cleanup()
	arguments := append([]string(nil), args...)
	arguments = append(arguments, "@"+path)
	result, err := run(arguments)
	if err != nil {
		if process.IsCleanupFailure(err) {
			return result, err
		}
		if errors.Is(ctx.Err(), context.Canceled) {
			return result, ctx.Err()
		}
		return result, fmt.Errorf("%s: %w", operation, err)
	}
	return result, nil
}
