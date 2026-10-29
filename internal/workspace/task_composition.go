package workspace

import (
	"context"
	"time"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/requirementprovider"
	"silverwing-go/internal/storage"
)

// TaskManifestRepository is the persistence boundary used by task orchestration.
type TaskManifestRepository interface {
	TaskManifestReader
	Save(string, domain.TaskManifest) error
	List(string) ([]storage.ManifestEntry, error)
}

// The explicit constructor does not select providers or create leaf services.
type TaskApplicationDependencies TaskApplicationService

func NewTaskApplicationServiceWithDependencies(dependencies TaskApplicationDependencies) *TaskApplicationService {
	service := TaskApplicationService(dependencies)
	return &service
}

// NewTaskApplicationService preserves the source-compatible default constructor.
// Production application composition explicitly supplies materials preparation.
func NewTaskApplicationService(manifests *storage.ManifestStore, git *GitClient, paths storage.Paths) *TaskApplicationService {
	preparation := documents.NewPreparationService(paths, requirementprovider.NewMeegle(process.NewRunner()))
	return NewTaskApplicationServiceWithDependencies(ComposeTaskApplicationDependencies(manifests, git, paths, preparation))
}

// These editions compose task/document services only. No Git workspace services
// are added by dependency injection or materials protocol isolation.
func ComposeTaskApplicationDependencies(manifests *storage.ManifestStore, _ *GitClient, paths storage.Paths, preparation *documents.PreparationService) TaskApplicationDependencies {
	if manifests == nil {
		manifests = storage.NewManifestStore()
	}
	locks := NewOperationLocks(paths.Locks)
	docs := NewAgentDocumentService(paths, manifests)
	dependencies := TaskApplicationDependencies{Manifests: manifests, Paths: paths, Locks: locks, Documents: docs, Now: time.Now, MaterialsPreparation: preparation}
	dependencies.RootMigration = NewTaskRootMigrationService(paths, nil, manifests, docs, nil, locks)
	dependencies.Defaults = NewTaskCreationDefaultsService(storage.NewConfigStore(paths))
	dependencies.WorkspaceTools = NewWorkspaceToolLaunchService(nil, manifests, locks)
	dependencies.RequirementMaterials = RequirementMaterialsFromPreparation(preparation)
	return dependencies
}

func RequirementMaterialsFromPreparation(preparation *documents.PreparationService) RequirementMaterialsResolver {
	if preparation == nil {
		return nil
	}
	return func(ctx context.Context, config domain.AppConfig, input, folder string) (domain.RequirementMaterialsDirectory, *string, error) {
		return resolveRequirementMaterials(ctx, config, input, folder, preparation)
	}
}
