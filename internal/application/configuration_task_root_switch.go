package application

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"silverwing-go/internal/domain"
)

// Only the explicitly confirmed direct-switch action reaches this helper.
// The checkpoint guard and save run under the same Config.Update lock.
func (a *App) prepareTaskRootSwitch(ctx context.Context, current, next domain.AppConfig) (domain.AppConfig, error) {
	if err := ctx.Err(); err != nil {
		return current, err
	}
	if next.TaskRoot == nil || strings.TrimSpace(*next.TaskRoot) == "" {
		return current, errors.New("请选择任务存放目录")
	}
	root, err := filepath.Abs(strings.TrimSpace(*next.TaskRoot))
	if err != nil {
		return current, fmt.Errorf("规范任务存放目录失败：%w", err)
	}
	pending, err := a.TaskRootMigrationPending(ctx)
	if err != nil {
		return current, fmt.Errorf("无法确认任务目录恢复状态，不能直接切换：%w", err)
	}
	if pending == nil || *pending {
		return current, errors.New("存在未完成的任务目录迁移，请先完成恢复后再直接切换目录")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return current, fmt.Errorf("创建任务存放目录失败：%w", err)
	}
	probe, err := os.CreateTemp(root, ".silverwing-directory-check-")
	if err != nil {
		return current, fmt.Errorf("任务存放目录不可写：%w", err)
	}
	probePath := probe.Name()
	_, writeErr := probe.Write([]byte("silverwing"))
	probeErr := errors.Join(writeErr, probe.Close(), os.Remove(probePath))
	if probeErr != nil {
		return current, fmt.Errorf("校验任务存放目录可写失败：%w", probeErr)
	}
	if err := ctx.Err(); err != nil {
		return current, err
	}
	next.TaskRoot = &root
	return next, nil
}

func (a *App) saveTaskRootSwitch(ctx context.Context, action Action) (ActionResult, error) {
	if !boolPayload(action, "confirmed") {
		return ActionResult{}, errors.New("请先选择直接切换任务存放目录")
	}
	root := strings.TrimSpace(action.Path)
	if root == "" {
		return ActionResult{}, errors.New("请选择任务存放目录")
	}
	updated, err := a.Config.Update(func(current domain.AppConfig) (domain.AppConfig, error) {
		if expected, exists := action.Payload["expectedTaskRoot"]; exists {
			expectedRoot, valid := expected.(string)
			if !valid || value(current.TaskRoot) != expectedRoot {
				return current, errors.New("任务存放目录已变化，请重新选择")
			}
		}
		next := current
		next.TaskRoot = &root
		return a.prepareTaskRootSwitch(ctx, current, next)
	})
	return ActionResult{Message: "任务存放目录已切换，旧任务数据保留在原目录", Path: value(updated.TaskRoot)}, err
}
