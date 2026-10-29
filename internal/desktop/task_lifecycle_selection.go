package desktop

import (
	"slices"

	"silverwing-go/internal/application"
)

// Keep the snapshot's category and stable task identity ahead of its list
// index. This is also used before replacing the UI state so cleanup still
// belongs to the old task when two tasks occupy the same index.
func (s *app) taskLifecycleSnapshotIndex(tasks []task) int {
	archived := s.page == "archive"
	return s.taskUnit.snapshotSelectionIndex(tasks, archived, s.taskIndexRows(archived), s.taskIndexRowsFor(tasks, archived))
}

func (s *taskController) snapshotSelectionIndex(tasks []task, archived bool, previousRows, nextRows []int) int {
	previous := s.currentTask()
	indices := make(map[string]int, len(tasks))
	for index, item := range tasks {
		if item.archived == archived {
			indices[taskNavigationKey(item)] = index
		}
	}
	if previous.id != "" {
		if index, exists := indices[taskNavigationKey(previous)]; exists {
			return index
		}
		rows := previousRows
		position := slices.Index(rows, s.state.selected)
		for _, index := range rows[position+1:] {
			if next, exists := indices[taskNavigationKey(s.state.tasks[index])]; exists {
				return next
			}
		}
		for position--; position >= 0; position-- {
			if next, exists := indices[taskNavigationKey(s.state.tasks[rows[position]])]; exists {
				return next
			}
		}
	}
	if rows := nextRows; len(rows) > 0 {
		return rows[0]
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
	transition := newTaskTransition(previous, next, taskTransitionSnapshot, false)
	if previous.id == "" || !transition.changed() {
		return false
	}
	_ = s.prepareFeatureTaskSelection(transition)
	s.materialsUnit.resetTaskPresentation(transition, s)
	return true
}

// Call only after the new snapshot's task/configuration identities are in place.
func (s *app) finishTaskLifecycleSelection() {
	s.afterReadingTaskSelection()
	s.refreshWorkspaceTaskSelection(false)
}

// Restoration refreshes the current category without opening the restored task.
func (s *app) applyTaskLifecycleSnapshotSelection(action application.Action, _ application.ActionResult) bool {
	if action.Kind != "task.restore" {
		return false
	}
	if s.taskUnit.restoredTaskPresent(action.TaskID) {
		return true
	}
	s.lastError = "任务已恢复，但刷新后的任务列表中未找到已恢复的任务；请刷新后重试。"
	return true
}
