package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/executable"
	"silverwing-go/internal/process"
)

const codexRPCVersion = "2.1.8"

func (s *Service) rpcRequest(ctx context.Context, cfg domain.AppConfig, cwd string, isolatedHome *string, method string, params map[string]any, timeout time.Duration) (map[string]any, error) {
	_, resolution, _, err := s.resolveCLIExecutableWithSource(ctx, cfg, string(executable.ToolCodex))
	if err != nil {
		return nil, err
	}
	command, err := codexStdioCommand(resolution.Command)
	if err != nil {
		return nil, err
	}
	args := []string{"app-server", "--stdio"}
	env, removals := commandEnvironment(cfg, domain.ProxyTargetCodex)
	for name, value := range cliEnvironmentWithRunner(ctx, s.runner, string(executable.ToolCodex), command) {
		env[name] = value
	}
	if isolatedHome != nil {
		env["CODEX_HOME"] = *isolatedHome
	}
	message := func(id int, method string, params map[string]any) map[string]any {
		value := map[string]any{"id": id, "method": method}
		if params != nil {
			value["params"] = params
		}
		return value
	}
	initialize := map[string]any{"clientInfo": map[string]any{"name": "silverwing", "version": codexRPCVersion}, "capabilities": map[string]any{"experimentalApi": true}}
	var response map[string]any
	var remoteError, remoteErrorCode string
	result, err := s.runner.RunInteractive(ctx,
		process.Spec{Executable: command, Arguments: args, Directory: cwd, Env: env, RemoveEnv: removals, Timeout: timeout},
		func(exchangeContext context.Context, stdin io.WriteCloser, output <-chan process.StreamEvent) error {
			defer stdin.Close()
			write := func(value map[string]any) error {
				encoded, err := json.Marshal(value)
				if err != nil {
					return err
				}
				encoded = append(encoded, '\n')
				_, err = stdin.Write(encoded)
				return err
			}
			if err := write(message(1, "initialize", initialize)); err != nil {
				return err
			}
			initialized := false
			for {
				select {
				case <-exchangeContext.Done():
					return exchangeContext.Err()
				case event, ok := <-output:
					if !ok {
						return nil
					}
					if event.Stream != "STDOUT" || len(event.Text) > 4_000_000 {
						continue
					}
					var envelope map[string]json.RawMessage
					if json.Unmarshal([]byte(strings.TrimSpace(event.Text)), &envelope) != nil {
						continue
					}
					rawID, hasID := envelope["id"]
					if hasID && len(envelope["method"]) > 0 && string(envelope["method"]) != "null" {
						if err := write(map[string]any{"id": json.RawMessage(rawID), "error": map[string]any{"code": -32601, "message": "Interactive requests are not supported in this view"}}); err != nil {
							return err
						}
						continue
					}
					if !hasID {
						continue
					}
					var id int
					if json.Unmarshal(rawID, &id) != nil {
						continue
					}
					if id == 1 && !initialized {
						if codexRPCHasError(envelope) {
							remoteError, remoteErrorCode = codexRPCErrorMessage(envelope["error"])
							return nil
						}
						if err := write(map[string]any{"method": "initialized"}); err != nil {
							return err
						}
						if err := write(message(2, method, params)); err != nil {
							return err
						}
						initialized = true
						continue
					}
					if id == 2 && initialized {
						if codexRPCHasError(envelope) {
							remoteError, remoteErrorCode = codexRPCErrorMessage(envelope["error"])
							return nil
						}
						if rawResult, exists := envelope["result"]; exists {
							_ = json.Unmarshal(rawResult, &response)
						}
						if response == nil {
							response = map[string]any{}
						}
						return nil
					}
				}
			}
		},
	)
	if err != nil {
		return nil, wrapCommandDisplayError(fmt.Sprintf("Codex 配置服务错误：%s", redactProxy(cfg, err.Error())), err)
	}
	if remoteError != "" {
		message := categorizeCodexError(redactProxy(cfg, remoteError))
		if message == "Codex 未完成此操作，请检查配置后重试" {
			message = fmt.Sprintf("Codex 未完成此操作（%s），请检查配置后重试", remoteErrorCode)
		}
		return nil, fmt.Errorf("%s", redactProxy(cfg, message))
	}
	if response == nil {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			message = strings.TrimSpace(result.Stdout)
		}
		if message == "" {
			message = fmt.Sprintf("Codex 配置服务退出，退出码 %d", result.ExitCode)
		}
		return nil, fmt.Errorf("Codex 未返回操作结果：%s", redactProxy(cfg, truncate(message, 2000)))
	}
	return response, nil
}

func codexRPCHasError(envelope map[string]json.RawMessage) bool {
	raw, exists := envelope["error"]
	return exists && len(raw) > 0 && string(raw) != "null"
}

func codexRPCErrorMessage(raw json.RawMessage) (message, code string) {
	message = "Codex 未完成此操作"
	code = "未知错误"
	var record map[string]json.RawMessage
	if json.Unmarshal(raw, &record) == nil {
		var text string
		if json.Unmarshal(record["message"], &text) == nil && text != "" {
			message = text
		}
		if rawCode, exists := record["code"]; exists {
			code = string(rawCode)
		}
	}
	return message, code
}
