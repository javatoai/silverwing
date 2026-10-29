package desktop

import "silverwing-go/internal/domain"

type taskModuleEditorState struct {
	taskModuleDraftValues
	repositoryPicker taskRepositoryPickerState
}

// Keep existing creation, append and repository-picker consumers on one editor
// type while the shared value rules remain independent of picker lifecycle.
type taskCreationModuleDraft = taskModuleEditorState

func newTaskCreationModuleDraft(service domain.GroupServiceConfig, module domain.ServiceModuleConfig, feature string) *taskCreationModuleDraft {
	return &taskModuleEditorState{taskModuleDraftValues: newTaskModuleDraftValues(service, module, feature)}
}
