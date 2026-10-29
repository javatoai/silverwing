package desktop

import (
	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

// Navigation consumes these two identities, independently of the persisted
// manifest schema. Native results are projected without an encoding round trip.
type taskCreationNavigation struct {
	TaskDirectoryName string
	CreatedAt         string
}

func taskCreationNavigationData(data any) taskCreationNavigation {
	switch created := data.(type) {
	case domain.TaskManifest:
		return taskCreationNavigation{created.TaskDirectoryName, created.CreatedAt}
	case *domain.TaskManifest:
		if created != nil {
			return taskCreationNavigation{created.TaskDirectoryName, created.CreatedAt}
		}
		return taskCreationNavigation{}
	default:
		// Preserve the legacy Action result contract: only string-valued
		// navigation identities matter; unrelated fields are not validated.
		values := mapData(data)
		return taskCreationNavigation{mapString(values, "taskDirectoryName"), mapString(values, "createdAt")}
	}
}

// applyTaskSnapshotSelection runs after the refreshed snapshot has been
// applied. A create result is only selected when that snapshot contains the
// task, matching the Source flow where reload completes before navigation.
func (s *app) applyTaskSnapshotSelection(action application.Action, result application.ActionResult) bool {
	if action.Kind != "task.create" {
		return false
	}
	created := taskCreationNavigationData(result.Data)
	id := created.TaskDirectoryName
	if id == "" {
		s.lastError = "任务已创建，但返回结果缺少任务目录标识，无法自动选中。"
		return true
	}
	for index, item := range s.taskUnit.state.tasks {
		createdAt := created.CreatedAt
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
