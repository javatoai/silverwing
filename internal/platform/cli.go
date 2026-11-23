package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"silverwing-go/internal/executable"
	"silverwing-go/internal/process"
)

func (s *Service) executeCLI(ctx context.Context, kind string, payload map[string]any) (any, error) {
	tool := strings.ToLower(strings.TrimSpace(payloadString(payload, "tool")))
	if tool == "" {
		return nil, fmt.Errorf("CLI 状态请求缺少工具名称")
	}
	cliTool := executable.Tool(tool)
	if !isSupportedCLI(cliTool) {
		return nil, fmt.Errorf("不支持的 CLI：%s", tool)
	}
	cfg, err := s.loadConfig()
	if err != nil {
		return nil, err
	}
	optional := isOptionalCLI(cliTool)
	if optional && !cfg.OptionalCLIEnabled(tool) {
		return disabledCLIStatus(tool, cfg.OptionalCLIPath(tool)), nil
	}
	force, versionOnly := payloadBool(payload, "force"), payloadBool(payload, "versionOnly")
	deferDiscovery := optional && versionOnly && payloadBool(payload, "deferDiscovery")
	if cliTool == executable.ToolLark {
		return s.larkCLIStatus(ctx, cfg, force, versionOnly, deferDiscovery)
	}
	resolution, err := s.resolveCLIExecutableWithSourceForce(ctx, cfg, cliTool, force)
	if err != nil {
		return nil, err
	}
	command := resolution.Command
	env := cliEnvironmentWithRunner(ctx, s.runner, cliTool, command)
	versionResult, versionErr := s.runToolWithEnv(ctx, cfg, cliTool, command, []string{"--version"}, "", 10*time.Second, env)
	clearedPath := ""
	if cliTool == executable.ToolCodex && resolution.Source == executable.SourceConfigured && versionErr != nil &&
		ctx.Err() == nil && !errors.Is(versionErr, context.Canceled) && !process.IsCleanupFailure(versionErr) {
		cfg, err = s.clearFailedCodexExecutable(ctx, command)
		if err != nil {
			return nil, err
		}
		clearedPath = command
		resolution = executable.NewResolver(s.runner).ResolveCodexReplacement(ctx, clearedPath)
		command = resolution.Command
		if command != "" {
			env = cliEnvironmentWithRunner(ctx, s.runner, cliTool, command)
			versionResult, versionErr = s.runToolWithEnv(ctx, cfg, cliTool, command, []string{"--version"}, "", 10*time.Second, env)
		}
	}
	if process.IsCleanupFailure(versionErr) {
		return nil, versionErr
	}
	if clearedPath != "" && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if versionErr == nil && !deferDiscovery {
		cfg, err = s.persistDiscoveredCLIExecutable(ctx, cfg, cliTool, resolution)
		if err != nil && (clearedPath == "" || ctx.Err() != nil) {
			return nil, err
		}
		versionErr = err
	}
	status := map[string]any{"tool": tool, "command": command, "source": string(resolution.Source), "installed": versionErr == nil, "version": nil}
	if optional {
		status["enabled"] = true
	}
	if clearedPath != "" {
		status["clearedConfiguredPath"] = clearedPath
	}
	if versionErr != nil {
		status["error"] = redactProxy(cfg, versionErr.Error())
		return status, nil
	}
	version := firstNonBlank(versionResult.Stdout, versionResult.Stderr)
	if version == "" {
		version = "未知"
	}
	status["version"] = version
	status["authenticated"] = false
	status["authenticationState"] = "NOT_CHECKED"
	if tool == "meegle" && !versionOnly {
		result, authErr := s.runToolWithEnv(ctx, cfg, cliTool, command, []string{"auth", "status", "--format", "json"}, "", 15*time.Second, env)
		if authErr != nil {
			if process.IsCleanupFailure(authErr) {
				return nil, authErr
			}
			status["authenticationState"] = "CHECK_FAILED"
			status["authenticationError"] = redactProxy(cfg, authErr.Error())
			return status, nil
		}
		var auth map[string]any
		if json.Unmarshal([]byte(result.Stdout), &auth) != nil {
			status["authenticationState"] = "CHECK_FAILED"
			status["authenticationError"] = "Meegle 登录状态 JSON 解析失败"
			return status, nil
		}
		status["authenticated"] = authBool(auth)
		status["authenticationState"] = map[bool]string{true: "AUTHENTICATED", false: "LOGIN_REQUIRED"}[authBool(auth)]
		if value, ok := auth["host"].(string); ok {
			status["host"] = value
		}
		if value, ok := auth["expiresInMinutes"]; ok {
			status["expiresInMinutes"] = value
		} else if value, ok := auth["expires_in_minutes"]; ok {
			status["expiresInMinutes"] = value
		}
	}
	return status, nil
}

func disabledCLIStatus(tool, command string) map[string]any {
	return map[string]any{"tool": tool, "command": command, "enabled": false, "installed": false, "version": nil, "authenticationState": "NOT_CHECKED"}
}

func authBool(value map[string]any) bool {
	if result, ok := value["authenticated"].(bool); ok {
		return result
	}
	if nested, ok := value["data"].(map[string]any); ok {
		return authBool(nested)
	}
	return false
}
