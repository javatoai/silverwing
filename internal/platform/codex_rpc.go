package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/executable"
	"silverwing-go/internal/process"
)

const (
	codexRPCVersion                 = "2.1.8"
	codexRPCMaxFrameBytes           = 4_000_000
	codexRPCMaxOperationStdoutBytes = 32 * 1024 * 1024
	codexRPCMaxDiagnosticBytes      = 2 * 1024 * 1024
)

var errCodexRPCNoResponse = errors.New("Codex 未返回操作结果：配置服务已退出")
var errCodexRPCOutputLimit = errors.New("Codex 配置服务输出超过安全预算")

type codexRPCSessionKey struct{}

// A session exists only inside one synchronous operation. The runner owns its
// process and joins cleanup before that operation returns; no request is replayed.
type codexRPCSession struct {
	service       *Service
	configuration string
	scope         string
	checkConfig   bool
	stdin         io.WriteCloser
	output        <-chan process.StreamEvent
	nextID        int
	cancel        context.CancelFunc
	outputErr     error
	mu            sync.Mutex
}

func codexRPCConfiguration(config domain.AppConfig) string {
	data, _ := json.Marshal([]any{config.CodexExecutablePath,
		config.CommandProxyURL, config.CommandProxyNoProxy, config.CommandProxyUsername,
		config.CommandProxyPassword, config.CommandProxyTargets, os.Getenv("CODEX_HOME")})
	return string(data)
}

func (s *Service) codexRPCScope(config domain.AppConfig, cwd string, isolatedHome *string) string {
	data, _ := json.Marshal([]any{s.paths.Home, codexRPCConfiguration(config), cwd, isolatedHome})
	return string(data)
}

func (s *Service) rpcRequest(ctx context.Context, cfg domain.AppConfig, cwd string, isolatedHome *string, method string, params map[string]any, timeout time.Duration) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if session, ok := ctx.Value(codexRPCSessionKey{}).(*codexRPCSession); ok {
		if session.service != s || session.scope != s.codexRPCScope(cfg, cwd, isolatedHome) {
			return nil, errors.New("Codex 配置服务的目录或配置身份已变化，请重新读取")
		}
		if session.checkConfig {
			current, err := s.loadConfig()
			if err != nil {
				return nil, err
			}
			if codexRPCConfiguration(current) != session.configuration {
				return nil, errors.New("Codex 配置服务的目录或配置身份已变化，请重新读取")
			}
		}
		return session.request(ctx, cfg, method, params, timeout)
	}
	value, err := s.withCodexRPCSession(ctx, cfg, cwd, isolatedHome, timeout, false, func(sessionContext context.Context) (any, error) {
		return s.rpcRequest(sessionContext, cfg, cwd, isolatedHome, method, params, timeout)
	})
	if err != nil {
		return nil, err
	}
	response, _ := value.(map[string]any)
	return response, nil
}

func (s *Service) withCodexRPCSession(ctx context.Context, cfg domain.AppConfig, cwd string, isolatedHome *string, timeout time.Duration, checkConfig bool,
	operation func(context.Context) (any, error)) (any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	_, resolution, _, err := s.resolveCLIExecutableWithSource(ctx, cfg, string(executable.ToolCodex))
	if err != nil {
		return nil, err
	}
	command, err := codexStdioCommand(resolution.Command)
	if err != nil {
		return nil, err
	}
	env, removals := commandEnvironment(cfg, domain.ProxyTargetCodex)
	for name, value := range cliEnvironmentWithRunner(ctx, s.runner, string(executable.ToolCodex), command) {
		env[name] = value
	}
	if isolatedHome != nil {
		env["CODEX_HOME"] = *isolatedHome
	}
	var value any
	var operationErr error
	var session *codexRPCSession
	operationContext, cancelOperation := context.WithCancel(ctx)
	defer cancelOperation()
	result, runErr := s.runner.RunInteractive(operationContext,
		process.Spec{Executable: command, Arguments: []string{"app-server", "--stdio"}, Directory: cwd, Env: env, RemoveEnv: removals,
			// Runner capture is also the aggregate transport budget, so it must
			// allow multiple valid frames and terminate instead of dropping bytes.
			// Its byte limit also bounds an unterminated line; completed protocol
			// frames have the smaller, independent limit checked by request below.
			Timeout: timeout, MaxStdoutBytes: codexRPCMaxOperationStdoutBytes,
			MaxStderrBytes: codexRPCMaxDiagnosticBytes, KillOnOutputLimit: true},
		func(exchangeContext context.Context, stdin io.WriteCloser, output <-chan process.StreamEvent) error {
			defer stdin.Close()
			session = &codexRPCSession{service: s, configuration: codexRPCConfiguration(cfg), scope: s.codexRPCScope(cfg, cwd, isolatedHome),
				checkConfig: checkConfig, stdin: stdin, output: output, nextID: 1, cancel: cancelOperation}
			initialize := map[string]any{"clientInfo": map[string]any{"name": "silverwing", "version": codexRPCVersion},
				"capabilities": map[string]any{"experimentalApi": true}}
			initializeTimeout := timeout
			if checkConfig {
				initializeTimeout = min(initializeTimeout, 30*time.Second)
			}
			if _, err := session.request(exchangeContext, cfg, "initialize", initialize, initializeTimeout); err != nil {
				operationErr = err
				return nil
			}
			if err := session.write(map[string]any{"method": "initialized"}); err != nil {
				operationErr = err
				return nil
			}
			value, operationErr = operation(context.WithValue(exchangeContext, codexRPCSessionKey{}, session))
			if operationErr == nil && checkConfig {
				current, err := s.loadConfig()
				if err != nil {
					operationErr = err
				} else if session.scope != s.codexRPCScope(current, cwd, isolatedHome) {
					operationErr = errors.New("Codex 配置服务的目录或配置身份已变化，请重新读取")
				}
			}
			return nil
		},
	)
	var frameErr error
	if session != nil {
		// RunInteractive joins the exchange before returning, including output
		// limit cancellation and process-tree cleanup, so its final error is stable.
		frameErr = session.outputErr
	}
	if err := codexRPCOutputFailure(cfg, result, runErr, operationErr, frameErr); err != nil {
		return nil, err
	}
	if runErr != nil {
		return nil, wrapCommandDisplayError(fmt.Sprintf("Codex 配置服务错误：%s", redactProxy(cfg, runErr.Error())), errors.Join(runErr, operationErr))
	}
	if errors.Is(operationErr, errCodexRPCNoResponse) {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			message = strings.TrimSpace(result.Stdout)
		}
		if message == "" {
			message = fmt.Sprintf("Codex 配置服务退出，退出码 %d", result.ExitCode)
		}
		return nil, wrapCommandDisplayError("Codex 未返回操作结果："+redactProxy(cfg, truncate(message, 2000)), operationErr)
	}
	if operationErr != nil {
		return nil, operationErr
	}
	return value, nil
}

func codexRPCOutputFailure(cfg domain.AppConfig, result process.Result, runErr, operationErr, frameErr error) error {
	budgetErr := frameErr
	if budgetErr == nil && result.StdoutTruncated {
		budgetErr = fmt.Errorf("%w：会话累计标准输出超过 %d 字节，已停止本次操作", errCodexRPCOutputLimit, codexRPCMaxOperationStdoutBytes)
	}
	if budgetErr == nil && result.StderrTruncated {
		budgetErr = fmt.Errorf("%w：诊断输出超过 %d 字节，已停止本次操作", errCodexRPCOutputLimit, codexRPCMaxDiagnosticBytes)
	}
	if budgetErr == nil {
		return nil
	}
	cause := errors.Join(budgetErr, runErr, operationErr)
	message := budgetErr.Error()
	if process.IsCleanupFailure(cause) {
		message += "；进程清理未确认"
	}
	return wrapCommandDisplayError(redactProxy(cfg, message), cause)
}

func (s *codexRPCSession) write(value map[string]any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = s.stdin.Write(append(encoded, '\n'))
	return err
}

func (s *codexRPCSession) request(ctx context.Context, cfg domain.AppConfig, method string, params map[string]any, timeout time.Duration) (map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.outputErr != nil {
		return nil, s.outputErr
	}
	requestContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := requestContext.Err(); err != nil {
		return nil, err
	}
	id := s.nextID
	s.nextID++
	message := map[string]any{"id": id, "method": method}
	if params != nil {
		message["params"] = params
	}
	if err := s.write(message); err != nil {
		return nil, wrapCommandDisplayError("Codex 配置服务未能写入请求", err)
	}
	for {
		select {
		case <-requestContext.Done():
			return nil, requestContext.Err()
		case event, ok := <-s.output:
			if !ok {
				return nil, errCodexRPCNoResponse
			}
			if event.Stream != "STDOUT" {
				continue
			}
			if len(event.Text) > codexRPCMaxFrameBytes {
				s.outputErr = fmt.Errorf("%w：单帧响应超过 %d 字节，已停止本次操作", errCodexRPCOutputLimit, codexRPCMaxFrameBytes)
				s.cancel()
				return nil, s.outputErr
			}
			var envelope map[string]json.RawMessage
			if json.Unmarshal([]byte(strings.TrimSpace(event.Text)), &envelope) != nil {
				continue
			}
			rawID, hasID := envelope["id"]
			if hasID && len(envelope["method"]) > 0 && string(envelope["method"]) != "null" {
				if err := s.write(map[string]any{"id": json.RawMessage(rawID), "error": map[string]any{"code": -32601, "message": "Interactive requests are not supported in this view"}}); err != nil {
					return nil, err
				}
				continue
			}
			var responseID int
			if !hasID || json.Unmarshal(rawID, &responseID) != nil || responseID != id {
				continue
			}
			if codexRPCHasError(envelope) {
				remoteError, remoteCode := codexRPCErrorMessage(envelope["error"])
				message := categorizeCodexError(redactProxy(cfg, remoteError))
				if message == "Codex 未完成此操作，请检查配置后重试" {
					message = fmt.Sprintf("Codex 未完成此操作（%s），请检查配置后重试", remoteCode)
				}
				return nil, errors.New(redactProxy(cfg, message))
			}
			var response map[string]any
			if rawResult, exists := envelope["result"]; exists {
				_ = json.Unmarshal(rawResult, &response)
			}
			if response == nil {
				response = map[string]any{}
			}
			return response, nil
		}
	}
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
