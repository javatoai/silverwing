package workspace

import (
	"time"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/storage"
)

// TaskRootMigrationDependencies keeps the concrete defaults outside migration
// planning and allows callers to supply the existing collaborators explicitly.
type TaskRootMigrationDependencies TaskRootMigrationService

func NewTaskRootMigrationServiceWithDependencies(dependencies TaskRootMigrationDependencies) *TaskRootMigrationService {
	service := TaskRootMigrationService(dependencies)
	return &service
}

func NewTaskRootMigrationService(paths storage.Paths, configStore *storage.ConfigStore, manifests *storage.ManifestStore, docs *AgentDocumentService, git *GitClient, locks *OperationLocks) *TaskRootMigrationService {
	if manifests == nil {
		manifests = storage.NewManifestStore()
	}
	if configStore == nil {
		configStore = storage.NewConfigStore(paths)
	}

	if docs == nil {
		docs = NewAgentDocumentService(paths, manifests)
	}
	if locks == nil {
		locks = NewOperationLocks(paths.Locks)
	}
	return NewTaskRootMigrationServiceWithDependencies(TaskRootMigrationDependencies{Paths: paths, ConfigStore: configStore, Manifests: manifests, Documents: docs, Drafts: documents.NewService(paths.Cache), Locks: locks, Now: time.Now})
}
