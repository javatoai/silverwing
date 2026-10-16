package desktop

import (
	"strings"

	"silverwing-go/internal/application"
)

type taskArchiveConfirmationScope struct {
	taskID, taskPath, taskCreatedAt string
	home, taskRoot, page            string
}

func (s *app) taskArchiveScope(t task) (taskArchiveConfirmationScope, bool) {
	current := s.currentTask()
	if s.busy || strings.TrimSpace(t.id) == "" || t.archived ||
		(s.page != "tasks" && s.page != "materials") || current.id != t.id ||
		current.path != t.path || current.createdAt != t.createdAt || current.archived {
		return taskArchiveConfirmationScope{}, false
	}
	return taskArchiveConfirmationScope{
		taskID: t.id, taskPath: t.path, taskCreatedAt: t.createdAt,
		home: s.snapshot.Home, taskRoot: s.configString("taskRoot"), page: s.page,
	}, true
}

func (s *app) taskArchiveConfirmationAction(scope taskArchiveConfirmationScope) (application.Action, bool) {
	current, ok := s.taskArchiveScope(s.currentTask())
	if !ok || current != scope {
		return application.Action{}, false
	}
	return application.Action{Kind: "task.archive", TaskID: scope.taskID, Path: scope.taskPath,
		Payload: map[string]any{"cleanup": false}}, true
}

func (s *app) openTaskArchiveDialog(t task) {
	scope, ok := s.taskArchiveScope(t)
	if !ok {
		return
	}
	s.confirmNative("归档任务", "任务将移至已归档，工作区和代码不会被删除。", firstNonEmpty(t.title, t.id), func() {
		if action, current := s.taskArchiveConfirmationAction(scope); current {
			s.dispatch(action)
		}
	})
}
