package delivery

import (
	"context"
	"errors"
	"strings"

	"silverwing-go/internal/domain"
)

// UpdateTarget changes only the selected task's future merge-mode builds.
// Historical retries continue resolving the destination saved by the operation.
func (s *Service) UpdateTarget(ctx context.Context, taskFolder, selection string, expectedTarget *string, targetRef string) (domain.TaskManifest, error) {
	if err := ctx.Err(); err != nil {
		return domain.TaskManifest{}, err
	}
	task, err := s.resolveTask(taskFolder)
	if err != nil {
		return domain.TaskManifest{}, err
	}
	if !task.config.AllowTaskTagTargetEditing {
		return domain.TaskManifest{}, errors.New("任务测试目标分支修改已关闭")
	}
	workspace, found, err := resolveWorkspace(task.manifest, selection)
	if err != nil {
		return domain.TaskManifest{}, err
	}
	if !found {
		return domain.TaskManifest{}, errors.New("任务中不存在测试Tag目标模块")
	}
	if err := ctx.Err(); err != nil {
		return domain.TaskManifest{}, err
	}
	return s.tasks.UpdateTagTarget(task.config, task.directory, workspace.GroupServiceID, workspace.ModuleID, expectedTarget, strings.TrimSpace(targetRef))
}

func (s *Service) updateTargetPayload(ctx context.Context, payload map[string]any) (domain.TaskManifest, error) {
	keys := stringSlice(payload, "selectionKeys")
	if len(keys) != 1 {
		return domain.TaskManifest{}, errors.New("请选择一个测试Tag目标模块")
	}
	raw, exists := payload["expectedTargetRef"]
	if !exists {
		return domain.TaskManifest{}, errors.New("缺少原测试目标分支，请重新打开设置")
	}
	var expected *string
	if raw != nil {
		text, ok := raw.(string)
		if !ok {
			return domain.TaskManifest{}, errors.New("原测试目标分支格式错误")
		}
		expected = &text
	}
	return s.UpdateTarget(ctx, stringValue(payload, "taskFolder", "taskId", "task"), keys[0], expected, stringValue(payload, "targetRef"))
}
