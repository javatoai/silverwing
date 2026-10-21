package desktop

import (
	"silverwing-go/internal/application"
)

// applyTaskSnapshotSelection runs after the refreshed snapshot has been
// applied. A create result is only selected when that snapshot contains the
// task, matching the Source flow where reload completes before navigation.
func (s *app) applyTaskSnapshotSelection(action application.Action, result application.ActionResult) bool {
	if action.Kind != "task.create" {
		return false
	}
	id := mapString(mapData(result.Data), "taskDirectoryName")
	if id == "" {
		s.lastError = "任务已创建，但返回结果缺少任务目录标识，无法自动选中。"
		return true
	}
	for index, item := range s.tasks {
		createdAt := mapString(mapData(result.Data), "createdAt")
		if item.id != id || createdAt != "" && item.createdAt != createdAt {
			continue
		}
		if s.selectTaskIndex(index) {
			s.page = "tasks"
			s.revealTaskInIndex(item)
		}
		return true
	}
	s.page = "tasks"
	s.lastError = "任务已创建，但刷新后的任务列表中未找到该任务；请刷新后重试。"
	return true
}
