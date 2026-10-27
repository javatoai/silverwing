package desktop

import "silverwing-go/internal/application"

func (s *app) openTaskDeleteDialog(t task) {
	scope, ok := s.taskMenuScopeFor(t)
	if !ok || s.busy {
		return
	}
	s.confirmNative("删除任务", "将删除该任务目录中的说明和本地文件，已关联的任务资料会保留。", t.title, func() {
		if s.taskMenuScopeCurrent(scope) {
			s.dispatchTaskContextAction(application.Action{Kind: "task.delete", TaskID: t.id, Path: t.path}, &scope)
		}
	})
}
