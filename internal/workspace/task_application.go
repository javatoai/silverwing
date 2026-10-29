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

// RequirementMaterialsResolver is injected by the application layer so task
// creation and other task workflows share the same materials preparation
// boundary as document and delivery flows.
type RequirementMaterialsResolver func(ctx context.Context, config domain.AppConfig, requirementInput, folderName string) (domain.RequirementMaterialsDirectory, *string, error)

type CreateTaskRequest struct {
	FolderName           string                   `json:"folderName"`
	GroupID              string                   `json:"groupId"`
	RequirementLink      string                   `json:"requirementLink"`
	TaskNotes            string                   `json:"taskNotes"`
	AgentContext         *domain.AgentTaskContext `json:"agentContext,omitempty"`
	AgentHandoffMarkdown *string                  `json:"agentHandoffMarkdown,omitempty"`
}
type TaskApplicationService struct {
	Manifests            TaskManifestRepository
	Paths                storage.Paths
	Locks                *OperationLocks
	Documents            *AgentDocumentService
	RootMigration        *TaskRootMigrationService
	Defaults             *TaskCreationDefaultsService
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

func (s *TaskApplicationService) CreateTask(ctx context.Context, config domain.AppConfig, request CreateTaskRequest) (domain.TaskManifest, error) {
	return s.CreateTaskWithProgress(ctx, config, request, nil)
}

func (s *TaskApplicationService) CreateTaskWithProgress(ctx context.Context, config domain.AppConfig, request CreateTaskRequest, progress func(TaskCreationProgress)) (manifest domain.TaskManifest, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err = ctx.Err(); err != nil {
		return
	}
	if (request.AgentContext == nil) != (request.AgentHandoffMarkdown == nil) {
		return manifest, fmt.Errorf("Agent 任务上下文与交接文档必须同时提供")
	}
	root, err := taskRoot(config)
	if err != nil {
		return
	}
	name, err := RequireValidTaskDirectoryName(request.FolderName)
	if err != nil {
		return manifest, err
	}
	groupID := request.GroupID
	if groupID == "" {
		groupID = domain.DefaultGroupID
	}
	if _, err = config.Group(groupID); err != nil {
		return
	}
	path := filepath.Join(root, name)
	err = s.Locks.WithTaskRoot(root, func() error {
		return s.Locks.WithTask(path, func() (createErr error) {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := requireAvailableTaskDirectory(path); err != nil {
				return err
			}
			manifest = domain.DefaultTaskManifest()
			manifest.FolderName, manifest.TaskDirectoryName, manifest.GroupID = request.FolderName, name, groupID
			manifest.RequirementLink = strings.TrimSpace(request.RequirementLink)
			createdTime := s.now()
			manifest.CreatedAt = domain.FormatSilverWingTime(createdTime)
			manifest.UpdatedAt = manifest.CreatedAt
			manifest.AgentContext = request.AgentContext
			emitTaskCreationProgress(progress, TaskCreationProgress{Phase: TaskCreationPhasePreparingMaterials, Status: TaskCreationProgressStarted, Message: "正在关联任务资料"})
			if err := ctx.Err(); err != nil {
				return err
			}
			if manifest.RequirementLink != "" {
				materials, id, resolveErr := s.resolveRequirementMaterials(documents.WithMaterialsCreationTime(ctx, createdTime), config, manifest.RequirementLink, request.FolderName)
				if resolveErr != nil {
					if ctxErr := ctx.Err(); ctxErr != nil {
						return errors.Join(resolveErr, ctxErr)
					}
					return resolveErr
				}
				manifest.RequirementMaterials, manifest.RequirementID = materials, id
				message := "需求资料准备步骤已返回"
				if materials.Status == domain.MaterialsFailed && materials.FailureReason != nil && strings.TrimSpace(*materials.FailureReason) != "" {
					message = "需求资料未能准备，但任务创建可以继续：" + *materials.FailureReason
				}
				emitTaskCreationProgress(progress, TaskCreationProgress{Phase: TaskCreationPhasePreparingMaterials, Status: TaskCreationProgressCompleted, Message: message})
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := requireAvailableTaskDirectory(path); err != nil {
				return err
			}
			if err := os.MkdirAll(root, 0755); err != nil {
				return err
			}
			if err := os.Mkdir(path, 0755); err != nil {
				return err
			}
			ownedDirectory, statErr := os.Lstat(path)
			if statErr != nil {
				return fmt.Errorf("检查新建任务目录 %s：%w", path, statErr)
			}
			// Pin the Windows file ID before any caller progress callback can
			// rename this directory and create a replacement at the same path.
			if !os.SameFile(ownedDirectory, ownedDirectory) {
				return fmt.Errorf("无法确认新建任务目录身份，已保留现场：%s", path)
			}
			committed := false
			defer func() {
				if !committed {
					if cleanupErr := cleanupTaskCreationDirectory(path, ownedDirectory); cleanupErr != nil {
						createErr = errors.Join(createErr, fmt.Errorf("任务创建失败后的目录清理未完成：%w", cleanupErr))
					}
				}
			}()
			emitTaskCreationProgress(progress, TaskCreationProgress{Phase: TaskCreationPhaseSavingManifest, Status: TaskCreationProgressStarted, Message: "正在保存任务"})
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := s.Manifests.Save(path, manifest); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if _, err := s.Documents.WriteTaskDocument(path, manifest, config); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := s.Documents.WriteTaskNotes(path, request.TaskNotes); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if request.AgentHandoffMarkdown != nil {
				if _, err := s.Documents.WriteHandoff(path, *request.AgentHandoffMarkdown); err != nil {
					return err
				}
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			committed = true
			emitTaskCreationProgress(progress, TaskCreationProgress{Phase: TaskCreationPhaseCompleted, Status: TaskCreationProgressCompleted, Message: "任务已创建"})
			return nil
		})
	})
	return
}

// RetryRequirementMaterials re-runs only the independent materials-directory
// preparation and persists the new result without touching Git workspaces.
func (s *TaskApplicationService) RetryRequirementMaterials(ctx context.Context, config domain.AppConfig, taskPath string) (domain.TaskManifest, error) {
	var updated domain.TaskManifest
	err := s.withTaskManifestMutation(ctx, config, taskPath, func(ctx context.Context, manifest domain.TaskManifest) error {
		if strings.TrimSpace(manifest.RequirementLink) == "" {
			return fmt.Errorf("任务未填写需求编号或飞书需求链接")
		}
		if created, err := domain.ParseSilverWingTime(manifest.CreatedAt); err == nil {
			ctx = documents.WithMaterialsCreationTime(ctx, created)
		}
		directory, requirementID, err := s.resolveRequirementMaterials(ctx, config, manifest.RequirementLink, manifest.FolderName)
		if err != nil {
			return err
		}
		manifest.RequirementID = requirementID
		manifest.RequirementMaterials = directory
		manifest.UpdatedAt = s.now().UTC().Format(time.RFC3339Nano)
		if err := s.Manifests.Save(taskPath, manifest); err != nil {
			return err
		}
		if _, err := s.Documents.writeTaskDocuments(taskPath, manifest, nil, false); err != nil {
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

func (s *TaskApplicationService) Archive(ctx context.Context, config domain.AppConfig, path string, _ bool) (result domain.TaskManifest, err error) {
	err = s.withTaskManifestMutation(ctx, config, path, func(_ context.Context, m domain.TaskManifest) error {
		if m.LifecycleStatus == domain.TaskArchived {
			return fmt.Errorf("任务已经归档")
		}
		m.LifecycleStatus = domain.TaskArchived
		m.UpdatedAt = domain.FormatSilverWingTime(s.now())
		if err := s.Manifests.Save(path, m); err != nil {
			return err
		}
		if _, err := s.Documents.EnsureTaskDocuments(path, m, config); err != nil {
			return err
		}
		result = m
		return nil
	})
	return
}

func (s *TaskApplicationService) Restore(ctx context.Context, config domain.AppConfig, path string) (result domain.TaskManifest, err error) {
	err = s.withTaskManifestMutation(ctx, config, path, func(_ context.Context, m domain.TaskManifest) error {
		if m.LifecycleStatus != domain.TaskArchived {
			return fmt.Errorf("只有已归档任务可以恢复")
		}
		m.LifecycleStatus = domain.TaskActive
		m.UpdatedAt = domain.FormatSilverWingTime(s.now())
		if err := s.Manifests.Save(path, m); err != nil {
			return err
		}
		if _, err := s.Documents.EnsureTaskDocuments(path, m, config); err != nil {
			return err
		}
		result = m
		return nil
	})
	return
}

func (s *TaskApplicationService) Delete(ctx context.Context, config domain.AppConfig, path string, _ bool) error {
	return s.withTaskManifestMutation(ctx, config, path, func(ctx context.Context, manifest domain.TaskManifest) error {
		root, err := taskRoot(config)
		if err != nil {
			return err
		}
		rootInfo, err := os.Stat(root)
		if err != nil {
			return fmt.Errorf("检查任务根目录：%w", err)
		}
		if !rootInfo.IsDir() {
			return fmt.Errorf("任务根目录不是目录：%s", root)
		}
		target, err := filepath.Abs(filepath.Clean(path))
		if err != nil {
			return err
		}
		if samePath(target, root) || !samePath(filepath.Dir(target), root) || filepath.Base(target) != manifest.TaskDirectoryName {
			return fmt.Errorf("任务目录与配置或任务清单不匹配：%s", target)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return os.RemoveAll(target)
	})
}

func (s *TaskApplicationService) SaveTaskNotes(config domain.AppConfig, taskPath, notes string) error {
	return s.Locks.WithTask(taskPath, func() error {
		manifest, err := s.Manifests.Load(taskPath)
		if err != nil {
			return err
		}
		if _, err := s.Documents.writeTaskDocuments(taskPath, manifest, &notes, false); err != nil {
			return err
		}
		return nil
	})
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

func taskRoot(config domain.AppConfig) (string, error) {
	if config.TaskRoot == nil || strings.TrimSpace(*config.TaskRoot) == "" {
		return "", fmt.Errorf("尚未配置任务根目录")
	}
	return filepath.Abs(filepath.Clean(*config.TaskRoot))
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

func requireAvailableTaskDirectory(path string) error {
	if _, err := os.Lstat(path); err == nil {
		return fmt.Errorf("本地目录已存在，请修改文件夹名：%s", path)
	} else if !os.IsNotExist(err) {
		return err
	}
	return nil
}

func cleanupTaskCreationDirectory(taskPath string, ownedDirectory os.FileInfo) error {
	current, err := os.Lstat(taskPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("检查待清理任务目录 %s：%w", taskPath, err)
	}
	if ownedDirectory == nil || !current.IsDir() || !os.SameFile(ownedDirectory, current) {
		return fmt.Errorf("任务目录已被替换，保留现有路径：%s", taskPath)
	}
	if reparse, err := bootstrapPathIsReparsePoint(taskPath, current); err != nil {
		return fmt.Errorf("检查待清理任务目录 %s：%w", taskPath, err)
	} else if reparse {
		return fmt.Errorf("任务目录已变为链接或重解析点，保留现有路径：%s", taskPath)
	}

	var cleanupErrors []error
	if err := removeGeneratedTaskDocuments(taskPath, ownedDirectory); err != nil {
		cleanupErrors = append(cleanupErrors, err)
	}
	current, err = os.Lstat(taskPath)
	if errors.Is(err, os.ErrNotExist) {
		return errors.Join(cleanupErrors...)
	}
	if err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("检查待清理任务目录 %s：%w", taskPath, err))
		return errors.Join(cleanupErrors...)
	}
	if !current.IsDir() || !os.SameFile(ownedDirectory, current) {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("任务目录已被替换，保留现有路径：%s", taskPath))
		return errors.Join(cleanupErrors...)
	}
	if reparse, err := bootstrapPathIsReparsePoint(taskPath, current); err != nil {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("检查待清理任务目录 %s：%w", taskPath, err))
		return errors.Join(cleanupErrors...)
	} else if reparse {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("任务目录已变为链接或重解析点，保留现有路径：%s", taskPath))
		return errors.Join(cleanupErrors...)
	}
	if err := os.Remove(taskPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		cleanupErrors = append(cleanupErrors, taskCreationRemovalError(taskPath, err))
	}
	return errors.Join(cleanupErrors...)
}

func removeGeneratedTaskDocuments(taskPath string, ownedDirectory os.FileInfo) error {
	paths := []struct {
		path      string
		directory bool
	}{
		{path: filepath.Join(taskPath, storage.TaskManifestFileName)},
		{path: filepath.Join(taskPath, AgentRouterFileName)},
		{path: filepath.Join(taskPath, AgentWorkspaceDir, AgentDirectoryName, AgentContextFileName)},
		{path: filepath.Join(taskPath, AgentWorkspaceDir, AgentDirectoryName, AgentRulesFileName)},
		{path: filepath.Join(taskPath, AgentWorkspaceDir, AgentDirectoryName), directory: true},
		{path: filepath.Join(taskPath, AgentWorkspaceDir, HandoffFileName)},
		{path: filepath.Join(taskPath, AgentWorkspaceDir), directory: true},
	}
	var cleanupErrors []error
	for _, target := range paths {
		if err := removeTaskCreationPath(taskPath, target.path, target.directory, ownedDirectory); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	return errors.Join(cleanupErrors...)
}

func removeTaskCreationPath(taskPath, targetPath string, wantDirectory bool, ownedDirectory os.FileInfo) error {
	relative, err := filepath.Rel(taskPath, targetPath)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("拒绝清理任务目录外的路径：%s", targetPath)
	}
	rootInfo, err := os.Lstat(taskPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("检查待清理任务目录 %s：%w", taskPath, err)
	}
	if ownedDirectory == nil || !rootInfo.IsDir() || !os.SameFile(ownedDirectory, rootInfo) {
		return fmt.Errorf("任务目录已被替换，保留生成路径：%s", targetPath)
	}
	if reparse, err := bootstrapPathIsReparsePoint(taskPath, rootInfo); err != nil {
		return fmt.Errorf("检查待清理任务目录 %s：%w", taskPath, err)
	} else if reparse {
		return fmt.Errorf("任务目录已变为链接或重解析点，保留生成路径：%s", targetPath)
	}

	currentPath := taskPath
	components := strings.Split(relative, string(os.PathSeparator))
	for index, component := range components {
		currentPath = filepath.Join(currentPath, component)
		info, err := os.Lstat(currentPath)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("检查待清理生成路径 %s：%w", currentPath, err)
		}
		reparse, err := bootstrapPathIsReparsePoint(currentPath, info)
		if err != nil {
			return fmt.Errorf("检查待清理生成路径 %s：%w", currentPath, err)
		}
		if reparse {
			return fmt.Errorf("保留链接或重解析点路径，未沿路径清理：%s", currentPath)
		}
		leaf := index == len(components)-1
		if !leaf && !info.IsDir() {
			return fmt.Errorf("保留非目录路径，未继续清理其下内容：%s", currentPath)
		}
		if leaf {
			if wantDirectory && !info.IsDir() {
				return taskCreationRemovalError(currentPath, fmt.Errorf("保留非目录生成路径"))
			}
			if !wantDirectory && !info.Mode().IsRegular() {
				return taskCreationRemovalError(currentPath, fmt.Errorf("保留非普通文件生成路径"))
			}
			if err := os.Remove(currentPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				return taskCreationRemovalError(currentPath, err)
			}
		}
	}
	return nil
}

func taskCreationRemovalError(path string, cause error) error {
	info, statErr := os.Lstat(path)
	if statErr == nil {
		reparse, reparseErr := bootstrapPathIsReparsePoint(path, info)
		if reparseErr != nil {
			return fmt.Errorf("无法确认任务生成路径 %s 是否安全，已保留：%w", path, errors.Join(cause, reparseErr))
		}
		if reparse {
			return fmt.Errorf("保留链接或重解析点路径 %s：%w", path, cause)
		}
		if info.IsDir() {
			entries, readErr := os.ReadDir(path)
			if readErr == nil && len(entries) > 0 {
				remaining := make([]string, 0, len(entries))
				for _, entry := range entries {
					remaining = append(remaining, filepath.Join(path, entry.Name()))
				}
				return fmt.Errorf("保留非生成内容 %s：%w", strings.Join(remaining, ", "), cause)
			}
		}
	}
	return fmt.Errorf("无法清理任务生成路径 %s：%w", path, cause)
}

func (s *TaskApplicationService) RefreshAgents(config domain.AppConfig, taskPath string) (path string, err error) {
	err = s.withTaskManifestMutation(context.Background(), config, taskPath, func(_ context.Context, manifest domain.TaskManifest) error {
		var e error
		path, e = s.Documents.EnsureTaskDocuments(taskPath, manifest, config)
		return e
	})
	return
}
