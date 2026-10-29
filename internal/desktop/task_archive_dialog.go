package desktop

import (
	"strings"

	"silverwing-go/internal/application"
)

type taskArchiveConfirmationScope = taskMenuScope

func (s *app) taskArchiveScope(t task) (taskArchiveConfirmationScope, bool) {
	scope, valid := s.taskMenuScopeFor(t)
	if !valid || s.busy || strings.TrimSpace(t.id) == "" || t.archived ||
		(s.page != "tasks" && s.page != "materials") {
		return taskArchiveConfirmationScope{}, false
	}
	return scope, true
}

func (s *app) taskArchiveConfirmationAction(scope taskArchiveConfirmationScope) (application.Action, bool) {
	current, ok := s.taskArchiveScope(s.taskUnit.taskByID(scope.taskID))
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
	s.confirmNative("归档任务", "任务将移至已归档，任务目录和资料会保留。", firstNonEmpty(t.title, t.id), func() {
		if action, current := s.taskArchiveConfirmationAction(scope); current {
			s.dispatchTaskContextAction(action, &scope)
		}
	})
}
