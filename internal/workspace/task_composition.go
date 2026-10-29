package workspace

import (
	"context"
	"time"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/requirementprovider"
	"silverwing-go/internal/storage"
)

// TaskManifestRepository is the persistence boundary used by task orchestration.
// Leaf workspace services keep their existing concrete contracts.
type TaskManifestRepository interface {
	TaskManifestReader
	Save(string, domain.TaskManifest) error
	List(string) ([]storage.ManifestEntry, error)
}

// TaskApplicationDependencies mirrors the facade's explicit collaborators. The
// injected constructor does not create providers, stores, or subprocess runners.
type TaskApplicationDependencies TaskApplicationService

func NewTaskApplicationServiceWithDependencies(dependencies TaskApplicationDependencies) *TaskApplicationService {
	service := TaskApplicationService(dependencies)
	return &service
}

// NewTaskApplicationService preserves the legacy constructor. Production entry
// points call ComposeTaskApplicationDependencies with an explicit preparation
// service; only this compatibility constructor selects the default source.
func NewTaskApplicationService(manifests *storage.ManifestStore, git *GitClient, paths storage.Paths) *TaskApplicationService {
	if git == nil {
		git = NewGitClient(nil, "")
	}
	preparation := documents.NewPreparationService(paths, requirementprovider.NewMeegle(git.Runner))
	return NewTaskApplicationServiceWithDependencies(ComposeTaskApplicationDependencies(manifests, git, paths, preparation))
}

// ComposeTaskApplicationDependencies is the concrete default-service boundary.
// The requirement source is supplied by the application composition root.
func ComposeTaskApplicationDependencies(manifests *storage.ManifestStore, git *GitClient, paths storage.Paths, preparation *documents.PreparationService) TaskApplicationDependencies {
	if git == nil {
		git = NewGitClient(nil, "")
	}
	if manifests == nil {
		manifests = storage.NewManifestStore()
	}
	locks := NewOperationLocks(paths.Locks)
	bootstrap := NewBootstrapService(git.Runner, git)
	dependencies := TaskApplicationDependencies{
		Manifests: manifests, Git: git, Paths: paths, Locks: locks,
		Provisioning:         NewWorkspaceProvisioningService(git, bootstrap, locks),
		Bootstrap:            bootstrap,
		BranchReuse:          NewWorkspaceBranchReuseInspector(git, locks),
		BranchValidator:      NewBranchReferenceValidator(git),
		Documents:            NewAgentDocumentService(paths, manifests),
		Lifecycle:            NewWorkspaceLifecycle(manifests, git, locks),
		MaterialsPreparation: preparation,
		Now:                  time.Now,
	}
	dependencies.Repair = NewWorkspaceRepairService(manifests, dependencies.Documents, locks, git, bootstrap)
	dependencies.ModuleRemoval = NewWorkspaceModuleRemovalService(manifests, dependencies.Documents, locks, git)
	dependencies.RootMigration = NewTaskRootMigrationService(paths, nil, manifests, dependencies.Documents, git, locks)
	dependencies.Defaults = NewTaskCreationDefaultsService(storage.NewConfigStore(paths))
	dependencies.Commands = NewWorkspaceCommandService(git.Runner)
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
