package application

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"silverwing-go/internal/domain"
)

// The stable owner is the manifest's task directory identity. Requirement
// links and display names may change or repeat and are never deletion keys.
func (a *App) tagHistoryActionScope(config domain.AppConfig, action Action) ([]domain.TaskManifest, []domain.TaskManifest, error) {
	mutation := action.Kind == "tag.history.delete" || action.Kind == "tag.history.clear"
	if strings.TrimSpace(action.TaskID) == "" {
		return nil, nil, errors.New("查看或清理Tag历史必须指定当前研发任务")
	}
	_, scopedRequest := action.Payload["tagHistoryTaskPath"]
	if mutation || scopedRequest {
		for _, key := range []string{"tagHistoryHome", "tagHistoryTaskRoot", "tagHistoryTaskPath", "tagHistoryTaskCreatedAt"} {
			if text, ok := action.Payload[key].(string); !ok || strings.TrimSpace(text) == "" {
				return nil, nil, errors.New("缺少Tag历史所属任务身份，请刷新后重新确认")
			}
		}
	}
	if expected, ok := action.Payload["tagHistoryHome"].(string); ok && expected != a.Paths.Home {
		return nil, nil, errors.New("Tag历史所属应用目录已变化，请刷新后重试")
	}
	if expected, ok := action.Payload["tagHistoryTaskRoot"].(string); ok && expected != value(config.TaskRoot) {
		return nil, nil, errors.New("Tag历史任务根目录已变化，请刷新后重试")
	}
	if config.TaskRoot == nil || strings.TrimSpace(*config.TaskRoot) == "" {
		return nil, nil, fmt.Errorf("找不到唯一研发任务：%s", action.TaskID)
	}
	entry, err := a.Manifests.LoadTask(*config.TaskRoot, action.TaskID)
	if err != nil {
		return nil, nil, err
	}
	if expected, ok := action.Payload["tagHistoryTaskPath"].(string); ok && !sameTagHistoryTaskPath(expected, entry.Path) {
		return nil, nil, errors.New("Tag历史所属任务目录已变化，请重新确认")
	}
	if expected, ok := action.Payload["tagHistoryTaskCreatedAt"].(string); ok && expected != entry.Manifest.CreatedAt {
		return nil, nil, errors.New("Tag历史所属任务已重新创建，请重新确认")
	}
	scope := []domain.TaskManifest{entry.Manifest}
	return scope, scope, nil
}

func sameTagHistoryTaskPath(left, right string) bool {
	a, leftErr := filepath.Abs(left)
	b, rightErr := filepath.Abs(right)
	return leftErr == nil && rightErr == nil && strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}
