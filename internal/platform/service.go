package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
)

// Service implements local platform integrations used by the desktop.
// It receives an explicit SilverWing home so its durable state never depends on
// an implicit process-wide working directory.
type Service struct {
	paths          storage.Paths
	config         *storage.ConfigStore
	runner         *process.Runner
	codexURIOpener func(context.Context, string) error
	authMu         sync.Mutex
	authSessions   map[string]*cliAuthSession
}

func NewService(paths storage.Paths, config *storage.ConfigStore, runner *process.Runner) *Service {
	if config == nil {
		config = storage.NewConfigStore(paths)
	}
	if runner == nil {
		runner = process.NewRunner()
	}
	return &Service{paths: paths, config: config, runner: runner, authSessions: map[string]*cliAuthSession{}}
}

// Execute invokes one local platform operation. External writes require an
// explicit `confirmed: true` field; reads and previews never modify Codex or
// install files.
func (s *Service) Execute(ctx context.Context, kind string, payload map[string]any) (any, error) {
	if payload == nil {
		payload = map[string]any{}
	}
	kind = strings.ToLower(strings.TrimSpace(kind))
	switch kind {
	case "skills.sources/save", "skills.sources/remove", "skills.sources/catalog", "skills.catalog", "skills.files/list", "skills.files/preview", "skills.files/copy-path", "skills.preview", "skills.preview/path", "skills.install", "skills.update", "skills.remove":
		return s.executeSkills(ctx, kind, payload)
	case "codex.extensions/snapshot", "codex.extensions/marketplaces/list", "codex.extensions/marketplaces/add-source", "codex.extensions/marketplaces/refresh-source", "codex.extensions/marketplaces/remove-source",
		"codex.extensions/branches/list", "skills.sources/branches/list",
		"codex.extensions/plugin/install", "codex.extensions/plugin/uninstall", "codex.extensions/plugins/install", "codex.extensions/plugins/uninstall",
		"codex.extensions/plugin/preview", "codex.extensions/plugin/skill-preview",
		"codex.extensions/mcp/status", "codex.extensions/mcp/check", "codex.extensions/mcp/set", "codex.extensions/mcp/add", "codex.extensions/mcp/edit", "codex.extensions/mcp/remove":
		return s.executeCodexExtensions(ctx, kind, payload)
	case "codex.ai/snapshot", "codex.ai/threads/list", "codex.ai/threads/bind", "codex.ai/file/read", "codex.ai/mcp/authenticate", "codex.ai/open":
		return s.executeCodexAI(ctx, kind, payload)
	case "cli.status":
		return s.executeCLI(ctx, kind, payload)
	case "cli.auth.start", "cli.auth.session", "cli.auth.poll", "cli.auth.cancel", "cli.auth.logout":
		return s.executeCLIAuth(ctx, kind, payload)
	case "terminal.resolve":
		return s.resolveTerminal(payload)
	case "proxy.test":
		return s.proxyTest(ctx)
	case "git.version":
		return s.gitVersion(ctx)
	case "diagnostics.export":
		return s.exportDiagnostics(ctx, payload)
	case "logs.list", "logs.read":
		return s.executeLogs(kind, payload)
	case "logs.clear":
		return s.clearApplicationLogs(ctx, payload)
	case "update.check":
		return map[string]any{
			"currentVersion": domain.CurrentProductVersion,
			"releasesUrl":    "https://github.com/javatoai/silverwing/releases?q=product-v&expanded=true",
			"automatic":      false,
		}, nil
	default:
		return nil, fmt.Errorf("未知平台操作：%s", kind)
	}
}

func (s *Service) loadConfig() (domain.AppConfig, error) { return s.config.Load() }

func confirmed(payload map[string]any) bool {
	value, _ := payload["confirmed"].(bool)
	return value
}

func requireConfirmed(payload map[string]any, operation string) error {
	if !confirmed(payload) {
		return fmt.Errorf("%s需要用户确认", operation)
	}
	return nil
}

func payloadString(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

func payloadBool(payload map[string]any, key string) bool {
	value, _ := payload[key].(bool)
	return value
}

func decodePayload[T any](payload map[string]any, key string) (T, error) {
	var value T
	raw, ok := payload[key]
	if !ok {
		return value, fmt.Errorf("缺少 %s", key)
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return value, fmt.Errorf("解析 %s：%w", key, err)
	}
	if err := json.Unmarshal(encoded, &value); err != nil {
		return value, fmt.Errorf("解析 %s：%w", key, err)
	}
	return value, nil
}
