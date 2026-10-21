package delivery

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

type Service struct {
	paths       storage.Paths
	config      *storage.ConfigStore
	manifests   *storage.ManifestStore
	tasks       *workspace.TaskApplicationService
	preparation PreparationAPI
}

func NewService(paths storage.Paths, config *storage.ConfigStore, manifests *storage.ManifestStore, git *workspace.GitClient, runner *process.Runner) *Service {
	if config == nil {
		config = storage.NewConfigStore(paths)
	}
	if manifests == nil {
		manifests = storage.NewManifestStore()
	}
	if git == nil {
		git = workspace.NewGitClient(runner, "")
	}
	return &Service{paths: paths, config: config, manifests: manifests, tasks: workspace.NewTaskApplicationService(manifests, git, paths)}
}
func (s *Service) Execute(ctx context.Context, kind string, payload map[string]any) (any, error) {
	switch kind {
	case "agent.handoff-template":
		return handoffTemplate(), nil
	case "agent.inspect", "agent.plan", "agent.apply", "agent.status":
		return s.executeAgent(ctx, kind, payload)
	default:
		return nil, fmt.Errorf("任务操作不支持：%s", kind)
	}
}
func handoffTemplate() string {
	return "# 任务交接\n\n## 目标\n\n- 待补充。\n\n## 范围与边界\n\n- 待补充。\n\n## 已验证事实\n\n- 待补充。\n\n## 风险与未决项\n\n- 待补充。\n\n## 建议下一步\n\n- 待补充。"
}

func value(pointer *string) string {
	if pointer == nil {
		return ""
	}
	return *pointer
}

func stringPointer(value string) *string { return &value }
func nonEmptyPointer(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func stringValue(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func stringSlice(payload map[string]any, keys ...string) []string {
	for _, key := range keys {
		switch values := payload[key].(type) {
		case []string:
			return append([]string(nil), values...)
		case []any:
			result := make([]string, 0, len(values))
			for _, value := range values {
				if text, ok := value.(string); ok {
					result = append(result, text)
				}
			}
			return result
		}
	}
	return nil
}

func formatSilverwingTime(now time.Time) string {
	zone, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		zone = time.FixedZone("CST", 8*60*60)
	}
	return now.In(zone).Format("2006-01-02 15:04:05")
}

func canonicalPath(value string) string {
	abs, err := filepath.Abs(value)
	if err != nil {
		abs = filepath.Clean(value)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(real)
	}
	return filepath.Clean(abs)
}

func samePath(left, right string) bool {
	left = canonicalPath(left)
	right = canonicalPath(right)
	if runtime.GOOS == "windows" {
		left, right = strings.ToLower(left), strings.ToLower(right)
	}
	return filepath.Clean(left) == filepath.Clean(right)
}
