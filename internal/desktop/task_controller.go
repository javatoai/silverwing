package desktop

import (
	"context"
	"silverwing-go/internal/application"
	"silverwing-go/internal/workspace"
	"slices"
)

// taskController owns this feature's state. Its zero value is usable in headless tests.
type taskController struct{ state taskState }

type taskState struct {
	taskGroups             []application.Group
	groupsRevision         uint64
	tasksRevision          uint64
	taskCatalog            *taskIndexCatalog
	taskSearch             taskIndexSearchProjection
	metadataRevision       uint64
	metadataValues         map[string]taskIndexMetadataValue
	metadataDiscovery      taskMetadataDiscoveryKey
	metadataDiscoveryValid bool
	metadataDiscoveryRetry map[string]task
	taskNavigation         *taskNavigationState
	taskContextScope       *taskMenuScope
	taskContextUpdate      func(func())
	taskIndexes            map[bool]*taskIndexBrowsingState
	taskListFocused        bool
	selected               int
	tasks                  []task
	notes                  [3]string
	notePaths              [3]string
	noteHashes             [3]string
	noteTab                int
	noteEditing            bool
	taskRootTargetFocused  bool
	taskGroupID            string
	taskCreation           *taskCreationState
	taskNotesUI            *taskNotesUIState
	taskCreationWarning    string
}

func (s *taskController) stopFeature() {
	s.closeTaskCreationRequirements()
	s.closeTaskCreationAINaming()
}

func (s *taskController) selectIndex(index int) {
	s.state.selected = index
	s.state.taskListFocused = false
}

func (s *taskController) restoredTaskPresent(taskID string) bool {
	for _, item := range s.state.tasks {
		if item.id == taskID && !item.archived {
			return true
		}
	}
	return false
}

// The desktop compatibility boundary selects narrow application capabilities.
// Real application backends use typed operations; small older test backends may
// still implement Dispatch. Identity guards are copied by the request adapters.
func executeTaskOperation(ctx context.Context, backend application.Backend, action application.Action, progress func(workspace.TaskCreationProgress)) (application.ActionResult, error) {
	switch action.Kind {
	case "task.create":
		if port, ok := backend.(application.TaskCreator); ok {
			request, err := application.TaskCreateRequestFromAction(action)
			if err != nil {
				return application.ActionResult{}, err
			}
			return legacyOperation(port.CreateTask(ctx, request, progress))
		}

	case "task.archive":
		if port, ok := backend.(application.TaskLifecycle); ok {
			return legacyOperation(port.ArchiveTask(ctx, application.TaskArchiveRequestFromAction(action)))
		}
	case "task.restore":
		if port, ok := backend.(application.TaskLifecycle); ok {
			return legacyOperation(port.RestoreTask(ctx, application.TaskTargetFromAction(action)))
		}
	case "task.delete":
		if port, ok := backend.(application.TaskLifecycle); ok {
			return legacyOperation(port.DeleteTask(ctx, application.TaskDeleteRequestFromAction(action)))
		}
	case "task.materials-retry":
		if port, ok := backend.(application.TaskMaterials); ok {
			return legacyOperation(port.RetryTaskMaterials(ctx, application.TaskTargetFromAction(action)))
		}

	case "task.tools-launch":
		if port, ok := backend.(application.TaskTools); ok {
			request, err := application.TaskToolsRequestFromAction(action)
			if err != nil {
				return application.ActionResult{}, err
			}
			return legacyOperation(port.LaunchTaskTools(ctx, request))
		}
	case "task.tools-retry":
		if port, ok := backend.(application.TaskTools); ok {
			return legacyOperation(port.RetryTaskTool(ctx, application.TaskToolRetryRequestFromAction(action)))
		}
	}
	if port, ok := backend.(application.TaskCreationProgressBackend); ok && (action.Kind == "task.create" || action.Kind == "task.create-preview") {
		return port.DispatchTaskCreation(ctx, action, progress)
	}
	return backend.Dispatch(ctx, action)
}

func (s *taskController) currentTask() task {
	if s.state.selected < 0 || s.state.selected >= len(s.state.tasks) {
		return task{}
	}
	return s.state.tasks[s.state.selected]
}

func (s *taskController) activeTasks(archived bool) []int {
	category := 0
	if archived {
		category = 1
	}
	return s.taskCatalog().categories[category]
}

func (s *taskController) replaceTasks(tasks []task, selected int) {
	// Publish after list/search/identity changes, including same-slice updates.
	// Tool launch status does not affect these presentation inputs.
	s.state.tasks, s.state.selected = tasks, selected
	s.invalidateTaskIndex()
}

// Session restore changes selection without recording a new user interaction.
func (s *taskController) restoreSelection(index int) {
	s.state.selected = index
}

// Group labels belong to task search, independently of developer service data.
// Keep detached values so later mutation and republication invalidates search.
func (s *taskController) publishTaskGroups(groups []application.Group) {
	if slices.Equal(s.state.taskGroups, groups) {
		return
	}
	s.state.taskGroups = slices.Clone(groups)
	s.state.groupsRevision++
}
