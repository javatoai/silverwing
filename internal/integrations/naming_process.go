package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

func namingIsolationArguments() []string {
	args := []string{}
	for _, setting := range []string{
		"features.plugins=false", "features.apps=false", "features.hooks=false", "features.shell_snapshot=false",
		"analytics.enabled=false", `otel.exporter="none"`, `otel.trace_exporter="none"`, `otel.metrics_exporter="none"`,
	} {
		args = append(args, "-c", setting)
	}
	return args
}

// MCP maps are merged by Codex, so an empty table does not remove inherited
// servers. Discover their names with plugins already disabled, then explicitly
// disable each server without importing or logging its transport credentials.
func (s *Service) prepareNamingIsolation(ctx context.Context, config domain.AppConfig, spec process.Spec) ([]string, error) {
	args := namingIsolationArguments()
	spec.Arguments = append(append([]string{}, args...), "mcp", "list", "--json")
	spec.Stdin = nil
	spec.Timeout = 5 * time.Second
	spec.MaxStdoutBytes, spec.MaxStderrBytes, spec.KillOnOutputLimit = 1024*1024, 64*1024, true
	result, err := s.runner.Run(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("命名隔离准备失败：%s", s.safeDiagnostic(config, err.Error()))
	}
	if !result.Successful() || result.StdoutTruncated || result.StderrTruncated {
		return nil, fmt.Errorf("命名隔离准备失败：无法读取 Codex MCP 配置（退出码 %d）", result.ExitCode)
	}
	var servers []struct {
		Name string `json:"name"`
	}
	decoder := json.NewDecoder(strings.NewReader(result.Stdout))
	if err := decoder.Decode(&servers); err != nil {
		return nil, errors.New("命名隔离准备失败：Codex MCP 配置返回格式无效")
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, errors.New("命名隔离准备失败：Codex MCP 配置包含多余内容")
	}
	entries := map[string]bool{}
	for _, server := range servers {
		if strings.TrimSpace(server.Name) == "" || strings.ContainsRune(server.Name, 0) {
			return nil, errors.New("命名隔离准备失败：Codex MCP 名称无效")
		}
		entries[server.Name] = true
	}
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, name)
	}
	sort.Strings(names)
	fields := make([]string, 0, len(names))
	for _, name := range names {
		quoted, _ := json.Marshal(name)
		fields = append(fields, string(quoted)+"={enabled=false}")
	}
	if len(fields) > 0 {
		args = append(args, "-c", "mcp_servers={"+strings.Join(fields, ",")+"}")
	}
	return args, nil
}

func namingExecutionArguments(isolation []string, model, schemaPath, outputPath, directory string, lowReasoning bool) []string {
	args := append([]string{}, isolation...)
	if lowReasoning {
		args = append(args, "-c", `model_reasoning_effort="low"`)
	}
	return append(args, "--model", model, "--ask-for-approval", "never", "exec", "-", "--ephemeral", "--skip-git-repo-check", "--sandbox", "read-only", "--output-schema", schemaPath, "--output-last-message", outputPath, "--cd", directory)
}
