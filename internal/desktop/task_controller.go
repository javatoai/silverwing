package desktop

import (
	"context"
	"silverwing-go/internal/application"
	"silverwing-go/internal/workspace"
)

// taskController owns this feature's state. Its zero value is usable in headless tests.
type taskController struct{ state taskState }

type taskState struct {
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
	taskOpenTools          *taskOpenToolsState
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
	taskBranchInfo         *taskBranchInfoState
	taskDeleteRiskJobs     *taskDeleteRiskJobState
	taskGroupID            string
	taskServices           map[string]bool
	taskModules            map[string]map[string]bool
	taskCreation           *taskCreationState
	taskCreationPreflights *taskCreationPreflightJobState
	taskDeleteDialog       *taskDeleteDialogState
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

type taskSelectionDialogView interface{ closeTaskDeleteDialog() }

func (s *taskController) leaveTaskSelection(transition TaskTransition, view taskSelectionDialogView) {
	if transition.origin == taskTransitionSnapshot {
		view.closeTaskDeleteDialog()
	}
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
	case "task.create-preview":
		if port, ok := backend.(application.TaskCreationPreviewer); ok {
			request, err := application.TaskCreateRequestFromAction(action)
			if err != nil {
				return application.ActionResult{}, err
			}
			return legacyOperation(port.PreviewTaskCreation(ctx, request, progress))
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
	case "task.requirement-link":
		if port, ok := backend.(application.TaskRequirementLinker); ok {
			return legacyOperation(port.LinkTaskRequirement(ctx, application.TaskRequirementLinkRequestFromAction(action)))
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
	// Publish on the UI thread after any list/search/identity field changes,
	// including a producer reusing the same slice. Nested workspace/tool status
	// updates are not list projection inputs and keep their existing owners.
	s.state.tasks, s.state.selected = tasks, selected
	s.invalidateTaskIndex()
}

// Session restore changes selection without recording a new user interaction.
func (s *taskController) restoreSelection(index int) {
	s.state.selected = index
}
