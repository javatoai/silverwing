package workspace

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
)

// PreviewAgentInstructions builds the task manifest shape used by the agent
// document renderer without creating a task directory or touching a repository.
func (s *TaskApplicationService) PreviewAgentInstructions(
	ctx context.Context,
	config domain.AppConfig,
	request CreateTaskRequest,
	materials domain.RequirementMaterialsDirectory,
) (AgentDocumentPreview, error) {
	if s == nil {
		return AgentDocumentPreview{}, fmt.Errorf("任务服务不可用")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return AgentDocumentPreview{}, err
	}

	folderName := request.FolderName
	if strings.TrimSpace(folderName) == "" {
		folderName = "任务名称"
	}
	directoryName, err := RequireValidTaskDirectoryName(folderName)
	if err != nil {
		directoryName = "任务名称"
	}
	featureBranch := strings.TrimSpace(request.FeatureBranch)
	if featureBranch == "" && (len(request.ServiceIDs) > 0 || len(request.ServiceSelections) > 0) {
		featureBranch = "feature/example"
	}

	group, err := config.Group(request.GroupID)
	if err != nil {
		return AgentDocumentPreview{}, err
	}
	if err := ctx.Err(); err != nil {
		return AgentDocumentPreview{}, err
	}

	selectedServices := make(map[string]bool, len(request.ServiceIDs))
	for _, id := range request.ServiceIDs {
		selectedServices[id] = true
	}
	selectionsByService := make(map[string]TaskServiceSelection, len(request.ServiceSelections))
	for _, selection := range request.ServiceSelections {
		// Kotlin's associateBy keeps the last selection for a duplicate key.
		selectionsByService[selection.ServiceID] = selection
		selectedServices[selection.ServiceID] = true
	}
	repositories := make(map[string]domain.RepositoryConfig, len(config.Repositories))
	for _, repository := range config.Repositories {
		repositories[repository.ID] = repository
	}

	root := s.Paths.Temp
	if config.TaskRoot != nil && strings.TrimSpace(*config.TaskRoot) != "" {
		root = *config.TaskRoot
	}
	taskDirectory := filepath.Join(root, directoryName)
	if strings.TrimSpace(request.RequirementLink) == "" {
		scope := documents.LocalTaskMaterials(taskDirectory)
		materials = domain.RequirementMaterialsDirectory{Status: domain.MaterialsReady, WriteRoot: &scope.Owned}
	}
	workspaces := make([]domain.ServiceWorkspace, 0)
	for _, service := range group.Services {
		if err := ctx.Err(); err != nil {
			return AgentDocumentPreview{}, err
		}
		if !selectedServices[service.ID] {
			continue
		}
		repository, found := repositories[service.RepositoryID]
		if !found {
			// The source preview omits selected services whose repository config
			// has been removed instead of blocking the preview.
			continue
		}

		selectedModules, hasSelection := selectionsByService[service.ID]
		modules := append([]TaskModuleSelection(nil), selectedModules.Modules...)
		if !hasSelection {
			modules = make([]TaskModuleSelection, 0, len(service.Modules))
			for _, configured := range service.Modules {
				target := featureBranch
				if len(service.Modules) != 1 {
					target += "-" + configured.Name
				}
				modules = append(modules, configuredModuleSelection(service, configured, service.EffectiveMasterBranch(configured), target))
			}
		}

		for _, selected := range modules {
			if err := ctx.Err(); err != nil {
				return AgentDocumentPreview{}, err
			}
			source := selected.Source
			if source == "" {
				source = domain.TaskModuleConfigured
			}
			baseRef := selected.BaseRef
			module := domain.ServiceModuleConfig{
				ID: selected.ID, Name: selected.Name, MasterBranch: previewStringPointer(baseRef),
				Strategy: selected.Strategy, TagEnabled: selected.TagEnabled && source == domain.TaskModuleConfigured,
				TagMode: selected.TagMode, TagTargetRef: previewNullableStringPointer(selected.TagTargetRef),
				TagMessagePrefix: selected.TagMessagePrefix, CustomCommands: []domain.WorkspaceCommandConfig{},
			}
			if module.TagMode == "" {
				module.TagMode = domain.TagBuildMergeToTargetBranch
			}
			if module.TagMessagePrefix == "" {
				module.TagMessagePrefix = "Tag"
			}

			targetBranch := strings.TrimSpace(selected.TargetBranch)
			if targetBranch == "" && module.Strategy == domain.WorkspaceStandardWorktree {
				targetBranch = featureBranch
				if len(modules) != 1 {
					targetBranch += "-" + module.Name
				}
			}
			branch := targetBranch
			if module.Strategy == domain.WorkspaceIndependentClone && targetBranch == "" {
				base, parseErr := domain.ParseRemoteBranchRef(baseRef)
				if parseErr != nil {
					return AgentDocumentPreview{}, parseErr
				}
				branch = base.Branch
			}
			directory, err := moduleDirectoryName(service, module)
			if err != nil {
				return AgentDocumentPreview{}, err
			}
			workspaces = append(workspaces, domain.ServiceWorkspace{
				RepositoryID: repository.ID, ServiceName: service.DisplayName,
				RepositoryPath: repository.RootPath, WorktreePath: filepath.Join(taskDirectory, directory),
				DevelopmentTool: service.DevelopmentTool, Branch: branch,
				Health: domain.WorkspaceCreating, Warnings: []string{}, GroupServiceID: service.ID,
				ModuleID: module.ID, ModuleName: domain.ModuleDisplayName(module.Name, service.DisplayName, baseRef, len(service.Modules)),
				Strategy: module.Strategy, ModuleSource: source, OriginURL: repository.OriginURL,
				BaseRef: previewStringPointer(baseRef), TargetBranch: previewNullableStringPointer(targetBranch),
				TagEnabled: module.TagEnabled, TagMode: module.TagMode,
				TagTargetRef: service.EffectiveTagTargetRef(module), TagMessagePrefix: module.TagMessagePrefix,
				PushRemote: "origin",
			})
		}
	}

	if err := ctx.Err(); err != nil {
		return AgentDocumentPreview{}, err
	}
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	stamp := now().UTC().Format(time.RFC3339Nano)
	manifest := domain.DefaultTaskManifest()
	manifest.FolderName = folderName
	manifest.TaskDirectoryName = directoryName
	manifest.FeatureBranch = featureBranch
	manifest.RequirementLink = strings.TrimSpace(request.RequirementLink)
	manifest.RequirementMaterials = materials
	manifest.CreatedAt = stamp
	manifest.UpdatedAt = stamp
	manifest.Services = workspaces
	manifest.GroupID = request.GroupID
	manifest.AgentDocumentLayout = domain.AgentDocumentReferencedV2

	documents := s.Documents
	if documents == nil {
		documents = NewAgentDocumentService(s.Paths, s.Manifests)
	}
	if err := ctx.Err(); err != nil {
		return AgentDocumentPreview{}, err
	}
	preview, err := documents.RenderPreviewChecked(taskDirectory, manifest, config, request.TaskNotes)
	if err != nil {
		return AgentDocumentPreview{}, err
	}
	if err := ctx.Err(); err != nil {
		return AgentDocumentPreview{}, err
	}
	return preview, nil
}

func previewStringPointer(value string) *string {
	return &value
}

func previewNullableStringPointer(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}
