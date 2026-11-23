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

func (s *Service) meegleCommandPath(ctx context.Context, config domain.AppConfig) string {
	resolver := executable.NewResolver(s.runner)
	return resolver.Resolve(ctx, executable.ToolMeegle, valuePointer(config.MeegleExecutablePath)).Command
}

func (s *Service) runMeegle(ctx context.Context, config domain.AppConfig, args []string, timeout time.Duration, operation string) (process.Result, error) {
	spec, err := commandruntime.Prepare(ctx, s.runner, &config, "", executable.ToolMeegle, valuePointer(config.MeegleExecutablePath))
	if err != nil {
		return process.Result{}, err
	}
	spec.Arguments, spec.Timeout = args, timeout
	if err := commandruntime.CheckAdmission(ctx); err != nil {
		return process.Result{}, err
	}
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

func (s *Service) safeDiagnostic(config domain.AppConfig, message string) string {
	return commandruntime.Redact(config, message)
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
