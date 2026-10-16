package desktop

import "silverwing-go/internal/application"

// Keep the snapshot's category and stable task identity ahead of its list
// index. This is also used before replacing the UI state so cleanup still
// belongs to the old task when two tasks occupy the same index.
func taskLifecycleSelectedIndex(tasks []task, selected int, selectedID string, archived bool) int {
	if len(tasks) == 0 {
		return -1
	}
	if selectedID != "" {
		selected = -1
		for index, item := range tasks {
			if item.id == selectedID {
				selected = index
				break
			}
		}
	}
	if selected < 0 || selected >= len(tasks) {
		selected = 0
	}
	if tasks[selected].archived == archived {
		return selected
	}
	for index, item := range tasks {
		if item.archived == archived {
			return index
		}
	}
	return -1
}

func taskLifecycleSelectionAt(tasks []task, selected int) task {
	if selected < 0 || selected >= len(tasks) {
		return task{}
	}
	return tasks[selected]
}

func sameTaskLifecycleSelection(left, right task) bool {
	return left.id == right.id && left.path == right.path && left.createdAt == right.createdAt &&
		left.materialsPath == right.materialsPath && left.archived == right.archived
}

// Call before changing tasks, selected or snapshot. Flush source into the old
// autosave entry and record its reading state while its identity is current.
// Clearing presentation state does not remove its pending autosave draft.
func (s *app) prepareTaskLifecycleSelection(previous, next task) bool {
	if previous.id == "" || sameTaskLifecycleSelection(previous, next) {
		return false
	}
	s.syncMarkdownEditor()
	s.beforeReadingTaskSelection()
	s.deactivateInstructionEditor()
	s.dismissWorkspaceHistory()
	s.clearWorkspaceCommandSelection()
	s.clearWorkspaceMergeSelection()
	s.cancelWorkspaceGitStatus()
	s.cancelWorkspaceGitFlow()
	s.cancelMaterialsSearch()
	s.closeTaskDeleteDialog()
	s.resetReadingMaterialsForTaskRestore()
	return true
}

// Call only after the new snapshot's task/configuration identities are in place.
func (s *app) finishTaskLifecycleSelection() {
	s.afterReadingTaskSelection()
	s.requestWorkspaceGitStatus(false)
}

// Restore is selected only from the refreshed task list. Before that snapshot
// arrives, keep the currently visible task unchanged rather than relabeling
// another task's editor and request state with the restored task's identity.
func (s *app) applyTaskLifecycleSnapshotSelection(action application.Action, _ application.ActionResult) bool {
	if action.Kind != "task.restore" {
		return false
	}
	for index, item := range s.tasks {
		if item.id != action.TaskID || item.archived {
			continue
		}
		s.syncMarkdownEditor()
		s.dismissWorkspaceHistory()
		s.page = "tasks"
		s.selectTaskIndex(index)
		return true
	}
	s.lastError = "任务已恢复，但刷新后的任务列表中未找到已恢复的任务；请刷新后重试。"
	return true
}
