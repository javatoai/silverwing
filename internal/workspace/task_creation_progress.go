package workspace

import "silverwing-go/internal/domain"

type TaskCreationProgressPhase string

const (
	TaskCreationPhaseChecking              TaskCreationProgressPhase = "checking"
	TaskCreationPhasePreparingMaterials    TaskCreationProgressPhase = "preparing-materials"
	TaskCreationPhaseRefreshingRemote      TaskCreationProgressPhase = "refreshing-remote"
	TaskCreationPhasePreparingWorktree     TaskCreationProgressPhase = "preparing-worktree"
	TaskCreationPhaseCloningRepository     TaskCreationProgressPhase = "cloning-repository"
	TaskCreationPhaseInitializingWorkspace TaskCreationProgressPhase = "initializing-workspace"
	TaskCreationPhaseSavingManifest        TaskCreationProgressPhase = "saving-manifest"
	TaskCreationPhaseWritingDocuments      TaskCreationProgressPhase = "writing-documents"
	TaskCreationPhaseLaunchingTools        TaskCreationProgressPhase = "launching-tools"
	TaskCreationPhaseRollback              TaskCreationProgressPhase = "rollback"
	TaskCreationPhaseCompleted             TaskCreationProgressPhase = "completed"
)

type TaskCreationProgressStatus string

const (
	TaskCreationProgressStarted   TaskCreationProgressStatus = "started"
	TaskCreationProgressCompleted TaskCreationProgressStatus = "completed"
	TaskCreationProgressFailed    TaskCreationProgressStatus = "failed"
)

// TaskCreationProgress describes an observable milestone in the existing
// synchronous task-creation workflow. It is deliberately not persisted in the
// task manifest.
type TaskCreationProgress struct {
	Sequence    uint64                     `json:"sequence"`
	Phase       TaskCreationProgressPhase  `json:"phase"`
	Status      TaskCreationProgressStatus `json:"status"`
	ServiceID   string                     `json:"serviceId,omitempty"`
	ServiceName string                     `json:"serviceName,omitempty"`
	ModuleID    string                     `json:"moduleId,omitempty"`
	ModuleName  string                     `json:"moduleName,omitempty"`
	Message     string                     `json:"message,omitempty"`
}

type taskCreationProgressFunc func(TaskCreationProgress)

func taskCreationProgressForModule(service domain.GroupServiceConfig, module domain.ServiceModuleConfig, phase TaskCreationProgressPhase) TaskCreationProgress {
	return TaskCreationProgress{
		Phase: phase, ServiceID: service.ID, ServiceName: service.DisplayName,
		ModuleID: module.ID, ModuleName: moduleEffectiveName(module),
	}
}

func taskCreationProgressForWorkspace(item domain.ServiceWorkspace, phase TaskCreationProgressPhase) TaskCreationProgress {
	return TaskCreationProgress{
		Phase: phase, ServiceID: item.GroupServiceID, ServiceName: item.ServiceName,
		ModuleID: item.ModuleID, ModuleName: item.ModuleName,
	}
}

func emitTaskCreationProgress(callback taskCreationProgressFunc, event TaskCreationProgress) {
	if callback != nil {
		callback(event)
	}
}

func runTaskCreationProgress(callback taskCreationProgressFunc, event TaskCreationProgress, startMessage, completedMessage string, operation func() error) error {
	if callback != nil {
		started := event
		started.Status = TaskCreationProgressStarted
		started.Message = startMessage
		emitTaskCreationProgress(callback, started)
	}
	if err := operation(); err != nil {
		if callback != nil {
			failed := event
			failed.Status = TaskCreationProgressFailed
			failed.Message = err.Error()
			emitTaskCreationProgress(callback, failed)
		}
		return err
	}
	if callback != nil {
		completed := event
		completed.Status = TaskCreationProgressCompleted
		completed.Message = completedMessage
		emitTaskCreationProgress(callback, completed)
	}
	return nil
}
