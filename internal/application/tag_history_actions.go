package application

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

func tagHistoryAction(kind string) bool {
	return kind == "tag.history" || kind == "tag.history.refresh" || kind == "tag.history.delete" || kind == "tag.history.clear"
}

func (a *App) tagHistoryAction(ctx context.Context, action Action) (ActionResult, error) {
	if err := ctx.Err(); err != nil {
		return ActionResult{}, err
	}
	config, err := a.Config.Load()
	if err != nil {
		return ActionResult{}, err
	}
	if expected, provided := action.Payload["tagHistoryHome"].(string); provided && expected != a.Paths.Home {
		return ActionResult{}, errors.New("Tag历史所属应用目录已变化，请刷新后重试")
	}
	if expected, provided := action.Payload["tagHistoryTaskRoot"].(string); provided && expected != value(config.TaskRoot) {
		return ActionResult{}, errors.New("Tag历史任务根目录已变化，请刷新后重试")
	}
	_, scope, err := a.tagHistoryActionScope(config, action)
	if err != nil {
		return ActionResult{}, err
	}
	service := a.delivery(config)
	if action.Kind == "tag.history" {
		items, err := a.readRetainedTagHistoryRecords(ctx, config, scope)
		return ActionResult{Message: "Tag 历史已读取", Data: items}, err
	}
	if action.Kind == "tag.history.refresh" {
		items, err := a.readRetainedTagHistoryRecords(ctx, config, scope)
		if err != nil {
			return ActionResult{}, err
		}
		_, probeErr := service.Probe(ctx, config, scope, true)
		refreshed, readErr := a.readTagHistoryRecords(ctx, config, scope)
		if readErr == nil {
			items = refreshed
		}
		statusErr := errors.Join(probeErr, readErr)
		refresh := TagHistoryRefreshResult{Records: items}
		if statusErr != nil {
			refresh.StatusError = statusErr.Error()
		}
		return ActionResult{Message: "Tag 历史及 Genbu 状态已刷新", Data: refresh}, nil
	}
	var deleted int
	if action.Kind == "tag.history.clear" {
		deleted, err = service.ClearHistory(ctx, config, scope)
	} else {
		ids := historyOperationIDs(action.Payload["operationIds"])
		if len(ids) == 0 {
			return ActionResult{}, errors.New("请选择要删除的Tag构建记录")
		}
		current, readErr := a.readTagHistoryRecords(ctx, config, scope)
		if readErr != nil {
			return ActionResult{}, readErr
		}
		owned := make(map[string]bool, len(current))
		for _, item := range current {
			owned[item.ID] = true
		}
		for _, id := range ids {
			if !owned[id] {
				return ActionResult{}, errors.New("所选Tag记录不属于当前任务或已变化，请刷新后重新选择")
			}
		}
		deleted, err = service.DeleteHistory(ctx, config, scope, ids)
	}
	// Completed deletions cannot be rolled back. Refresh remaining records even
	// after cancellation or a partial filesystem error, without re-running writes.
	items, readErr := a.readTagHistoryRecords(context.WithoutCancel(ctx), config, scope)
	return ActionResult{Message: fmt.Sprintf("已删除 %d 项本地Tag历史", deleted), Data: TagHistoryMutationResult{Deleted: deleted, Records: items}}, errors.Join(err, readErr)
}

// TagHistoryRefreshResult keeps the local history available even when a
// remote Genbu probe fails or only updates part of the task's records.
type TagHistoryRefreshResult struct {
	Records     []TagRecord `json:"records"`
	StatusError string      `json:"statusError,omitempty"`
}

// Both dispatch ports share retention before streaming or returning history.
// An active build owns the task lease; keep its progress readable and retry
// retention from the page's polling coordinator after that lease is released.
func (a *App) readRetainedTagHistoryRecords(ctx context.Context, config domain.AppConfig, tasks []domain.TaskManifest) ([]TagRecord, error) {
	if _, err := a.delivery(config).EnforceHistoryRetention(ctx, config, tasks); err != nil && !errors.Is(err, storage.ErrLockBusy) {
		return nil, err
	}
	return a.readTagHistoryRecords(ctx, config, tasks)
}

func (a *App) readTagHistoryRecords(ctx context.Context, config domain.AppConfig, tasks []domain.TaskManifest) ([]TagRecord, error) {
	items := []TagRecord{}
	service := a.delivery(config)
	var failures []error
	for _, task := range tasks {
		if err := ctx.Err(); err != nil {
			return items, err
		}
		reports, err := service.History(ctx, task.TaskDirectoryName)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		items = append(items, tagRecords(task.TaskDirectoryName, reports)...)
	}
	sortTagHistoryRecords(items)
	return items, errors.Join(failures...)
}

func sortTagHistoryRecords(items []TagRecord) {
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Updated != items[j].Updated {
			return items[i].Updated > items[j].Updated
		}
		return items[i].ID > items[j].ID
	})
}

func historyOperationIDs(value any) []string {
	switch ids := value.(type) {
	case []string:
		return ids
	case []any:
		result := []string{}
		for _, value := range ids {
			if id, ok := value.(string); ok {
				result = append(result, id)
			}
		}
		return result
	}
	return nil
}
