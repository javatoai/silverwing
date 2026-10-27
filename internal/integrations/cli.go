package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"silverwing-go/internal/commandruntime"
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

func (s *Service) commandPath(ctx context.Context, config domain.AppConfig, target domain.CommandProxyTarget) string {
	command, _, err := s.resolveCommand(ctx, config, target)
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
	return commandruntime.ProxyEnvironment(config, target)
}

func (s *Service) runCLI(ctx context.Context, config domain.AppConfig, target domain.CommandProxyTarget, args []string, stdin []byte, timeout time.Duration, operation string, workingDirectory string) (process.Result, error) {
	command, _, err := s.resolveCommand(ctx, config, target)
	if err != nil {
		return process.Result{}, err
	}
	spec, err := commandruntime.Prepare(ctx, s.runner, &config, target, executable.Tool(targetToolName(target)), command)
	if err != nil {
		return process.Result{}, err
	}
	spec.Arguments, spec.Directory, spec.Stdin, spec.Timeout = args, workingDirectory, stdin, timeout
	result, err := s.runner.Run(ctx, spec)
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
	default:
		return ""
	}
}

func (s *Service) runMeegle(ctx context.Context, config domain.AppConfig, args []string, timeout time.Duration, operation string) (process.Result, error) {
	return s.runCLI(ctx, config, domain.ProxyTargetMeegle, args, nil, timeout, operation, "")
}

func (s *Service) safeDiagnostic(config domain.AppConfig, message string) string {
	return commandruntime.Redact(config, message)
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
