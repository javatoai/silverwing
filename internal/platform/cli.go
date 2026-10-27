package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"silverwing-go/internal/process"
)

func (s *Service) executeCLI(ctx context.Context, kind string, payload map[string]any) (any, error) {
	tool := strings.ToLower(strings.TrimSpace(payloadString(payload, "tool")))
	if tool == "" {
		return nil, fmt.Errorf("CLI 状态请求缺少工具名称")
	}
	if _, ok := cliProxyTarget(tool); !ok {
		return nil, fmt.Errorf("不支持的 CLI：%s", tool)
	}
	cfg, err := s.loadConfig()
	if err != nil {
		return nil, err
	}
	cfg, resolution, target, err := s.resolveCLIExecutableWithSourceForce(ctx, cfg, tool, payloadBool(payload, "force"))
	if err != nil {
		return nil, err
	}
	command := resolution.Command
	env := cliEnvironmentWithRunner(ctx, s.runner, tool, command)
	versionResult, versionErr := s.runToolWithEnv(ctx, cfg, target, command, []string{"--version"}, "", 10*time.Second, env)
	if versionErr != nil {
		if process.IsCleanupFailure(versionErr) {
			return nil, versionErr
		}
		return map[string]any{"tool": tool, "command": command, "source": string(resolution.Source), "installed": false, "version": nil, "error": redactProxy(cfg, versionErr.Error())}, nil
	}
	cfg, err = s.persistDiscoveredCLIExecutable(ctx, cfg, tool, resolution)
	if err != nil {
		return nil, err
	}
	version := firstNonBlank(versionResult.Stdout, versionResult.Stderr)
	if version == "" {
		version = "未知"
	}
	status := map[string]any{"tool": tool, "command": command, "source": string(resolution.Source), "installed": true, "version": version, "authenticated": false, "authenticationState": "NOT_CHECKED"}
	if tool == "meegle" {
		result, authErr := s.runToolWithEnv(ctx, cfg, target, command, []string{"auth", "status", "--format", "json"}, "", 15*time.Second, env)
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

func authBool(value map[string]any) bool {
	if result, ok := value["authenticated"].(bool); ok {
		return result
	}
	if nested, ok := value["data"].(map[string]any); ok {
		return authBool(nested)
	}
	return false
}
