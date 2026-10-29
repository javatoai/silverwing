package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/taskscope"
)

type CreateTaskRequest struct {
	FolderName               string                   `json:"folderName"`
	FeatureBranch            string                   `json:"featureBranch"`
	GroupID                  string                   `json:"groupId"`
	ServiceIDs               []string                 `json:"serviceIds"`
	RequirementLink          string                   `json:"requirementLink"`
	TaskNotes                string                   `json:"taskNotes"`
	ConfirmedBranchReuseKeys []BranchReuseKey         `json:"confirmedBranchReuseKeys"`
	BaseOverrides            []ModuleBaseOverride     `json:"baseOverrides"`
	ServiceSelections        []TaskServiceSelection   `json:"serviceSelections"`
	AgentContext             *domain.AgentTaskContext `json:"agentContext"`
	AgentHandoffMarkdown     *string                  `json:"agentHandoffMarkdown"`
}

type AddWorkspaceRequest struct {
	ServiceID                string                   `json:"serviceId"`
	ModuleID                 string                   `json:"moduleId"`
	ModuleName               string                   `json:"moduleName"`
	Strategy                 domain.WorkspaceStrategy `json:"strategy"`
	BaseRef                  string                   `json:"baseRef"`
	BaseRemote               string                   `json:"baseRemote"`
	Branch                   string                   `json:"branch"`
	TargetBranch             string                   `json:"targetBranch"`
	Source                   domain.TaskModuleSource  `json:"source"`
	TagEnabled               bool                     `json:"tagEnabled"`
	TagMode                  domain.TagBuildMode      `json:"tagMode"`
	TagTargetRef             string                   `json:"tagTargetRef"`
	TagMessagePrefix         string                   `json:"tagMessagePrefix"`
	ServiceIDs               []string                 `json:"serviceIds"`
	ConfirmedBranchReuseKeys []BranchReuseKey         `json:"confirmedBranchReuseKeys"`
	BaseOverrides            []ModuleBaseOverride     `json:"baseOverrides"`
	ServiceSelections        []TaskServiceSelection   `json:"serviceSelections"`
	Modules                  []TaskModuleSelection    `json:"modules"`
}

// RequirementMaterialsResolver is injected by the application layer so task
// creation and other task workflows share the same materials preparation
// boundary as document and delivery flows.
type RequirementMaterialsResolver func(ctx context.Context, config domain.AppConfig, requirementInput, folderName string) (domain.RequirementMaterialsDirectory, *string, error)

type TaskApplicationService struct {
	Manifests            TaskManifestRepository
	Git                  *GitClient
	Paths                storage.Paths
	Locks                *OperationLocks
	Provisioning         *WorkspaceProvisioningService
	Bootstrap            *BootstrapService
	BranchReuse          *WorkspaceBranchReuseInspector
	BranchValidator      BranchReferenceValidator
	Documents            *AgentDocumentService
	Lifecycle            *WorkspaceLifecycle
	Repair               *WorkspaceRepairService
	ModuleRemoval        *WorkspaceModuleRemovalService
	RootMigration        *TaskRootMigrationService
	Defaults             *TaskCreationDefaultsService
	Commands             *WorkspaceCommandService
	WorkspaceTools       *WorkspaceToolLaunchService
	MaterialsPreparation *documents.PreparationService
	RequirementMaterials RequirementMaterialsResolver
	Now                  func() time.Time
	OnOutput             func(process.StreamEvent)
}

func (s *TaskApplicationService) ListTasks(taskRoot string) ([]storage.ManifestEntry, error) {
	if strings.TrimSpace(taskRoot) == "" {
		return nil, fmt.Errorf("任务根目录未配置")
	}
	return s.Manifests.List(taskRoot)
}

func (s *TaskApplicationService) InspectCreateBranchReuse(ctx context.Context, config domain.AppConfig, request CreateTaskRequest) ([]BranchReuseConflict, error) {
	return s.InspectCreateBranchReuseWithProgress(ctx, config, request, nil)
}

// InspectCreateBranchReuseWithProgress performs the same read-only preflight
// while reporting its real validation and per-module inspection boundaries.
func (s *TaskApplicationService) InspectCreateBranchReuseWithProgress(ctx context.Context, config domain.AppConfig, request CreateTaskRequest, onProgress func(TaskCreationProgress)) ([]BranchReuseConflict, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	checking := TaskCreationProgress{Phase: TaskCreationPhaseChecking, ServiceName: "任务"}
	var conflicts []BranchReuseConflict
	err := runTaskCreationProgress(onProgress, checking, "正在检查任务信息与远程分支", "任务信息与远程分支检查完成", func() error {
		root, err := taskRoot(config)
		if err != nil {
			return err
		}
		folder, err := RequireValidTaskDirectoryName(request.FolderName)
		if err != nil {
			return err
		}
		if err := requireAvailableTaskDirectory(filepath.Join(root, folder)); err != nil {
			return err
		}
		groupID := request.GroupID
		if groupID == "" {
			groupID = domain.DefaultGroupID
		}
		group, err := config.Group(groupID)
		if err != nil {
			return err
		}
		feature := strings.TrimSpace(request.FeatureBranch)
		if len(request.ServiceIDs) > 0 || len(request.ServiceSelections) > 0 {
			if err := s.validateFeatureBranch(feature); err != nil {
				return err
			}
		}
		resolved, err := resolveTaskSelections(group, request.ServiceIDs, request.ServiceSelections, request.BaseOverrides, feature)
		if err != nil {
			return err
		}
		if err := requireUniqueWorkspacePaths(filepath.Join(root, folder), nil, resolved); err != nil {
			return err
		}
		conflicts, err = s.inspectBranchReuseWithProgress(ctx, config, resolved, feature, onProgress)
		return err
	})
	if err != nil {
		return nil, err
	}
	return conflicts, nil
}

func (s *TaskApplicationService) CreateTask(ctx context.Context, config domain.AppConfig, request CreateTaskRequest) (domain.TaskManifest, error) {
	return s.CreateTaskWithProgress(ctx, config, request, nil)
}

// CreateTaskWithProgress runs the same synchronous create workflow while
// reporting real operation boundaries to an optional caller-owned observer.
func (s *TaskApplicationService) CreateTaskWithProgress(ctx context.Context, config domain.AppConfig, request CreateTaskRequest, onProgress func(TaskCreationProgress)) (domain.TaskManifest, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return domain.TaskManifest{}, err
	}
	checking := TaskCreationProgress{Phase: TaskCreationPhaseChecking, ServiceName: "任务", Message: "正在检查任务信息与远程分支"}
	root := ""
	folderName := ""
	feature := ""
	var group domain.GroupConfig
	var selections []effectiveTaskService
	taskPath := ""
	err := runTaskCreationProgress(onProgress, checking, "正在检查任务信息与远程分支", "任务信息与远程分支检查完成", func() error {
		var checkErr error
		root, checkErr = taskRoot(config)
		if checkErr != nil {
			return checkErr
		}
		folderName, checkErr = RequireValidTaskDirectoryName(request.FolderName)
		if checkErr != nil {
			return checkErr
		}
		if checkErr = requireAvailableTaskDirectory(filepath.Join(root, folderName)); checkErr != nil {
			return checkErr
		}
		feature = strings.TrimSpace(request.FeatureBranch)
		if len(request.ServiceIDs) > 0 || len(request.ServiceSelections) > 0 {
			if checkErr = s.validateFeatureBranch(feature); checkErr != nil {
				return checkErr
			}
		}
		if (request.AgentContext == nil) != (request.AgentHandoffMarkdown == nil) {
			return fmt.Errorf("Agent 任务上下文与交接文档必须同时提供")
		}
		groupID := request.GroupID
		if groupID == "" {
			groupID = domain.DefaultGroupID
		}
		group, checkErr = config.Group(groupID)
		if checkErr != nil {
			return checkErr
		}
		selections, checkErr = resolveTaskSelections(group, request.ServiceIDs, request.ServiceSelections, request.BaseOverrides, feature)
		if checkErr != nil {
			return checkErr
		}
		taskPath = filepath.Join(root, folderName)
		if checkErr = requireUniqueWorkspacePaths(taskPath, nil, selections); checkErr != nil {
			return checkErr
		}
		_, checkErr = s.inspectBranchReuseWithProgress(ctx, config, selections, feature, onProgress)
		return checkErr
	})
	if err != nil {
		return domain.TaskManifest{}, err
	}
	var manifest domain.TaskManifest
	err = s.Locks.WithTaskRoot(root, func() error {
		return s.Locks.WithTask(taskPath, func() error {
			var inner error
			manifest, inner = s.createLocked(ctx, config, request, group, selections, folderName, feature, root, taskPath, onProgress)
			return inner
		})
	})
	return manifest, err
}

func (s *TaskApplicationService) createLocked(ctx context.Context, config domain.AppConfig, request CreateTaskRequest, group domain.GroupConfig, selections []effectiveTaskService, folderName, feature, taskRootPath, taskPath string, onProgress func(TaskCreationProgress)) (domain.TaskManifest, error) {
	createdTime := s.now()
	ctx = documents.WithMaterialsCreationTime(ctx, createdTime)
	if err := ctx.Err(); err != nil {
		return domain.TaskManifest{}, err
	}
	checking := TaskCreationProgress{Phase: TaskCreationPhaseChecking, ServiceName: "任务"}
	if err := runTaskCreationProgress(onProgress, checking, "正在检查任务目录", "任务目录检查完成", func() error {
		if err := os.MkdirAll(taskRootPath, 0o755); err != nil {
			return err
		}
		if parent, err := filepath.Abs(filepath.Dir(taskPath)); err != nil || !samePath(parent, mustAbs(taskRootPath)) {
			return fmt.Errorf("任务目录必须是任务根目录的直接子目录")
		}
		if err := requireAvailableTaskDirectory(taskPath); err != nil {
			return err
		}
		return nil
	}); err != nil {
		return domain.TaskManifest{}, err
	}
	materialsDirectory := domain.RequirementMaterialsDirectory{Status: domain.MaterialsNotRequested}
	var requirementID *string
	if strings.TrimSpace(request.RequirementLink) != "" {
		materialsEvent := TaskCreationProgress{Phase: TaskCreationPhasePreparingMaterials, ServiceName: "需求资料"}
		emitTaskCreationProgress(onProgress, TaskCreationProgress{Phase: materialsEvent.Phase, Status: TaskCreationProgressStarted, Message: "正在准备需求资料"})
		var err error
		materialsDirectory, requirementID, err = s.resolveRequirementMaterials(ctx, config, request.RequirementLink, folderName)
		if err != nil {
			failed := materialsEvent
			failed.Status = TaskCreationProgressFailed
			failed.Message = err.Error()
			emitTaskCreationProgress(onProgress, failed)
			return domain.TaskManifest{}, err
		}
		completed := materialsEvent
		completed.Status = TaskCreationProgressCompleted
		completed.Message = "需求资料准备步骤已返回"
		if materialsDirectory.Status == domain.MaterialsFailed && materialsDirectory.FailureReason != nil && strings.TrimSpace(*materialsDirectory.FailureReason) != "" {
			completed.Message = "需求资料未能准备，但任务创建可以继续：" + *materialsDirectory.FailureReason
		}
		emitTaskCreationProgress(onProgress, completed)
	}
	workspaces := []domain.ServiceWorkspace{}
	var ownedTaskDirectory os.FileInfo
	localMaterials := strings.TrimSpace(request.RequirementLink) == ""
	rollback := func(primary error) error {
		rollbackEvent := TaskCreationProgress{Phase: TaskCreationPhaseRollback, ServiceName: "任务"}
		emitTaskCreationProgress(onProgress, TaskCreationProgress{Phase: rollbackEvent.Phase, Status: TaskCreationProgressStarted, Message: "正在回滚本次创建并清理任务目录"})
		finishRollback := func(cleanupErr error) {
			status := TaskCreationProgressCompleted
			message := "回滚流程结束"
			if cleanupErr != nil {
				status = TaskCreationProgressFailed
				message = "回滚流程结束，仍有清理失败，请检查保留的任务目录：" + cleanupErr.Error()
			}
			event := rollbackEvent
			event.Status = status
			event.Message = message
			emitTaskCreationProgress(onProgress, event)
		}
		if cleanupErr := verifyOwnedTaskDirectory(taskPath, ownedTaskDirectory); cleanupErr != nil {
			finishRollback(cleanupErr)
			return fmt.Errorf("任务目录身份已变化，跳过工作区回滚以保护新目录；已保留现场：%s: %w", taskPath, errors.Join(primary, cleanupErr))
		}
		if len(workspaces) > 0 {
			if cleanupErr := s.rollbackWorkspacesWithProgress(ctx, config, group.ID, taskPath, workspaces, onProgress); cleanupErr != nil {
				finishRollback(cleanupErr)
				return fmt.Errorf("任务创建失败且自动回滚未完成；为避免 Git 元数据损坏，已保留目录供人工检查：%s: %w", taskPath, errors.Join(primary, cleanupErr))
			}
		}
		if localMaterials {
			if cleanErr := documents.CleanupLocalTaskMaterials(taskPath); cleanErr != nil {
				finishRollback(cleanErr)
				return errors.Join(primary, cleanErr)
			}
		}
		if cleanErr := removeGeneratedTaskDocuments(taskPath, ownedTaskDirectory); cleanErr != nil {
			finishRollback(cleanErr)
			return fmt.Errorf("任务创建失败，清理生成文档失败，已保留现场：%s: %w", taskPath, errors.Join(primary, cleanErr))
		}
		if removeErr := removeEmptyOwnedTaskDirectory(taskPath, ownedTaskDirectory); removeErr != nil {
			cleanupErr := removeErr
			finishRollback(cleanupErr)
			return fmt.Errorf("任务创建失败且任务目录未能安全清理；已保留现场供人工检查：%s: %w", taskPath, errors.Join(primary, cleanupErr))
		}
		finishRollback(nil)
		return primary
	}
	preparingDirectory := TaskCreationProgress{Phase: TaskCreationPhasePreparingWorktree, ServiceName: "任务"}
	if err := runTaskCreationProgress(onProgress, preparingDirectory, "正在创建任务目录", "任务目录已创建", func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.Mkdir(taskPath, 0o755); err != nil {
			return err
		}
		info, err := os.Lstat(taskPath)
		if err != nil {
			return fmt.Errorf("无法确认新建任务目录身份，已保留现场：%w", err)
		}
		// On Windows FileInfo may lazily resolve its file ID. Pin it before
		// invoking progress callbacks so a later path replacement cannot change
		// the identity represented by this snapshot.
		if !os.SameFile(info, info) {
			return fmt.Errorf("无法确认新建任务目录身份，已保留现场：%s", taskPath)
		}
		reparse, err := bootstrapPathIsReparsePoint(taskPath, info)
		if err != nil {
			return fmt.Errorf("无法确认新建任务目录类型：%w", err)
		}
		if !info.IsDir() || reparse {
			return fmt.Errorf("新建任务目录不是安全的普通目录：%s", taskPath)
		}
		ownedTaskDirectory = info
		return nil
	}); err != nil {
		if ownedTaskDirectory == nil {
			return domain.TaskManifest{}, err
		}
		return domain.TaskManifest{}, rollback(err)
	}
	if err := ctx.Err(); err != nil {
		return domain.TaskManifest{}, rollback(err)
	}
	if err := verifyOwnedTaskDirectory(taskPath, ownedTaskDirectory); err != nil {
		return domain.TaskManifest{}, rollback(err)
	}

	for _, effective := range selections {
		if err := ctx.Err(); err != nil {
			return domain.TaskManifest{}, rollback(err)
		}
		repository, ok := repositoryByID(config, effective.Service.RepositoryID)
		if !ok {
			return domain.TaskManifest{}, rollback(fmt.Errorf("服务 %s 的仓库配置不存在", effective.Service.DisplayName))
		}
		created, err := s.Provisioning.ProvisionWithProgress(ctx, WorkspaceProvisionRequest{
			TaskDirectory: taskPath, Repository: repository, Service: effective.Service,
			RequestedFeatureBranch: feature, ConfirmedBranchReuseKeys: request.ConfirmedBranchReuseKeys,
			ModuleBranches: effective.ModuleBranches, ModuleSources: effective.ModuleSources,
		}, s.OnOutput, onProgress)
		if err != nil {
			return domain.TaskManifest{}, rollback(err)
		}
		workspaces = append(workspaces, created...)
		if err := ctx.Err(); err != nil {
			return domain.TaskManifest{}, rollback(err)
		}
	}
	if err := ctx.Err(); err != nil {
		return domain.TaskManifest{}, rollback(err)
	}
	if localMaterials {
		if err := runTaskCreationProgress(onProgress, TaskCreationProgress{Phase: TaskCreationPhasePreparingMaterials}, "正在准备本地任务资料", "本地任务资料已准备", func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			scope, err := documents.PrepareLocalTaskMaterials(taskPath)
			if err == nil {
				materialsDirectory = domain.RequirementMaterialsDirectory{Status: domain.MaterialsReady, WriteRoot: &scope.Owned}
			}
			return err
		}); err != nil {
			return domain.TaskManifest{}, rollback(err)
		}
	}
	now := createdTime.UTC().Format(time.RFC3339Nano)
	manifest := domain.DefaultTaskManifest()
	manifest.FolderName = folderName
	manifest.TaskDirectoryName = folderName
	manifest.FeatureBranch = feature
	manifest.RequirementLink = strings.TrimSpace(request.RequirementLink)
	manifest.RequirementID = requirementID
	manifest.RequirementMaterials = materialsDirectory
	manifest.CreatedAt = now
	manifest.UpdatedAt = now
	manifest.Services = workspaces
	manifest.GroupID = group.ID
	manifest.AgentContext = request.AgentContext
	manifest.AgentDocumentLayout = domain.AgentDocumentReferencedV2
	manifest = domain.NormalizeTaskManifest(manifest)
	saving := TaskCreationProgress{Phase: TaskCreationPhaseSavingManifest, ServiceName: "任务"}
	if err := runTaskCreationProgress(onProgress, saving, "正在保存任务清单", "任务清单已保存", func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := verifyOwnedTaskDirectory(taskPath, ownedTaskDirectory); err != nil {
			return err
		}
		return s.Manifests.Save(taskPath, manifest)
	}); err != nil {
		return domain.TaskManifest{}, rollback(err)
	}
	if err := ctx.Err(); err != nil {
		return domain.TaskManifest{}, rollback(err)
	}
	if err := verifyOwnedTaskDirectory(taskPath, ownedTaskDirectory); err != nil {
		return domain.TaskManifest{}, rollback(err)
	}
	writing := TaskCreationProgress{Phase: TaskCreationPhaseWritingDocuments, ServiceName: "任务资料"}
	if err := runTaskCreationProgress(onProgress, writing, "正在写入任务资料", "任务资料已写入", func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := verifyOwnedTaskDirectory(taskPath, ownedTaskDirectory); err != nil {
			return err
		}
		if _, err := s.Documents.writeTaskDocuments(taskPath, manifest, repositoryInfos(config), &request.TaskNotes, true); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := verifyOwnedTaskDirectory(taskPath, ownedTaskDirectory); err != nil {
			return err
		}
		if request.AgentHandoffMarkdown != nil {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := verifyOwnedTaskDirectory(taskPath, ownedTaskDirectory); err != nil {
				return err
			}
			if _, err := s.Documents.WriteHandoff(taskPath, *request.AgentHandoffMarkdown); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return domain.TaskManifest{}, rollback(err)
	}
	if err := ctx.Err(); err != nil {
		return domain.TaskManifest{}, rollback(err)
	}
	if err := verifyOwnedTaskDirectory(taskPath, ownedTaskDirectory); err != nil {
		return domain.TaskManifest{}, rollback(err)
	}
	return manifest, nil
}

// RetryRequirementMaterials re-runs only the independent materials-directory
// preparation and persists the new result without touching Git workspaces.
func (s *TaskApplicationService) RetryRequirementMaterials(ctx context.Context, config domain.AppConfig, taskPath string) (domain.TaskManifest, error) {
	var updated domain.TaskManifest
	err := s.withTaskManifestMutation(ctx, config, taskPath, func(ctx context.Context, manifest domain.TaskManifest) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if created, err := domain.ParseSilverWingTime(manifest.CreatedAt); err == nil {
			ctx = documents.WithMaterialsCreationTime(ctx, created)
		}
		_, local := documents.LocalScope(stringValue(manifest.RequirementMaterials.WriteRoot))
		directory, requirementID := manifest.RequirementMaterials, manifest.RequirementID
		var err error
		if local || strings.TrimSpace(manifest.RequirementLink) == "" {
			var scope documents.AnnualMaterialsScope
			scope, err = documents.PrepareLocalTaskMaterials(taskPath)
			directory = domain.RequirementMaterialsDirectory{Status: domain.MaterialsReady, WriteRoot: &scope.Owned}
		} else {
			directory, requirementID, err = s.resolveRequirementMaterials(ctx, config, manifest.RequirementLink, manifest.FolderName)
		}
		if err != nil {
			return err
		}
		manifest.RequirementID = requirementID
		manifest.RequirementMaterials = directory
		manifest.UpdatedAt = s.now().UTC().Format(time.RFC3339Nano)
		if err := s.Manifests.Save(taskPath, manifest); err != nil {
			return err
		}
		if _, err := s.Documents.writeTaskDocuments(taskPath, manifest, repositoryInfos(config), nil, false); err != nil {
			return err
		}
		updated = manifest
		return nil
	})
	return updated, err
}

func (s *TaskApplicationService) resolveRequirementMaterials(ctx context.Context, config domain.AppConfig, input, folder string) (domain.RequirementMaterialsDirectory, *string, error) {
	if strings.TrimSpace(input) == "" {
		return domain.RequirementMaterialsDirectory{Status: domain.MaterialsNotRequested}, nil, nil
	}
	if s.RequirementMaterials == nil {
		return domain.RequirementMaterialsDirectory{}, nil, fmt.Errorf("任务资料准备服务未配置")
	}
	return s.RequirementMaterials(ctx, config, input, folder)
}

// withTaskManifestMutation loads and validates the manifest only after the
// task lock is held, then keeps that lock through the operation callback.
func (s *TaskApplicationService) withTaskManifestMutation(ctx context.Context, config domain.AppConfig, taskPath string, mutate func(context.Context, domain.TaskManifest) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	return s.Locks.WithTask(taskPath, func() error {
		manifest, err := s.Manifests.Load(taskPath)
		if err != nil {
			return err
		}
		if err := taskscope.Validate(ctx, config, taskPath, manifest); err != nil {
			return err
		}
		return mutate(ctx, manifest)
	})
}

func resolveRequirementMaterials(ctx context.Context, config domain.AppConfig, input, folder string, preparation *documents.PreparationService) (domain.RequirementMaterialsDirectory, *string, error) {
	value := strings.TrimRight(strings.TrimSpace(input), ",，。;；")
	requirementID := parseRequirementID(value)
	if value == "" {
		return domain.RequirementMaterialsDirectory{Status: domain.MaterialsNotRequested}, nil, nil
	}
	if !config.RequirementMaterialsConfigured() {
		return domain.RequirementMaterialsDirectory{Status: domain.MaterialsNotRequested}, requirementID, nil
	}
	directory, err := preparation.Ensure(ctx, config, value, folder)
	if err != nil {
		return domain.RequirementMaterialsDirectory{}, nil, err
	}
	return directory, requirementID, nil
}

func parseRequirementID(value string) *string {
	if value == "" {
		return nil
	}
	if isNumericID(value) {
		return &value
	}
	if link := domain.ParseFeishuWorkItemLink(value); link != nil {
		id := link.WorkItemID
		return &id
	}
	return nil
}

func isNumericID(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func (s *TaskApplicationService) AddWorkspace(ctx context.Context, config domain.AppConfig, taskPath string, request AddWorkspaceRequest) (domain.TaskManifest, error) {
	return s.addWorkspace(ctx, config, taskPath, request, false, false)
}

// AddServices preserves the source action's rule that a service can be added
// only once, even when a request selects only some of its modules.
func (s *TaskApplicationService) AddServices(ctx context.Context, config domain.AppConfig, taskPath string, request AddWorkspaceRequest) (domain.TaskManifest, error) {
	return s.addWorkspace(ctx, config, taskPath, request, true, false)
}

// AddModules preserves the source action's module identity, name, path, and
// target-branch uniqueness checks for an existing task.
func (s *TaskApplicationService) AddModules(ctx context.Context, config domain.AppConfig, taskPath string, request AddWorkspaceRequest) (domain.TaskManifest, error) {
	return s.addWorkspace(ctx, config, taskPath, request, false, true)
}

func (s *TaskApplicationService) addWorkspace(ctx context.Context, config domain.AppConfig, taskPath string, request AddWorkspaceRequest, addServices, addModules bool) (domain.TaskManifest, error) {
	var updated domain.TaskManifest
	err := s.Locks.WithTask(taskPath, func() error {
		manifest, err := s.Manifests.Load(taskPath)
		if err != nil {
			return err
		}
		if manifest.LifecycleStatus != domain.TaskActive {
			return fmt.Errorf("归档任务不能添加工作区")
		}
		group, err := config.Group(manifest.GroupID)
		if err != nil {
			return err
		}
		selections, err := selectionsForAdd(group, request, manifest.FeatureBranch)
		if err != nil {
			return err
		}
		if addServices {
			requestedIDs := []string{}
			for _, selection := range selections {
				requestedIDs = append(requestedIDs, selection.Service.ID)
			}
			for _, serviceID := range uniqueStrings(requestedIDs) {
				for _, old := range manifest.Services {
					if old.GroupServiceID == serviceID {
						return fmt.Errorf("服务已在任务中：%s", serviceID)
					}
				}
			}
		}
		if addModules {
			if len(selections) != 1 {
				return fmt.Errorf("一次只能为一个服务添加模块")
			}
			if err := requireNewTaskModules(manifest, taskPath, selections[0]); err != nil {
				return err
			}
		}
		if err := requireUniqueWorkspacePaths(taskPath, manifest.Services, selections); err != nil {
			return err
		}
		for _, selection := range selections {
			for _, module := range selection.Service.Modules {
				for _, old := range manifest.Services {
					if old.GroupServiceID == selection.Service.ID && strings.EqualFold(old.ModuleID, module.ID) {
						return fmt.Errorf("模块已在任务中：%s/%s", selection.Service.ID, module.ID)
					}
				}
			}
		}
		if _, err := s.inspectBranchReuse(ctx, config, selections, manifest.FeatureBranch); err != nil {
			return err
		}
		created := []domain.ServiceWorkspace{}
		requests := []WorkspaceProvisionRequest{}
		before := manifest
		compensate := func(primary error, restoreManifest, restoreDocuments, provisioningRollbackIncomplete bool) error {
			var compensationFailures []error
			restoreTaskState := func() {
				if restoreManifest {
					if err := s.Manifests.Save(taskPath, before); err != nil {
						compensationFailures = append(compensationFailures, fmt.Errorf("恢复任务 manifest 失败：%w", err))
						return
					}
				}
				if restoreDocuments {
					if _, err := s.Documents.WriteTaskDocument(taskPath, before, config); err != nil {
						compensationFailures = append(compensationFailures, fmt.Errorf("恢复任务 Agent 文档失败：%w", err))
					}
				}
			}
			rollbackCreated := func() {
				if err := rollbackProvisionRequests(ctx, s.Provisioning, requests, created); err != nil {
					compensationFailures = append(compensationFailures, fmt.Errorf("回滚工作区失败：%w", err))
				}
			}
			if addModules {
				rollbackCreated()
				restoreTaskState()
			} else {
				restoreTaskState()
				rollbackCreated()
			}
			if len(compensationFailures) == 0 && !provisioningRollbackIncomplete {
				return primary
			}
			allErrors := append([]error{primary}, compensationFailures...)
			if addModules {
				return errors.Join(allErrors...)
			}
			return fmt.Errorf(
				"追加服务失败，且自动回滚未完整完成；已保留任务数据供人工检查：%s: %w",
				taskPath,
				errors.Join(allErrors...),
			)
		}
		for _, effective := range selections {
			repository, ok := repositoryByID(config, effective.Service.RepositoryID)
			if !ok {
				return compensate(fmt.Errorf("服务 %s 的仓库配置不存在", effective.Service.DisplayName), false, false, false)
			}
			req := WorkspaceProvisionRequest{TaskDirectory: taskPath, Repository: repository, Service: effective.Service, RequestedFeatureBranch: manifest.FeatureBranch, ConfirmedBranchReuseKeys: request.ConfirmedBranchReuseKeys, ModuleBranches: effective.ModuleBranches, ModuleSources: effective.ModuleSources}
			if addModules {
				req.ModuleDisplayNames = make(map[string]string, len(effective.Service.Modules))
				for _, module := range effective.Service.Modules {
					req.ModuleDisplayNames[module.ID] = moduleEffectiveName(module)
				}
			}
			workspaces, err := s.Provisioning.Provision(ctx, req, s.OnOutput)
			if err != nil {
				if addModules {
					return err
				}
				return compensate(err, false, false, hasProvisionRollbackFailure(err))
			}
			requests = append(requests, req)
			created = append(created, workspaces...)
		}
		updated = manifest
		updated.Services = append(append([]domain.ServiceWorkspace(nil), manifest.Services...), created...)
		updated.UpdatedAt = s.now().UTC().Format(time.RFC3339Nano)
		if err := s.Manifests.Save(taskPath, updated); err != nil {
			return compensate(err, addModules, addModules, false)
		}
		if _, err := s.Documents.WriteTaskDocument(taskPath, updated, config); err != nil {
			return compensate(err, true, true, false)
		}
		return nil
	})
	return updated, err
}

// WorkspaceProvisioningService and the concrete provisioners join the original
// provisioning error with rollback errors when cleanup of partial work fails.
func hasProvisionRollbackFailure(err error) bool {
	for current := err; current != nil; current = errors.Unwrap(current) {
		if joined, ok := current.(interface{ Unwrap() []error }); ok {
			return len(joined.Unwrap()) > 1
		}
	}
	return false
}

// Archive only changes task navigation classification. The legacy boolean is
// accepted for callers of the earlier Go facade, but never removes checkouts.
func (s *TaskApplicationService) Archive(ctx context.Context, config domain.AppConfig, taskPath string, _ bool) (domain.TaskManifest, error) {
	var result domain.TaskManifest
	err := s.withTaskManifestMutation(ctx, config, taskPath, func(_ context.Context, manifest domain.TaskManifest) error {
		if manifest.LifecycleStatus == domain.TaskArchived {
			return fmt.Errorf("任务已经归档")
		}
		updated := manifest
		updated.LifecycleStatus = domain.TaskArchived
		updated.UpdatedAt = s.now().UTC().Format(time.RFC3339Nano)
		if err := s.Manifests.Save(taskPath, updated); err != nil {
			return err
		}
		if _, err := s.Documents.WriteTaskDocument(taskPath, updated, config); err != nil {
			return err
		}
		result = updated
		return nil
	})
	return result, err
}

func (s *TaskApplicationService) Restore(ctx context.Context, config domain.AppConfig, taskPath string) (domain.TaskManifest, error) {
	var result domain.TaskManifest
	err := s.withTaskManifestMutation(ctx, config, taskPath, func(_ context.Context, manifest domain.TaskManifest) error {
		if manifest.LifecycleStatus != domain.TaskArchived {
			return fmt.Errorf("只有已归档任务可以恢复")
		}
		updated := manifest
		updated.LifecycleStatus = domain.TaskActive
		updated.UpdatedAt = s.now().UTC().Format(time.RFC3339Nano)
		if err := s.Manifests.Save(taskPath, updated); err != nil {
			return err
		}
		if _, err := s.Documents.WriteTaskDocument(taskPath, updated, config); err != nil {
			return err
		}
		result = updated
		return nil
	})
	return result, err
}

func (s *TaskApplicationService) Delete(ctx context.Context, config domain.AppConfig, taskPath string, forceDiscard bool) error {
	return s.withTaskManifestMutation(ctx, config, taskPath, func(ctx context.Context, manifest domain.TaskManifest) error {
		risks, err := s.Lifecycle.InspectDeleteRisks(ctx, config, taskPath, manifest)
		if err != nil {
			return err
		}
		for _, risk := range risks {
			if risk.StatusCheckError != nil {
				return fmt.Errorf("存在无法安全验证的工作区，请先修复其 Git 身份或路径后再删除")
			}
		}
		if len(risks) > 0 && !forceDiscard {
			return fmt.Errorf("存在未提交改动，请确认强制丢弃后再删除")
		}
		removal, err := s.Lifecycle.RemoveAll(ctx, config, taskPath, manifest, forceDiscard)
		if err != nil {
			return err
		}
		if len(removal.RetainedBackupPaths) > 0 {
			return fmt.Errorf("任务已删除，但部分工作区备份清理失败，已保留：%s", strings.Join(removal.RetainedBackupPaths, ", "))
		}
		return nil
	})
}

func (s *TaskApplicationService) SaveTaskNotes(config domain.AppConfig, taskPath, notes string) error {
	return s.Locks.WithTask(taskPath, func() error {
		manifest, err := s.Manifests.Load(taskPath)
		if err != nil {
			return err
		}
		if _, err := s.Documents.writeTaskDocuments(taskPath, manifest, repositoryInfos(config), &notes, false); err != nil {
			return err
		}
		return nil
	})
}

func (s *TaskApplicationService) InspectDeleteRisks(ctx context.Context, config domain.AppConfig, taskPath string) ([]domain.DeleteRisk, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var risks []domain.DeleteRisk
	err := s.Locks.WithTask(taskPath, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		manifest, err := s.Manifests.Load(taskPath)
		if err != nil {
			return err
		}
		risks, err = s.Lifecycle.InspectDeleteRisks(ctx, config, taskPath, manifest)
		return err
	})
	if cancelErr := ctx.Err(); cancelErr != nil {
		return nil, cancelErr
	}
	return risks, err
}

func (s *TaskApplicationService) InspectWorkspaceRepair(config domain.AppConfig, taskPath, workspacePath string) (WorkspaceRepairPreview, error) {
	return s.Repair.Inspect(config, taskPath, workspacePath)
}

func (s *TaskApplicationService) RepairWorkspace(ctx context.Context, config domain.AppConfig, taskPath string, preview WorkspaceRepairPreview, confirmation WorkspaceRepairConfirmation) (WorkspaceRepairResult, error) {
	return s.Repair.Repair(ctx, config, taskPath, preview, confirmation)
}

func (s *TaskApplicationService) InspectModuleRemoval(config domain.AppConfig, taskPath, workspacePath string) (WorkspaceModuleRemovalPreview, error) {
	return s.ModuleRemoval.Inspect(config, taskPath, workspacePath)
}

func (s *TaskApplicationService) RemoveModule(ctx context.Context, config domain.AppConfig, taskPath string, preview WorkspaceModuleRemovalPreview, confirmation WorkspaceModuleRemovalConfirmation) (WorkspaceModuleRemovalResult, error) {
	return s.ModuleRemoval.Remove(ctx, config, taskPath, preview, confirmation)
}

// RestoreWorkspaces rehydrates a task's physical checkouts and updates health
// metadata only after every workspace has been verified or successfully created.
func (s *TaskApplicationService) RestoreWorkspaces(ctx context.Context, config domain.AppConfig, taskPath string) (domain.TaskManifest, error) {
	var result domain.TaskManifest
	err := s.Locks.WithTask(taskPath, func() error {
		manifest, err := s.Manifests.Load(taskPath)
		if err != nil {
			return err
		}
		restored, err := s.Lifecycle.RestoreAll(ctx, config, taskPath, manifest)
		if err != nil {
			return err
		}
		updated := manifest
		updated.UpdatedAt = s.now().UTC().Format(time.RFC3339Nano)
		for _, workspace := range restored {
			for i := range updated.Services {
				if samePath(updated.Services[i].WorktreePath, workspace.WorktreePath) {
					updated.Services[i] = workspace
				}
			}
		}
		if err := s.Manifests.Save(taskPath, updated); err != nil {
			return err
		}
		if _, err := s.Documents.WriteTaskDocument(taskPath, updated, config); err != nil {
			_ = s.Manifests.Save(taskPath, manifest)
			return err
		}
		result = updated
		return nil
	})
	return result, err
}

func (s *TaskApplicationService) PreviewTaskRootMigration(config domain.AppConfig, targetRoot string) (TaskRootMigrationPreview, error) {
	return s.RootMigration.Preview(config, targetRoot)
}

func (s *TaskApplicationService) MigrateTaskRoot(ctx context.Context, config domain.AppConfig, targetRoot string, onProgress func(TaskRootMigrationProgress)) (TaskRootMigrationResult, error) {
	return s.RootMigration.Migrate(ctx, config, targetRoot, onProgress)
}

func (s *TaskApplicationService) RecoverInterruptedTaskRootMigration(config domain.AppConfig) ([]string, error) {
	return s.RootMigration.RecoverInterruptedMigration(config)
}

func (s *TaskApplicationService) LaunchWorkspaceTools(ctx context.Context, taskPath string, manifest domain.TaskManifest, toolIDs []string) (domain.TaskManifest, error) {
	return s.WorkspaceTools.Launch(ctx, taskPath, manifest, toolIDs)
}

func (s *TaskApplicationService) RetryWorkspaceTool(ctx context.Context, taskPath string, manifest domain.TaskManifest, toolID string) (domain.TaskManifest, error) {
	return s.WorkspaceTools.Retry(ctx, taskPath, manifest, toolID)
}

func (s *TaskApplicationService) validateFeatureBranch(branch string) error {
	if branch == "" || strings.IndexFunc(branch, func(r rune) bool { return r == ' ' || r == '\t' || r == '\r' || r == '\n' }) >= 0 {
		return fmt.Errorf("任务分支不能为空或包含空格")
	}
	if domain.ContainsUnresolvedBranchPlaceholder(branch) {
		return fmt.Errorf("任务分支仍包含未解析的 {num} 或 {ai}")
	}
	if s.BranchValidator == nil || !s.BranchValidator.IsValid(branch) {
		return fmt.Errorf("任务分支不是合法的 Git 分支名：%s", branch)
	}
	return nil
}

func (s *TaskApplicationService) inspectBranchReuse(ctx context.Context, config domain.AppConfig, selections []effectiveTaskService, requested string) ([]BranchReuseConflict, error) {
	return s.inspectBranchReuseWithProgress(ctx, config, selections, requested, nil)
}

func (s *TaskApplicationService) inspectBranchReuseWithProgress(ctx context.Context, config domain.AppConfig, selections []effectiveTaskService, requested string, onProgress func(TaskCreationProgress)) ([]BranchReuseConflict, error) {
	var conflicts []BranchReuseConflict
	for _, effective := range selections {
		serviceChecking := TaskCreationProgress{
			Phase: TaskCreationPhaseChecking, ServiceID: effective.Service.ID, ServiceName: effective.Service.DisplayName,
		}
		var found []BranchReuseConflict
		err := runTaskCreationProgress(onProgress, serviceChecking, "正在检查服务仓库与分支引用", "服务仓库与分支引用检查完成", func() error {
			repository, ok := repositoryByID(config, effective.Service.RepositoryID)
			if !ok {
				return fmt.Errorf("服务 %s 的仓库配置不存在", effective.Service.DisplayName)
			}
			var inspectErr error
			found, inspectErr = s.BranchReuse.InspectWithProgress(ctx, repository, effective.Service, requested, effective.ModuleBranches, onProgress)
			return inspectErr
		})
		if err != nil {
			return nil, err
		}
		conflicts = append(conflicts, found...)
	}
	return uniqueBranchReuseConflicts(conflicts), nil
}

func (s *TaskApplicationService) rollbackWorkspaces(ctx context.Context, config domain.AppConfig, groupID, taskPath string, workspaces []domain.ServiceWorkspace) error {
	return s.rollbackWorkspacesWithProgress(ctx, config, groupID, taskPath, workspaces, nil)
}

func (s *TaskApplicationService) rollbackWorkspacesWithProgress(ctx context.Context, config domain.AppConfig, groupID, taskPath string, workspaces []domain.ServiceWorkspace, onProgress func(TaskCreationProgress)) error {
	var failures []error
	for i := len(workspaces) - 1; i >= 0; i-- {
		ws := workspaces[i]
		event := taskCreationProgressForWorkspace(ws, TaskCreationPhaseRollback)
		repository, ok := repositoryByID(config, ws.RepositoryID)
		if !ok {
			err := fmt.Errorf("回滚缺少仓库配置：%s", ws.RepositoryID)
			emitTaskCreationProgress(onProgress, TaskCreationProgress{Phase: event.Phase, Status: TaskCreationProgressStarted, ServiceID: event.ServiceID, ServiceName: event.ServiceName, ModuleID: event.ModuleID, ModuleName: event.ModuleName, Message: "正在回滚模块工作区"})
			failed := event
			failed.Status = TaskCreationProgressFailed
			failed.Message = err.Error()
			emitTaskCreationProgress(onProgress, failed)
			failures = append(failures, err)
			continue
		}
		service := domain.GroupServiceConfig{ID: ws.GroupServiceID, RepositoryID: ws.RepositoryID, DisplayName: ws.ServiceName, Modules: []domain.ServiceModuleConfig{}}
		if group, err := config.Group(groupID); err == nil {
			for _, configured := range group.Services {
				if configured.ID == ws.GroupServiceID {
					service = configured
					break
				}
			}
		}
		service.Modules = []domain.ServiceModuleConfig{{ID: ws.ModuleID, Name: ws.ModuleName, Strategy: ws.Strategy, MasterBranch: ws.BaseRef, TagEnabled: ws.TagEnabled, TagMode: ws.TagMode, TagTargetRef: ws.TagTargetRef, TagMessagePrefix: ws.TagMessagePrefix}}
		request := WorkspaceProvisionRequest{TaskDirectory: taskPath, Repository: repository, Service: service, RequestedFeatureBranch: ws.Branch, ModuleBranches: map[string]string{ws.ModuleID: targetBranchValue(ws)}, ModuleSources: map[string]domain.TaskModuleSource{ws.ModuleID: ws.ModuleSource}}
		if err := s.Provisioning.RollbackWithProgress(ctx, request, []domain.ServiceWorkspace{ws}, onProgress); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func rollbackProvisionRequests(ctx context.Context, provisioning *WorkspaceProvisioningService, requests []WorkspaceProvisionRequest, workspaces []domain.ServiceWorkspace) error {
	var failures []error
	for i := len(workspaces) - 1; i >= 0; i-- {
		ws := workspaces[i]
		for _, request := range requests {
			if request.Repository.ID != ws.RepositoryID || request.Service.ID != ws.GroupServiceID {
				continue
			}
			if err := provisioning.Rollback(ctx, request, []domain.ServiceWorkspace{ws}); err != nil {
				failures = append(failures, err)
			}
			break
		}
	}
	return errors.Join(failures...)
}

type effectiveTaskService struct {
	Service        domain.GroupServiceConfig
	ModuleBranches map[string]string
	ModuleSources  map[string]domain.TaskModuleSource
}

func resolveTaskSelections(group domain.GroupConfig, legacyServiceIDs []string, selections []TaskServiceSelection, overrides []ModuleBaseOverride, feature string) ([]effectiveTaskService, error) {
	if len(selections) == 0 {
		if len(legacyServiceIDs) == 0 {
			if len(overrides) != 0 {
				return nil, fmt.Errorf("未选择服务时不能指定模块覆盖")
			}
			return []effectiveTaskService{}, nil
		}
		seen := map[string]bool{}
		for _, o := range overrides {
			key := strings.ToLower(o.ServiceID + "\x00" + o.ModuleID)
			if o.ServiceID == "" || o.ModuleID == "" || strings.TrimSpace(o.BaseRef) == "" {
				return nil, fmt.Errorf("主分支覆盖项不能为空")
			}
			if seen[key] {
				return nil, fmt.Errorf("主分支覆盖项不能重复")
			}
			seen[key] = true
		}
		for _, selection := range legacyServiceIDs {
			service, err := findEnabledService(group, selection)
			if err != nil {
				return nil, err
			}
			for _, override := range overrides {
				if override.ServiceID == selection && findModule(service.Modules, override.ModuleID) == nil {
					return nil, fmt.Errorf("主分支覆盖引用了不存在的模块")
				}
			}
		}
		selections = make([]TaskServiceSelection, 0, len(legacyServiceIDs))
		for _, serviceID := range legacyServiceIDs {
			service, _ := findEnabledService(group, serviceID)
			modules := make([]TaskModuleSelection, 0, len(service.Modules))
			for _, module := range service.Modules {
				base := service.EffectiveMasterBranch(module)
				branch := defaultTargetBranch(feature, service.Modules, module)
				for _, override := range overrides {
					if override.ServiceID == serviceID && override.ModuleID == module.ID {
						base = override.BaseRef
						if override.TargetBranch != "" {
							branch = override.TargetBranch
						}
					}
				}
				modules = append(modules, configuredModuleSelection(service, module, base, branch))
			}
			selections = append(selections, TaskServiceSelection{ServiceID: serviceID, Modules: modules})
		}
	} else if len(legacyServiceIDs) > 0 && !sameStringSet(legacyServiceIDs, selectionIDs(selections)) {
		return nil, fmt.Errorf("服务选择与模块选择不一致")
	}
	seenServices := map[string]bool{}
	result := make([]effectiveTaskService, 0, len(selections))
	for _, selected := range selections {
		key := strings.ToLower(selected.ServiceID)
		if key == "" || seenServices[key] {
			return nil, fmt.Errorf("服务不能重复选择")
		}
		seenServices[key] = true
		service, err := findEnabledService(group, selected.ServiceID)
		if err != nil {
			return nil, err
		}
		if len(selected.Modules) == 0 {
			return nil, fmt.Errorf("服务至少需要一个任务模块")
		}
		modules := make([]domain.ServiceModuleConfig, 0, len(selected.Modules))
		branches, sources := map[string]string{}, map[string]domain.TaskModuleSource{}
		seenModules := map[string]bool{}
		for _, selectedModule := range selected.Modules {
			moduleID := strings.ToLower(selectedModule.ID)
			if moduleID == "" || seenModules[moduleID] {
				return nil, fmt.Errorf("模块 ID 不能重复（忽略大小写）")
			}
			seenModules[moduleID] = true
			if strings.TrimSpace(selectedModule.BaseRef) == "" {
				return nil, fmt.Errorf("主分支不能为空：%s", selectedModule.Name)
			}
			if _, err := ParseRemoteBranchRef(selectedModule.BaseRef); err != nil {
				return nil, err
			}
			if selectedModule.Strategy != domain.WorkspaceStandardWorktree && selectedModule.Strategy != domain.WorkspaceIndependentClone {
				return nil, fmt.Errorf("工作区策略不合法：%s", selectedModule.Strategy)
			}
			module := domain.ServiceModuleConfig{ID: selectedModule.ID, Name: selectedModule.Name, MasterBranch: ptr(selectedModule.BaseRef), Strategy: selectedModule.Strategy, TagEnabled: selectedModule.TagEnabled && selectedModule.Source != domain.TaskModuleTemporary, TagMode: selectedModule.TagMode, TagMessagePrefix: selectedModule.TagMessagePrefix, CustomCommands: []domain.WorkspaceCommandConfig{}}
			if selectedModule.BaseRemote != "" {
				ref, _ := ParseRemoteBranchRef(selectedModule.BaseRef)
				if ref.Remote != selectedModule.BaseRemote {
					return nil, fmt.Errorf("主分支远程与分支引用不一致：%s", selectedModule.BaseRef)
				}
			}
			if selectedModule.TagTargetRef != "" {
				if _, err := ParseRemoteBranchRef(selectedModule.TagTargetRef); err != nil {
					return nil, err
				}
				module.TagTargetRef = ptr(selectedModule.TagTargetRef)
			}
			module.TagTargetRef = service.EffectiveTagTargetRef(module)
			modules = append(modules, module)
			branch := strings.TrimSpace(selectedModule.TargetBranch)
			if selectedModule.Strategy == domain.WorkspaceStandardWorktree && branch == "" {
				return nil, fmt.Errorf("Worktree 模块目标分支不能为空：%s", selectedModule.Name)
			}
			if branch != "" {
				if strings.IndexFunc(branch, func(r rune) bool { return r == ' ' || r == '\t' || r == '\r' || r == '\n' }) >= 0 || domain.ContainsUnresolvedBranchPlaceholder(branch) {
					return nil, fmt.Errorf("模块 %s 目标分支不合法：%s", selectedModule.ID, branch)
				}
			}
			branches[selectedModule.ID] = branch
			if selectedModule.Source == "" {
				selectedModule.Source = domain.TaskModuleConfigured
			}
			sources[selectedModule.ID] = selectedModule.Source
		}
		if _, err := domain.StandardWorktreeModuleNames(modules); err != nil {
			return nil, err
		}
		worktreeTargets := []string{}
		for _, module := range modules {
			if module.Strategy == domain.WorkspaceStandardWorktree {
				worktreeTargets = append(worktreeTargets, branches[module.ID])
			}
		}
		if err := requireNoBranchPathConflicts(worktreeTargets); err != nil {
			return nil, err
		}
		service.Modules = modules
		result = append(result, effectiveTaskService{Service: service, ModuleBranches: branches, ModuleSources: sources})
	}
	return result, nil
}

func configuredModuleSelection(service domain.GroupServiceConfig, module domain.ServiceModuleConfig, baseRef, target string) TaskModuleSelection {
	return TaskModuleSelection{ID: module.ID, Name: module.Name, Strategy: module.Strategy, BaseRef: baseRef, BaseRemote: service.EffectiveMasterRemote(module), TargetBranch: target, Source: domain.TaskModuleConfigured, TagEnabled: module.TagEnabled, TagMode: module.TagMode, TagTargetRef: stringValue(module.TagTargetRef), TagMessagePrefix: module.TagMessagePrefix}
}

func selectionsForAdd(group domain.GroupConfig, request AddWorkspaceRequest, feature string) ([]effectiveTaskService, error) {
	selections := append([]TaskServiceSelection(nil), request.ServiceSelections...)
	if len(selections) == 0 && len(request.Modules) > 0 {
		selections = []TaskServiceSelection{{ServiceID: request.ServiceID, Modules: append([]TaskModuleSelection(nil), request.Modules...)}}
	}
	if len(selections) == 0 && request.ServiceID != "" && request.ModuleID == "" {
		service, err := findEnabledService(group, request.ServiceID)
		if err != nil {
			return nil, err
		}
		modules := make([]TaskModuleSelection, 0, len(service.Modules))
		for _, configured := range service.Modules {
			target := defaultTargetBranch(feature, service.Modules, configured)
			modules = append(modules, configuredModuleSelection(service, configured, service.EffectiveMasterBranch(configured), target))
		}
		selections = []TaskServiceSelection{{ServiceID: service.ID, Modules: modules}}
	} else if len(selections) == 0 && request.ModuleID != "" {
		service, err := findEnabledService(group, request.ServiceID)
		if err != nil {
			return nil, err
		}
		moduleName := request.ModuleName
		if moduleName == "" {
			moduleName = request.ModuleID
		}
		base := request.BaseRef
		if base == "" {
			if configured := findModule(service.Modules, request.ModuleID); configured != nil {
				base = service.EffectiveMasterBranch(*configured)
			}
		}
		strategy := request.Strategy
		if strategy == "" {
			if configured := findModule(service.Modules, request.ModuleID); configured != nil {
				strategy = configured.Strategy
			} else {
				strategy = domain.WorkspaceStandardWorktree
			}
		}
		branch := request.TargetBranch
		if branch == "" {
			branch = request.Branch
		}
		moduleID := request.ModuleID
		module := TaskModuleSelection{ID: moduleID, Name: moduleName, Strategy: strategy, BaseRef: base, BaseRemote: request.BaseRemote, TargetBranch: branch, Source: request.Source, TagEnabled: request.TagEnabled, TagMode: request.TagMode, TagTargetRef: request.TagTargetRef, TagMessagePrefix: request.TagMessagePrefix}
		selections = []TaskServiceSelection{{ServiceID: request.ServiceID, Modules: []TaskModuleSelection{module}}}
	}
	return resolveTaskSelections(group, request.ServiceIDs, selections, request.BaseOverrides, "")
}

func requireNewTaskModules(manifest domain.TaskManifest, taskPath string, selection effectiveTaskService) error {
	service := selection.Service
	if len(service.Modules) == 0 {
		return fmt.Errorf("至少添加一个模块")
	}
	existingWorkspaces := []domain.ServiceWorkspace{}
	for _, ws := range manifest.Services {
		if ws.GroupServiceID == service.ID {
			existingWorkspaces = append(existingWorkspaces, ws)
		}
	}
	existingIDs, existingNames, existingPaths := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, ws := range existingWorkspaces {
		existingIDs[strings.ToLower(ws.ModuleID)] = true
		existingNames[strings.ToLower(ws.ModuleName)] = true
		existingPaths[strings.ToLower(filepath.Clean(ws.WorktreePath))] = true
	}
	newIDs, newNames, newPaths := map[string]bool{}, map[string]bool{}, map[string]bool{}
	existingWorktreeBranches := []string{}
	for _, ws := range manifest.Services {
		if ws.Strategy == domain.WorkspaceStandardWorktree && ws.RepositoryID == service.RepositoryID {
			existingWorktreeBranches = append(existingWorktreeBranches, targetBranchValue(ws))
		}
	}
	branches := append([]string(nil), existingWorktreeBranches...)
	for _, module := range service.Modules {
		id := strings.ToLower(module.ID)
		name := strings.ToLower(moduleEffectiveName(module))
		if existingIDs[id] {
			return fmt.Errorf("任务中已存在同名模块")
		}
		if newIDs[id] {
			return fmt.Errorf("新增模块 ID 不能重复（忽略大小写）")
		}
		newIDs[id] = true
		if existingNames[name] {
			return fmt.Errorf("新增模块名称不能与任务中已有模块重复（忽略大小写）")
		}
		if newNames[name] {
			return fmt.Errorf("新增模块名称不能重复（忽略大小写）")
		}
		newNames[name] = true
		directory, err := moduleDirectoryName(service, module)
		if err != nil {
			return err
		}
		path := strings.ToLower(filepath.Clean(filepath.Join(taskPath, directory)))
		if existingPaths[path] || newPaths[path] {
			return fmt.Errorf("新增模块目录不能与任务中已有模块重复")
		}
		newPaths[path] = true
		if module.Strategy == domain.WorkspaceStandardWorktree {
			branch := strings.TrimSpace(selection.ModuleBranches[module.ID])
			if branch != "" {
				branches = append(branches, branch)
			}
		}
	}
	return requireNoBranchPathConflicts(branches)
}

func findEnabledService(group domain.GroupConfig, id string) (domain.GroupServiceConfig, error) {
	for _, service := range group.Services {
		if service.ID == id && service.Enabled {
			return service, nil
		}
	}
	return domain.GroupServiceConfig{}, fmt.Errorf("组 %s 中不存在或未启用服务：%s", group.Name, id)
}

func findModule(modules []domain.ServiceModuleConfig, id string) *domain.ServiceModuleConfig {
	for i := range modules {
		if strings.EqualFold(modules[i].ID, id) {
			return &modules[i]
		}
	}
	return nil
}

func requireUniqueWorkspacePaths(taskPath string, existing []domain.ServiceWorkspace, selections []effectiveTaskService) error {
	seen := map[string]bool{}
	for _, ws := range existing {
		path := strings.ToLower(filepath.Clean(ws.WorktreePath))
		if seen[path] {
			return fmt.Errorf("任务内工作区目录不能重复（忽略大小写）：%s", path)
		}
		seen[path] = true
	}
	for _, selection := range selections {
		for _, module := range selection.Service.Modules {
			directory, err := moduleDirectoryName(selection.Service, module)
			if err != nil {
				return err
			}
			path := strings.ToLower(filepath.Clean(filepath.Join(taskPath, directory)))
			if seen[path] {
				return fmt.Errorf("任务内工作区目录不能重复（忽略大小写）：%s", path)
			}
			seen[path] = true
		}
	}
	branchesByRepository := map[string][]string{}
	repositoryOrder := []string{}
	addBranch := func(repositoryID, branch string) {
		key := strings.ToLower(repositoryID)
		if _, exists := branchesByRepository[key]; !exists {
			repositoryOrder = append(repositoryOrder, key)
		}
		branchesByRepository[key] = append(branchesByRepository[key], branch)
	}
	for _, ws := range existing {
		if ws.Strategy == domain.WorkspaceStandardWorktree {
			addBranch(ws.RepositoryID, targetBranchValue(ws))
		}
	}
	for _, selection := range selections {
		for _, module := range selection.Service.Modules {
			if module.Strategy == domain.WorkspaceStandardWorktree {
				addBranch(selection.Service.RepositoryID, selection.ModuleBranches[module.ID])
			}
		}
	}
	for _, repositoryID := range repositoryOrder {
		if err := requireNoBranchPathConflicts(branchesByRepository[repositoryID]); err != nil {
			return err
		}
	}
	return nil
}

func repositoryByID(config domain.AppConfig, id string) (domain.RepositoryConfig, bool) {
	for _, repository := range config.Repositories {
		if repository.ID == id {
			return repository, true
		}
	}
	return domain.RepositoryConfig{}, false
}

func taskRoot(config domain.AppConfig) (string, error) {
	if config.TaskRoot == nil || strings.TrimSpace(*config.TaskRoot) == "" {
		return "", fmt.Errorf("尚未配置任务根目录")
	}
	return filepath.Abs(filepath.Clean(*config.TaskRoot))
}

func uniqueBranchReuseConflicts(conflicts []BranchReuseConflict) []BranchReuseConflict {
	seen := map[BranchReuseKey]bool{}
	out := make([]BranchReuseConflict, 0, len(conflicts))
	for _, conflict := range conflicts {
		if !seen[conflict.Key] {
			seen[conflict.Key] = true
			out = append(out, conflict)
		}
	}
	return out
}

func selectionIDs(selections []TaskServiceSelection) []string {
	out := make([]string, 0, len(selections))
	for _, selection := range selections {
		out = append(out, selection.ServiceID)
	}
	return out
}
func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]int{}
	for _, value := range a {
		seen[strings.ToLower(value)]++
	}
	for _, value := range b {
		seen[strings.ToLower(value)]--
	}
	for _, count := range seen {
		if count != 0 {
			return false
		}
	}
	return true
}
func mustAbs(path string) string {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return absolute
}
func (s *TaskApplicationService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
func targetBranchValue(ws domain.ServiceWorkspace) string {
	if ws.TargetBranch != nil {
		return *ws.TargetBranch
	}
	return ws.Branch
}

func requireAvailableTaskDirectory(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("本地目录已存在，请修改文件夹名：%s", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}
