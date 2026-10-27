package application

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
)

func (a *App) refreshTasks(ctx context.Context) (result ActionResult, err error) {
	if err := ctx.Err(); err != nil {
		return result, err
	}
	config, err := a.Config.Load()
	if err != nil {
		return result, err
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	service := a.taskService(config)
	if config.TaskRoot != nil {
		scan, e := a.Manifests.Scan(*config.TaskRoot)
		if e != nil {
			if cancelled := ctx.Err(); cancelled != nil {
				return result, cancelled
			}
			return result, e
		}
		if err := ctx.Err(); err != nil {
			return result, err
		}
		for _, entry := range scan.Current {
			if err := ctx.Err(); err != nil {
				return result, err
			}
			if filepath.Base(entry.Path) != entry.Manifest.TaskDirectoryName {
				return result, fmt.Errorf("任务目录标识不一致：%s", entry.Path)
			}
			if _, e := service.RefreshAgents(config, entry.Path); e != nil {
				if cancelled := ctx.Err(); cancelled != nil {
					return result, cancelled
				}
				return result, e
			}
			if err := ctx.Err(); err != nil {
				return result, err
			}
		}
	}
	snapshot, err := a.Snapshot(ctx)
	if cancelled := ctx.Err(); cancelled != nil {
		return ActionResult{}, cancelled
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ActionResult{}, err
	}
	result = ActionResult{Message: "任务已刷新", Data: snapshot}
	appendMaterialsCatalogWarnings(&result, service.Documents)
	return result, err
}
