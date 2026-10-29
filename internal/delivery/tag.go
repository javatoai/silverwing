package delivery

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"silverwing-go/internal/commandruntime"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/executable"
	"silverwing-go/internal/process"
	"silverwing-go/internal/storage"
	"silverwing-go/internal/workspace"
)

type Service struct {
	paths       storage.Paths
	config      *storage.ConfigStore
	manifests   *storage.ManifestStore
	git         tagGitClient
	runner      tagProcessRunner
	locks       *workspace.OperationLocks
	store       tagOperationStore
	preparation PreparationAPI
	tasks       *workspace.TaskApplicationService
	progress    func(TagProgress)
	lockedTask  string
}

type tagProcessRunner interface {
	Run(context.Context, process.Spec) (process.Result, error)
}

// NewService preserves the legacy concrete task-service defaults.
func NewService(paths storage.Paths, config *storage.ConfigStore, manifests *storage.ManifestStore, git *workspace.GitClient, runner *process.Runner) *Service {
	return newService(paths, config, manifests, git, runner, func(manifests *storage.ManifestStore, git *workspace.GitClient) *workspace.TaskApplicationService {
		return workspace.NewTaskApplicationService(manifests, git, paths)
	})
}

// NewServiceWithTasks is the production composition entry point. It uses the
// supplied task facade without choosing a materials provider or task storage.
func NewServiceWithTasks(paths storage.Paths, config *storage.ConfigStore, manifests *storage.ManifestStore, git *workspace.GitClient, runner *process.Runner, tasks *workspace.TaskApplicationService) *Service {
	return newService(paths, config, manifests, git, runner, func(*storage.ManifestStore, *workspace.GitClient) *workspace.TaskApplicationService { return tasks })
}

func newService(paths storage.Paths, config *storage.ConfigStore, manifests *storage.ManifestStore, git *workspace.GitClient, runner *process.Runner, taskFactory func(*storage.ManifestStore, *workspace.GitClient) *workspace.TaskApplicationService) *Service {
	if config == nil {
		config = storage.NewConfigStore(paths)
	}
	if manifests == nil {
		manifests = storage.NewManifestStore()
	}
	if git == nil {
		git = workspace.NewGitClient(runner, "")
	}
	if runner == nil {
		runner = git.Runner
	}
	if runner == nil {
		runner = process.NewRunner()
	}
	if git.Runner == nil {
		git.Runner = runner
	}
	return &Service{
		paths: paths, config: config, manifests: manifests, git: git, runner: runner,
		locks: workspace.NewOperationLocks(paths.Locks), tasks: taskFactory(manifests, git),
	}
}

// Execute is the single typed-process entry point shared by CLI and desktop
// adapters. Payloads are JSON-compatible maps produced by the caller.
func (s *Service) Execute(ctx context.Context, kind string, payload map[string]any) (any, error) {
	switch kind {
	case "tag.build":
		return s.buildSelected(ctx, stringValue(payload, "taskFolder", "taskId", "task"), stringSlice(payload, "selectionKeys", "serviceIds"), boolValue(payload, "allServices"), boolValue(payload, "batch"))
	case "tag.status":
		return s.Status(ctx, stringValue(payload, "taskFolder", "taskId", "task"), stringValue(payload, "operationId"))
	case "tag.history":
		return s.History(ctx, stringValue(payload, "taskFolder", "taskId", "task"))
	case "tag.retry":
		return s.Retry(ctx, stringValue(payload, "taskFolder", "taskId", "task"), stringValue(payload, "operationId"))
	case "tag.workspace-check":
		return s.WorkspaceCheck(ctx, stringValue(payload, "taskFolder", "taskId", "task"), stringValue(payload, "operationId"))
	case "tag.preflight":
		return s.Preflight(ctx, stringValue(payload, "taskFolder", "taskId", "task"), stringValue(payload, "selectionKey", "serviceId"))
	case "tag.target.update":
		return s.updateTargetPayload(ctx, payload)
	case "agent.handoff-template":
		return handoffTemplate(), nil
	case "agent.inspect", "agent.plan", "agent.apply", "agent.status":
		return s.executeAgent(ctx, kind, payload)
	default:
		return nil, fmt.Errorf("交付功能不支持：%s", kind)
	}
}

type TagRetryKind string

const (
	TagRetryResolveConflict TagRetryKind = "RESOLVE_CONFLICT"
	TagRetryInterrupted     TagRetryKind = "RETRY_INTERRUPTED"
	TagRetryBuild           TagRetryKind = "RETRY_BUILD"
	TagRetryPartial         TagRetryKind = "RESUME_PARTIAL"
	TagRetryRetag           TagRetryKind = "RETAG"
	TagRetryNone            TagRetryKind = "NONE"
)

type TagOperationReport struct {
	Operation domain.TagOperation `json:"operation"`
	RetryKind TagRetryKind        `json:"retryKind"`
	Guidance  *string             `json:"guidance"`
}

func (report TagOperationReport) ConflictSummary() string {
	target := "目标分支"
	if report.Operation.TargetBranch != nil {
		target = report.Operation.Remote + "/" + *report.Operation.TargetBranch
	}
	return mergeConflictMessage(report.Operation.ServiceName, report.Operation.SourceBranch, target)
}

type TagWorkspaceCheckReport struct {
	Clean   bool     `json:"clean"`
	Changes []string `json:"changes"`
}

type FeatureSyncStatus struct {
	RemoteExists bool `json:"remoteExists"`
	Ahead        int  `json:"ahead"`
	Behind       int  `json:"behind"`
}

func (s FeatureSyncStatus) CanPush() bool      { return s.Behind == 0 }
func (s FeatureSyncStatus) PushRequired() bool { return !s.RemoteExists || s.Ahead > 0 }

type TagPreflight struct {
	FolderName   string              `json:"folderName"`
	ServiceName  string              `json:"serviceName"`
	SourceBranch string              `json:"sourceBranch"`
	SourceSHA    string              `json:"sourceSha"`
	Remote       string              `json:"remote"`
	TargetBranch *string             `json:"targetBranch"`
	TargetSHA    *string             `json:"targetSha"`
	SourceSync   FeatureSyncStatus   `json:"sourceSync"`
	TagMode      domain.TagBuildMode `json:"tagMode"`
	MergeMode    *string             `json:"mergeMode"`
	CommitList   []string            `json:"commitList"`
	DiffStat     string              `json:"diffStat"`
	EstimatedTag string              `json:"estimatedTag"`
}

type resolvedTask struct {
	config    domain.AppConfig
	directory string
	manifest  domain.TaskManifest
}

func (s *Service) resolveTask(taskFolder string) (resolvedTask, error) {
	folder := strings.TrimSpace(taskFolder)
	if folder == "" {
		return resolvedTask{}, errors.New("任务文件夹名不能为空")
	}
	config, err := s.config.Load()
	if err != nil {
		return resolvedTask{}, err
	}
	if config.TaskRoot == nil || strings.TrimSpace(*config.TaskRoot) == "" {
		return resolvedTask{}, errors.New("尚未配置任务根目录")
	}
	root, err := filepath.Abs(*config.TaskRoot)
	if err != nil {
		return resolvedTask{}, err
	}
	directory, err := filepath.Abs(filepath.Join(root, folder))
	if err != nil {
		return resolvedTask{}, err
	}
	if !samePath(filepath.Dir(directory), root) {
		return resolvedTask{}, errors.New("任务目录必须是任务根目录的直接子目录")
	}
	manifestPath := filepath.Join(directory, storage.TaskManifestFileName)
	info, err := os.Stat(manifestPath)
	if err != nil || !info.Mode().IsRegular() {
		return resolvedTask{}, fmt.Errorf("找不到研发任务：%s", folder)
	}
	manifest, err := s.manifests.Load(directory)
	if err != nil {
		return resolvedTask{}, err
	}
	if filepath.Base(directory) != manifest.TaskDirectoryName {
		return resolvedTask{}, fmt.Errorf("任务目录与任务清单名称不一致：%s（清单记录：%s）", folder, manifest.TaskDirectoryName)
	}
	return resolvedTask{config, directory, manifest}, nil
}

func (s *Service) Build(ctx context.Context, taskFolder string, selections []string, allServices bool) ([]TagOperationReport, error) {
	return s.buildSelected(ctx, taskFolder, selections, allServices, false)
}

func (s *Service) buildSelected(ctx context.Context, taskFolder string, selections []string, allServices, batch bool) ([]TagOperationReport, error) {
	if allServices && len(selections) != 0 {
		return nil, errors.New("--all-services 与 --service 不能同时使用")
	}
	task, err := s.resolveTask(taskFolder)
	if err != nil {
		return nil, err
	}
	if !task.config.TagEnabled {
		return nil, errors.New("全局测试Tag已关闭")
	}
	if _, err := task.config.Group(task.manifest.GroupID); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(selections))
	if allServices {
		for _, service := range task.manifest.Services {
			if service.TagEnabled {
				keys = append(keys, service.SelectionKey())
			}
		}
		if len(keys) == 0 {
			return nil, errors.New("任务内没有可构建测试Tag的服务")
		}
	} else if len(selections) != 0 {
		for _, key := range selections {
			canonical, err := canonicalSelection(task.manifest, key)
			if err != nil {
				return nil, err
			}
			keys = appendUnique(keys, canonical)
		}
	} else {
		return nil, errors.New("请指定 --service 或 --all-services")
	}
	var operations []domain.TagOperation
	batchID := ""
	if batch || allServices || len(keys) > 1 {
		batchID = newUUID()
	}
	err = s.withTaskLock(task.directory, func() error {
		for _, key := range keys {
			operation, buildErr := s.buildSafely(ctx, task, key, batchID)
			if buildErr != nil {
				return buildErr
			}
			operations = append(operations, operation)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	reports := make([]TagOperationReport, len(operations))
	for i, operation := range operations {
		reports[i] = operationReport(operation, task.manifest.TaskDirectoryName)
	}
	return reports, nil
}

func canonicalSelection(manifest domain.TaskManifest, key string) (string, error) {
	var matches []domain.ServiceWorkspace
	for _, service := range manifest.Services {
		if service.SelectionKey() == key || service.RepositoryID == key {
			matches = append(matches, service)
		}
	}
	if len(matches) != 1 {
		return key, nil
	}
	if !matches[0].TagEnabled {
		return "", fmt.Errorf("模块 %s 已关闭测试Tag", matches[0].ModuleName)
	}
	return matches[0].SelectionKey(), nil
}

func appendUnique(values []string, value string) []string {
	for _, old := range values {
		if old == value {
			return values
		}
	}
	return append(values, value)
}

func (s *Service) buildSafely(ctx context.Context, task resolvedTask, selection, batchID string) (domain.TagOperation, error) {
	return s.buildOperationSafely(ctx, task, selection, batchID, nil)
}

func (s *Service) buildOperationSafely(ctx context.Context, task resolvedTask, selection, batchID string, initial *domain.TagOperation) (domain.TagOperation, error) {
	operation, err := s.buildUnlocked(ctx, task, selection, initial, batchID)
	if err == nil {
		return operation, nil
	}
	if operation.OperationID != "" {
		return operation, err
	}
	workspaceRecord, found, lookupErr := resolveWorkspace(task.manifest, selection)
	if lookupErr != nil {
		return domain.TagOperation{}, lookupErr
	}
	if !found {
		return domain.TagOperation{}, err
	}
	target, targetErr := domain.ResolveEffectiveTagTarget(task.config, task.manifest, selection)
	var now = formatSilverwingTime(time.Now())
	operation = domain.TagOperation{
		OperationID: newUUID(), FolderName: task.manifest.FolderName, ServiceName: workspaceRecord.ServiceName,
		RepositoryID: workspaceRecord.RepositoryID, SourceBranch: workspaceRecord.Branch,
		Remote: "", TagMode: domain.TagBuildMergeToTargetBranch, State: domain.TagOperationFailed,
		CreatedAt: now, UpdatedAt: now, Message: stringPointer(err.Error()),
		ConflictFiles: []string{}, GroupServiceID: workspaceRecord.GroupServiceID, ModuleID: workspaceRecord.ModuleID,
		GenbuStatus: initialGenbuStatus(), BatchID: nonEmptyPointer(batchID),
	}
	if targetErr == nil {
		operation.TargetBranch = target.TargetBranch
		operation.Remote, operation.TagMode = target.Remote, target.Mode
	} else if targetErr != nil && err == nil {
		operation.Message = stringPointer(targetErr.Error())
	}
	if initial != nil {
		operation.OperationID, operation.CreatedAt = initial.OperationID, initial.CreatedAt
	}
	if saveErr := s.store.save(ctx, task.directory, operation); saveErr != nil {
		return domain.TagOperation{}, errors.Join(err, saveErr)
	}
	s.notifyTag("failed", value(operation.Message), tagFailureKind(err), &operation)
	return operation, nil
}

func resolveWorkspace(manifest domain.TaskManifest, selection string) (domain.ServiceWorkspace, bool, error) {
	var matches []domain.ServiceWorkspace
	for _, service := range manifest.Services {
		if service.SelectionKey() == selection || service.RepositoryID == selection {
			matches = append(matches, service)
		}
	}
	if len(matches) > 1 {
		return domain.ServiceWorkspace{}, false, fmt.Errorf("测试Tag目标不唯一，请选择具体模块：%s", selection)
	}
	if len(matches) == 0 {
		return domain.ServiceWorkspace{}, false, nil
	}
	return matches[0], true, nil
}

func (s *Service) buildUnlocked(ctx context.Context, task resolvedTask, selection string, previous *domain.TagOperation, batchID string) (domain.TagOperation, error) {
	var target domain.EffectiveTagTarget
	var err error
	if previous != nil {
		target, err = domain.ResolveHistoricalTagTarget(task.config, task.manifest, *previous)
	} else {
		target, err = domain.ResolveEffectiveTagTarget(task.config, task.manifest, selection)
	}
	if err != nil {
		return domain.TagOperation{}, err
	}
	validated, err := s.validateForMutation(ctx, task, target.Workspace)
	if err != nil {
		return domain.TagOperation{}, err
	}
	workspaceRecord := target.Workspace
	now := formatSilverwingTime(time.Now())
	var operation domain.TagOperation
	if previous != nil {
		operation = *previous
		operation.FolderName, operation.ServiceName, operation.RepositoryID = task.manifest.FolderName, workspaceRecord.ServiceName, workspaceRecord.RepositoryID
		operation.SourceBranch, operation.TargetBranch, operation.Remote, operation.TagMode = workspaceRecord.Branch, target.TargetBranch, target.Remote, target.Mode
		operation.State, operation.UpdatedAt = domain.TagOperationCreated, now
		operation.SourceSHA, operation.TargetSHA, operation.Tag, operation.Message = nil, nil, nil, nil
		operation.ConflictFiles = []string{}
		operation.GroupServiceID, operation.ModuleID = workspaceRecord.GroupServiceID, workspaceRecord.ModuleID
		operation.GenbuStatus = initialGenbuStatus()
	} else {
		operation = domain.TagOperation{
			OperationID: newUUID(), FolderName: task.manifest.FolderName, ServiceName: workspaceRecord.ServiceName,
			RepositoryID: workspaceRecord.RepositoryID, SourceBranch: workspaceRecord.Branch, TargetBranch: target.TargetBranch,
			Remote: target.Remote, TagMode: target.Mode, State: domain.TagOperationCreated,
			CreatedAt: now, UpdatedAt: now, ConflictFiles: []string{}, GroupServiceID: workspaceRecord.GroupServiceID,
			ModuleID: workspaceRecord.ModuleID, GenbuStatus: initialGenbuStatus(), BatchID: nonEmptyPointer(batchID),
		}
	}
	if err := s.store.save(ctx, task.directory, operation); err != nil {
		return domain.TagOperation{}, err
	}
	s.notifyTag("checking", "检查工作区与远端", "", &operation)
	s.recordTagBuildStarted(operation)
	repository := validated.repository
	err = s.withRepositoryLock(ctx, repository, func() error {
		return s.runTagBuild(ctx, task, target, validated, &operation)
	})
	if err != nil {
		return operation, err
	}
	return operation, nil
}

type mutationTarget struct{ repository, worktree string }

func (s *Service) validateForMutation(ctx context.Context, task resolvedTask, service domain.ServiceWorkspace) (mutationTarget, error) {
	if err := ctx.Err(); err != nil {
		return mutationTarget{}, err
	}
	root, err := filepath.Abs(value(task.config.TaskRoot))
	if err != nil {
		return mutationTarget{}, err
	}
	directory, err := filepath.Abs(task.directory)
	if err != nil {
		return mutationTarget{}, err
	}
	if !samePath(filepath.Dir(directory), root) || filepath.Base(directory) != task.manifest.TaskDirectoryName {
		return mutationTarget{}, fmt.Errorf("任务目录与配置或任务清单不匹配：%s", directory)
	}
	target, err := filepath.Abs(service.WorktreePath)
	if err != nil {
		return mutationTarget{}, err
	}
	if !samePath(filepath.Dir(target), directory) {
		return mutationTarget{}, fmt.Errorf("工作区路径不在任务目录的直接子级，已拒绝操作：%s", target)
	}
	if info, statErr := os.Stat(target); statErr != nil || !info.IsDir() {
		return mutationTarget{}, fmt.Errorf("任务工作区不存在：%s", target)
	}
	branch, err := s.currentBranch(ctx, target)
	if err != nil {
		return mutationTarget{}, err
	}
	if branch != service.Branch {
		return mutationTarget{}, fmt.Errorf("工作区当前分支与任务记录不一致：期望 %s，实际 %s", service.Branch, branch)
	}
	actualTop, err := s.gitTopLevel(ctx, target)
	if err != nil {
		return mutationTarget{}, err
	}
	if !samePath(actualTop, target) {
		return mutationTarget{}, fmt.Errorf("Git 顶层目录不匹配：%s", target)
	}
	if service.Strategy == domain.WorkspaceIndependentClone {
		gitMetadata, statErr := os.Stat(filepath.Join(target, ".git"))
		if statErr != nil || !gitMetadata.IsDir() {
			return mutationTarget{}, fmt.Errorf("独立克隆不是常规 Git checkout：%s", target)
		}
		if service.OriginURL == nil || strings.TrimSpace(*service.OriginURL) == "" {
			return mutationTarget{}, errors.New("独立克隆缺少任务记录的 origin URL")
		}
		if !configuredRepositoryExists(task.config, service.RepositoryID) {
			return mutationTarget{}, fmt.Errorf("仓库配置不存在：%s", service.RepositoryID)
		}
		actualOrigin, err := s.remoteURL(ctx, target, "origin")
		if err != nil {
			return mutationTarget{}, err
		}
		if strings.TrimSpace(actualOrigin) != strings.TrimSpace(*service.OriginURL) {
			return mutationTarget{}, errors.New("独立克隆 origin 与任务记录不匹配")
		}
		return mutationTarget{repository: target, worktree: target}, nil
	}
	configured, ok := repositoryByID(task.config, service.RepositoryID)
	if !ok {
		return mutationTarget{}, fmt.Errorf("仓库配置不存在：%s", service.RepositoryID)
	}
	repository, err := filepath.Abs(configured.RootPath)
	if err != nil {
		return mutationTarget{}, err
	}
	expectedCommon, err := filepath.Abs(configured.GitCommonDirectory)
	if err != nil {
		return mutationTarget{}, err
	}
	actualCommon, err := s.gitCommonDirectory(ctx, repository)
	if err != nil {
		return mutationTarget{}, err
	}
	if !samePath(actualCommon, expectedCommon) {
		return mutationTarget{}, errors.New("主仓库 Git 身份与配置不匹配")
	}
	worktreeCommon, err := s.gitCommonDirectory(ctx, target)
	if err != nil {
		return mutationTarget{}, err
	}
	if !samePath(worktreeCommon, expectedCommon) {
		return mutationTarget{}, errors.New("Linked Worktree 不属于配置的主仓库")
	}
	return mutationTarget{repository: repository, worktree: target}, nil
}

func configuredRepositoryExists(config domain.AppConfig, id string) bool {
	_, ok := repositoryByID(config, id)
	return ok
}
func repositoryByID(config domain.AppConfig, id string) (domain.RepositoryConfig, bool) {
	for _, repository := range config.Repositories {
		if repository.ID == id {
			return repository, true
		}
	}
	return domain.RepositoryConfig{}, false
}

func (s *Service) runTagBuild(ctx context.Context, task resolvedTask, target domain.EffectiveTagTarget, validated mutationTarget, operation *domain.TagOperation) error {
	preview, err := s.preflightUnlocked(ctx, task.manifest, target, validated)
	if err != nil {
		var conflict *mergeConflictError
		if errors.As(err, &conflict) {
			message := mergeConflictMessage(target.Workspace.ServiceName, target.SourceBranch, target.Remote+"/"+value(target.TargetBranch))
			if transitionErr := s.transition(ctx, task.directory, operation, domain.TagOperationConflict, nil, nil, nil, &message, conflict.files); transitionErr != nil {
				return s.finishBuildFailure(ctx, task.directory, operation, errors.Join(err, transitionErr))
			}
			return s.recordTagHistoryWithCleanup(ctx, task.directory, *operation)
		}
		return s.finishBuildFailure(ctx, task.directory, operation, err)
	}
	if err = s.requireTagBranchesAllowed(task.config, target, preview); err != nil {
		return s.finishBuildFailure(ctx, task.directory, operation, err)
	}
	if err = s.transition(ctx, task.directory, operation, domain.TagOperationPreflightPassed, &preview.SourceSHA, preview.TargetSHA, nil, nil, nil); err != nil {
		return s.finishBuildFailure(ctx, task.directory, operation, err)
	}
	if preview.SourceSync.PushRequired() || target.Mode == domain.TagBuildCurrentBranch {
		s.notifyTag("pushing-source", "推送源分支", "", operation)
		if _, err = s.run(ctx, validated.repository, 10*time.Minute, true, false, "push", "--set-upstream", target.Remote, target.SourceBranch+":refs/heads/"+target.SourceBranch); err != nil {
			return s.finishBuildFailure(ctx, task.directory, operation, err)
		}
	}
	if err = s.transition(ctx, task.directory, operation, domain.TagOperationSourceBranchPushed, nil, nil, nil, nil, nil); err != nil {
		return s.finishBuildFailure(ctx, task.directory, operation, err)
	}
	var tagCommit string
	if target.Mode == domain.TagBuildMergeToTargetBranch {
		mergeResult, mergeErr := s.mergeAndPushTargetBranch(ctx, validated.repository, preview.SourceSHA, target, target.Workspace.ServiceName)
		if mergeErr != nil {
			var conflict *mergeConflictError
			if errors.As(mergeErr, &conflict) {
				message := mergeConflictMessage(target.Workspace.ServiceName, target.SourceBranch, target.Remote+"/"+value(target.TargetBranch))
				if err := s.transition(ctx, task.directory, operation, domain.TagOperationConflict, nil, nil, nil, &message, conflict.files); err != nil {
					return s.finishBuildFailure(ctx, task.directory, operation, err)
				}
				return s.recordTagHistoryWithCleanup(ctx, task.directory, *operation)
			}
			return s.finishBuildFailure(ctx, task.directory, operation, mergeErr)
		}
		tagCommit = mergeResult.targetSHA
		if err = s.transition(ctx, task.directory, operation, domain.TagOperationTargetBranchPushed, nil, &tagCommit, nil, nil, nil); err != nil {
			return s.finishBuildFailureWithTargetSHA(ctx, task.directory, operation, err, &tagCommit)
		}
	} else {
		tagCommit = preview.SourceSHA
	}
	s.notifyTag("creating-tag", "创建测试Tag", "", operation)
	initial, err := s.nextTag(ctx, validated.repository, tagCommit)
	if err != nil {
		return s.finishBuildFailure(ctx, task.directory, operation, err)
	}
	message := s.auditMessage(task.manifest, target, preview.SourceSHA)
	pushedTag, err := s.createAndPushTag(ctx, validated.repository, target.Remote, initial, tagCommit, message, func(candidate string) error {
		return s.transition(ctx, task.directory, operation, operation.State, nil, nil, &candidate, nil, nil)
	}, func(candidate string) error {
		return s.transition(ctx, task.directory, operation, domain.TagOperationLocalTagCreated, nil, nil, &candidate, nil, nil)
	})
	if err != nil {
		return s.finishBuildFailure(ctx, task.directory, operation, err)
	}
	if err = s.transition(ctx, task.directory, operation, domain.TagOperationTagPushed, nil, nil, nil, nil, nil); err != nil {
		return s.finishBuildFailure(ctx, task.directory, operation, err)
	}
	successMessage := target.Workspace.ServiceName + "：" + pushedTag
	if err = s.transition(ctx, task.directory, operation, domain.TagOperationSuccess, nil, nil, nil, &successMessage, nil); err != nil {
		return s.finishBuildFailure(ctx, task.directory, operation, err)
	}
	return s.recordTagHistoryWithCleanup(ctx, task.directory, *operation)
}

func (s *Service) finishBuildFailure(ctx context.Context, taskDirectory string, operation *domain.TagOperation, cause error) error {
	return s.finishBuildFailureWithTargetSHA(ctx, taskDirectory, operation, cause, nil)
}

func (s *Service) finishBuildFailureWithTargetSHA(ctx context.Context, taskDirectory string, operation *domain.TagOperation, cause error, targetSHA *string) error {
	partial := operation.Tag != nil && oneOf(operation.State, domain.TagOperationSourceBranchPushed, domain.TagOperationTargetBranchPushed, domain.TagOperationLocalTagCreated)
	state := domain.TagOperationFailed
	if partial {
		state = domain.TagOperationPartial
	}
	return s.finishTagFailure(ctx, taskDirectory, operation, state, cause, targetSHA)
}

func (s *Service) finishTagFailure(ctx context.Context, taskDirectory string, operation *domain.TagOperation, state domain.TagOperationState, cause error, targetSHA *string) error {
	cleanupCtx, cancel := tagBuildCleanupContext(ctx)
	defer cancel()
	message := cause.Error()
	if err := s.transition(cleanupCtx, taskDirectory, operation, state, nil, targetSHA, nil, &message, nil); err != nil {
		return errors.Join(cause, fmt.Errorf("保存 Tag 操作失败收尾（%s）：%w", operation.OperationID, err))
	}
	s.notifyTag("failed", message, tagFailureKind(cause), operation)
	if err := s.recordTagHistory(cleanupCtx, taskDirectory, *operation); err != nil {
		return errors.Join(cause, fmt.Errorf("写入 Tag 操作历史（%s）：%w", operation.OperationID, err))
	}
	return nil
}

func (s *Service) recordTagHistoryWithCleanup(ctx context.Context, taskDirectory string, operation domain.TagOperation) error {
	cleanupCtx, cancel := tagBuildCleanupContext(ctx)
	defer cancel()
	if err := s.recordTagHistory(cleanupCtx, taskDirectory, operation); err != nil {
		return fmt.Errorf("写入 Tag 操作历史（%s）：%w", operation.OperationID, err)
	}
	return nil
}

func tagBuildCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
}

func (s *Service) transition(ctx context.Context, taskDirectory string, operation *domain.TagOperation, state domain.TagOperationState, sourceSHA, targetSHA, tag, message *string, conflicts []string) error {
	candidate := *operation
	candidate.State = state
	candidate.UpdatedAt = formatSilverwingTime(time.Now())
	if sourceSHA != nil {
		candidate.SourceSHA = sourceSHA
	}
	if targetSHA != nil {
		candidate.TargetSHA = targetSHA
	}
	if tag != nil {
		candidate.Tag = tag
	}
	if message != nil {
		candidate.Message = message
	}
	if conflicts != nil {
		candidate.ConflictFiles = conflicts
	}
	var err error
	candidate, err = s.store.saveTransition(ctx, taskDirectory, candidate)
	if err != nil {
		return err
	}
	*operation = candidate
	phase, failure := "", ""
	if state == domain.TagOperationConflict {
		phase, failure = "conflict", "conflict"
	}
	if state == domain.TagOperationSuccess {
		phase = "completed"
	}
	s.notifyTag(phase, stateLabel(state), failure, operation)
	return nil
}

func (s *Service) preflightUnlocked(ctx context.Context, manifest domain.TaskManifest, target domain.EffectiveTagTarget, validated mutationTarget) (TagPreflight, error) {
	if err := s.ensureCleanFeatureWorktree(ctx, validated.worktree); err != nil {
		return TagPreflight{}, err
	}
	if err := s.fetch(ctx, validated.repository, target.Remote); err != nil {
		return TagPreflight{}, err
	}
	if err := s.fetchTags(ctx, validated.repository, target.Remote); err != nil {
		return TagPreflight{}, err
	}
	sourceSHA, err := s.resolve(ctx, validated.repository, target.Workspace.Branch)
	if err != nil {
		return TagPreflight{}, err
	}
	remoteSourceRef := target.Remote + "/" + target.Workspace.Branch
	sync, err := s.featureSync(ctx, validated.repository, sourceSHA, remoteSourceRef)
	if err != nil {
		return TagPreflight{}, err
	}
	if !sync.CanPush() {
		return TagPreflight{}, fmt.Errorf("远端当前分支领先本地 %d 个提交，请先手工同步；工具不会自动 pull/rebase", sync.Behind)
	}
	preview := TagPreflight{
		FolderName: manifest.FolderName, ServiceName: target.Workspace.ServiceName, SourceBranch: target.Workspace.Branch,
		SourceSHA: sourceSHA, Remote: target.Remote, TargetBranch: target.TargetBranch,
		SourceSync: sync, TagMode: target.Mode, CommitList: []string{},
	}
	if target.Mode == domain.TagBuildCurrentBranch {
		rangeRef := sourceSHA
		if sync.RemoteExists {
			rangeRef = remoteSourceRef + ".." + sourceSHA
		}
		commits, err := s.run(ctx, validated.repository, 10*time.Minute, true, true, "log", "--format=%h %s", "--no-merges", rangeRef)
		if err != nil {
			return TagPreflight{}, err
		}
		preview.CommitList = nonEmptyLines(commits.Stdout)
		preview.EstimatedTag, err = s.nextTag(ctx, validated.repository, sourceSHA)
		return preview, err
	}
	if target.TargetBranch == nil {
		return TagPreflight{}, errors.New("合并到目标分支模式缺少目标分支")
	}
	targetRef := target.Remote + "/" + *target.TargetBranch
	targetSHA, err := s.resolve(ctx, validated.repository, targetRef)
	if err != nil {
		return TagPreflight{}, err
	}
	preview.TargetSHA = &targetSHA
	var mergeMode string
	if ancestor, err := s.isAncestor(ctx, validated.repository, sourceSHA, targetSHA); err != nil {
		return TagPreflight{}, err
	} else if ancestor {
		mergeMode = "ALREADY_MERGED"
	} else if ancestor, err = s.isAncestor(ctx, validated.repository, targetSHA, sourceSHA); err != nil {
		return TagPreflight{}, err
	} else if ancestor {
		mergeMode = "FAST_FORWARD"
	} else {
		mergeMode = "MERGE_COMMIT"
	}
	preview.MergeMode = &mergeMode
	if err = s.verifyMergeInTemporaryWorktree(ctx, validated.repository, targetSHA, sourceSHA, target.Workspace.ServiceName); err != nil {
		return TagPreflight{}, err
	}
	commits, err := s.run(ctx, validated.repository, 10*time.Minute, true, true, "log", "--format=%h %s", "--no-merges", targetSHA+".."+sourceSHA)
	if err != nil {
		return TagPreflight{}, err
	}
	preview.CommitList = nonEmptyLines(commits.Stdout)
	diff, err := s.run(ctx, validated.repository, 10*time.Minute, true, true, "diff", "--stat", targetSHA, sourceSHA)
	if err != nil {
		return TagPreflight{}, err
	}
	preview.DiffStat = strings.TrimSpace(diff.Stdout)
	preview.EstimatedTag, err = s.nextTag(ctx, validated.repository, targetSHA)
	return preview, err
}

func (s *Service) requireTagBranchesAllowed(config domain.AppConfig, target domain.EffectiveTagTarget, preview TagPreflight) error {
	policy := domain.NewGitWritePolicy(config.BlockedGitWriteBranches)
	if preview.SourceSync.PushRequired() || target.Mode == domain.TagBuildCurrentBranch {
		if err := policy.RequireAllowed(preview.SourceBranch, "测试Tag流程推送源分支"); err != nil {
			return err
		}
	}
	if target.Mode == domain.TagBuildMergeToTargetBranch && (preview.MergeMode == nil || *preview.MergeMode != "ALREADY_MERGED") {
		return policy.RequireAllowed(value(target.TargetBranch), "测试Tag流程合并并推送目标分支")
	}
	return nil
}

func (s *Service) ensureCleanFeatureWorktree(ctx context.Context, worktreePath string) error {
	status, err := s.run(ctx, worktreePath, 10*time.Minute, true, true, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return err
	}
	if strings.TrimSpace(status.Stdout) != "" {
		return &UncommittedChangesError{Changes: tagWorkspaceChanges(status.Stdout)}
	}
	gitDir, err := s.run(ctx, worktreePath, 2*time.Minute, true, true, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return err
	}
	for _, item := range []struct{ name, kind string }{{"MERGE_HEAD", "merge"}, {"rebase-merge", "rebase"}, {"rebase-apply", "rebase"}, {"CHERRY_PICK_HEAD", "cherry-pick"}, {"REVERT_HEAD", "revert"}, {"BISECT_LOG", "bisect"}, {"BISECT_START", "bisect"}} {
		if _, err := os.Stat(filepath.Join(strings.TrimSpace(gitDir.Stdout), item.name)); err == nil {
			return fmt.Errorf("特性工作区正在执行 %s", item.kind)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (s *Service) featureSync(ctx context.Context, repository, sourceSHA, remoteSource string) (FeatureSyncStatus, error) {
	result, err := s.run(ctx, repository, 10*time.Minute, false, true, "rev-parse", "--verify", remoteSource+"^{commit}")
	if err != nil {
		return FeatureSyncStatus{}, err
	}
	if result.ExitCode != 0 {
		return FeatureSyncStatus{}, nil
	}
	ahead, err := s.run(ctx, repository, 10*time.Minute, true, true, "rev-list", "--count", remoteSource+".."+sourceSHA)
	if err != nil {
		return FeatureSyncStatus{}, err
	}
	behind, err := s.run(ctx, repository, 10*time.Minute, true, true, "rev-list", "--count", sourceSHA+".."+remoteSource)
	if err != nil {
		return FeatureSyncStatus{}, err
	}
	a, err := strconv.Atoi(strings.TrimSpace(ahead.Stdout))
	if err != nil {
		return FeatureSyncStatus{}, err
	}
	b, err := strconv.Atoi(strings.TrimSpace(behind.Stdout))
	if err != nil {
		return FeatureSyncStatus{}, err
	}
	return FeatureSyncStatus{RemoteExists: true, Ahead: a, Behind: b}, nil
}

type mergeConflictError struct{ files []string }

func (e *mergeConflictError) Error() string {
	return "合并存在冲突：" + strings.Join(e.files, ", ")
}

type mergePushResult struct{ targetSHA string }
type genbuCandidate struct {
	directory string
	operation domain.TagOperation
	service   string
}

func (s *Service) mergeAndPushTargetBranch(ctx context.Context, repository, sourceSHA string, target domain.EffectiveTagTarget, serviceName string) (mergePushResult, error) {
	branch := value(target.TargetBranch)
	for attempt := 0; attempt < 2; attempt++ {
		if err := s.fetch(ctx, repository, target.Remote); err != nil {
			return mergePushResult{}, err
		}
		if err := s.fetchTags(ctx, repository, target.Remote); err != nil {
			return mergePushResult{}, err
		}
		remoteRef := target.Remote + "/" + branch
		remoteTargetSHA, err := s.resolve(ctx, repository, remoteRef)
		if err != nil {
			return mergePushResult{}, err
		}
		merged, err := s.isAncestor(ctx, repository, sourceSHA, remoteTargetSHA)
		if err != nil {
			return mergePushResult{}, err
		}
		if merged {
			return mergePushResult{targetSHA: remoteTargetSHA}, nil
		}
		temporary := s.temporaryWorktreePath(repository, serviceName+"-build-"+strconv.Itoa(attempt+1))
		if err := s.addDetachedWorktree(ctx, repository, temporary, remoteTargetSHA); err != nil {
			return mergePushResult{}, err
		}
		cleanup := func() {
			_, _ = s.run(context.Background(), temporary, 2*time.Minute, false, false, "merge", "--abort")
			_ = s.removeWorktree(context.Background(), repository, temporary, true)
			_, _ = s.run(context.Background(), repository, 2*time.Minute, false, false, "worktree", "prune")
		}
		s.notifyTag("merging", "合并目标分支", "", nil)
		merge, runErr := s.run(ctx, temporary, 10*time.Minute, false, false, "merge", "--no-edit", sourceSHA)
		if runErr != nil {
			cleanup()
			return mergePushResult{}, runErr
		}
		if merge.ExitCode != 0 {
			files, _ := s.conflictFiles(ctx, temporary)
			_, _ = s.run(context.Background(), temporary, 2*time.Minute, false, false, "merge", "--abort")
			cleanup()
			if len(files) > 0 {
				return mergePushResult{}, &mergeConflictError{files: files}
			}
			return mergePushResult{}, gitFailure("合并目标分支失败", merge)
		}
		mergedSHA, err := s.resolve(ctx, temporary, "HEAD")
		if err != nil {
			cleanup()
			return mergePushResult{}, err
		}
		s.notifyTag("pushing-target", "推送目标分支", "", nil)
		push, runErr := s.run(ctx, temporary, 10*time.Minute, false, false, "push", target.Remote, "HEAD:refs/heads/"+branch)
		if runErr != nil {
			cleanup()
			return mergePushResult{}, runErr
		}
		if push.ExitCode == 0 {
			cleanup()
			if err := s.fetch(ctx, repository, target.Remote); err != nil {
				return mergePushResult{}, err
			}
			if err := s.fetchTags(ctx, repository, target.Remote); err != nil {
				return mergePushResult{}, err
			}
			verified, err := s.resolve(ctx, repository, remoteRef)
			if err != nil {
				return mergePushResult{}, err
			}
			if verified != mergedSHA {
				return mergePushResult{}, fmt.Errorf("推送后远端 %s 的 SHA 校验失败", branch)
			}
			return mergePushResult{targetSHA: mergedSHA}, nil
		}
		cleanup()
		if attempt == 1 {
			return mergePushResult{}, gitFailure("目标分支在推送期间被更新，重试后仍失败", push)
		}
	}
	return mergePushResult{}, errors.New("无法更新目标分支")
}

func (s *Service) verifyMergeInTemporaryWorktree(ctx context.Context, repository, targetSHA, sourceSHA, serviceName string) error {
	alreadyMerged, err := s.isAncestor(ctx, repository, sourceSHA, targetSHA)
	if err != nil || alreadyMerged {
		return err
	}
	temporary := s.temporaryWorktreePath(repository, serviceName+"-preflight")
	if err := s.addDetachedWorktree(ctx, repository, temporary, targetSHA); err != nil {
		return err
	}
	defer func() {
		_, _ = s.run(context.Background(), temporary, 2*time.Minute, false, false, "merge", "--abort")
		_ = s.removeWorktree(context.Background(), repository, temporary, true)
		_, _ = s.run(context.Background(), repository, 2*time.Minute, false, false, "worktree", "prune")
	}()
	merge, runErr := s.run(ctx, temporary, 10*time.Minute, false, false, "merge", "--no-commit", "--no-ff", sourceSHA)
	if runErr != nil {
		return runErr
	}
	files, err := s.conflictFiles(ctx, temporary)
	if err != nil {
		return err
	}
	if len(files) > 0 {
		return &mergeConflictError{files: files}
	}
	if merge.ExitCode != 0 {
		return gitFailure("合并预检失败", merge)
	}
	return nil
}

func (s *Service) conflictFiles(ctx context.Context, worktree string) ([]string, error) {
	result, err := s.run(ctx, worktree, 2*time.Minute, false, true, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return nil, err
	}
	if result.ExitCode != 0 {
		return nil, nil
	}
	return nonEmptyLines(result.Stdout), nil
}

func (s *Service) nextTag(ctx context.Context, repository, commit string) (string, error) {
	result, err := s.run(ctx, repository, 10*time.Minute, true, true, "for-each-ref", "--merged", commit, "--format=%(creatordate:unix) %(refname:short)", "refs/tags")
	if err != nil {
		return "", err
	}
	latest := domain.LatestTagVersion(versionTagsFromOutput(result.Stdout))
	if latest == "" {
		return "", errors.New("仓库没有可用的历史测试Tag，无法计算下一版本；请先在仓库创建并推送一个符合版本规则的测试Tag")
	}
	return domain.NextTagVersion(latest)
}

func (s *Service) createAndPushTag(ctx context.Context, repository, remote, initial, commit, message string, candidate, created func(string) error) (string, error) {
	tag := initial
	for attempt := 0; attempt < 2; attempt++ {
		if err := candidate(tag); err != nil {
			return "", err
		}
		if err := s.createOrValidateLocalTag(ctx, repository, tag, commit, message); err != nil {
			var collision *TagCollisionError
			if errors.As(err, &collision) && attempt == 0 {
				tag, err = domain.NextTagVersion(tag)
				if err == nil {
					continue
				}
			}
			return "", err
		}
		if err := created(tag); err != nil {
			return "", err
		}
		s.notifyTag("pushing-tag", "推送测试Tag", "", nil)
		if err := s.pushTag(ctx, repository, remote, tag, commit); err != nil {
			var collision *TagCollisionError
			if errors.As(err, &collision) && attempt == 0 {
				tag, err = domain.NextTagVersion(tag)
				if err == nil {
					continue
				}
			}
			return "", err
		}
		return tag, nil
	}
	return "", errors.New("测试Tag推送未完成")
}

type TagCollisionError struct{ Tag, ExistingSHA, ExpectedSHA, Location string }

func (e *TagCollisionError) Error() string {
	return fmt.Sprintf("%s测试Tag %s 已指向 %s，预期为 %s", e.Location, e.Tag, e.ExistingSHA, e.ExpectedSHA)
}

func (s *Service) createOrValidateLocalTag(ctx context.Context, repository, tag, commit, message string) error {
	result, err := s.run(ctx, repository, 2*time.Minute, false, false, "rev-parse", "--verify", "refs/tags/"+tag+"^{commit}")
	if err != nil {
		return err
	}
	if result.ExitCode == 0 {
		if strings.TrimSpace(result.Stdout) != commit {
			return &TagCollisionError{tag, strings.TrimSpace(result.Stdout), commit, "本地"}
		}
		return nil
	}
	_, err = s.run(ctx, repository, 2*time.Minute, true, false, "tag", "-a", tag, commit, "-m", message)
	return err
}

func (s *Service) pushTag(ctx context.Context, repository, remote, tag, commit string) error {
	before, err := s.remoteTagSHA(ctx, repository, remote, tag)
	if err != nil {
		return err
	}
	if before != "" {
		if before != commit {
			return &TagCollisionError{tag, before, commit, "远端"}
		}
		return nil
	}
	push, err := s.run(ctx, repository, 10*time.Minute, false, false, "push", remote, "refs/tags/"+tag+":refs/tags/"+tag)
	if err != nil {
		return err
	}
	if push.ExitCode == 0 {
		return nil
	}
	after, queryErr := s.remoteTagSHA(ctx, repository, remote, tag)
	if queryErr != nil {
		return errors.Join(gitFailure("推送测试Tag失败", push), queryErr)
	}
	if after != "" && after != commit {
		return &TagCollisionError{tag, after, commit, "远端"}
	}
	if after == commit {
		return nil
	}
	return gitFailure("推送测试Tag失败", push)
}

func (s *Service) remoteTagSHA(ctx context.Context, repository, remote, tag string) (string, error) {
	peeled, err := s.run(ctx, repository, 2*time.Minute, false, true, "ls-remote", remote, "refs/tags/"+tag+"^{}")
	if err != nil {
		return "", err
	}
	if value := remoteOutputSHA(peeled.Stdout); value != "" {
		return value, nil
	}
	direct, err := s.run(ctx, repository, 2*time.Minute, false, true, "ls-remote", remote, "refs/tags/"+tag)
	if err != nil {
		return "", err
	}
	return remoteOutputSHA(direct.Stdout), nil
}

func remoteOutputSHA(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return strings.TrimSpace(strings.SplitN(line, "\t", 2)[0])
		}
	}
	return ""
}

func (s *Service) auditMessage(manifest domain.TaskManifest, target domain.EffectiveTagTarget, sourceSHA string) string {
	_ = sourceSHA
	var lines = []string{target.TagMessagePrefix + " build", "Task: " + manifest.FolderName}
	if link := strings.TrimSpace(manifest.RequirementLink); link != "" {
		lines = append(lines, "需求链接："+link)
	}
	user := os.Getenv("USERNAME")
	if user == "" {
		user = os.Getenv("USER")
	}
	lines = append(lines, "Builder: "+user, "时间："+formatSilverwingTime(time.Now()))
	return strings.Join(lines, "\n")
}

func (s *Service) temporaryWorktreePath(repository, label string) string {
	common, err := s.git.CommonDirectory(repository)
	if err != nil {
		common = repository
	}
	abs, err := filepath.Abs(common)
	if err != nil {
		abs = filepath.Clean(common)
	}
	if runtime.GOOS == "windows" {
		abs = strings.ToLower(abs)
	}
	digest := sha256.Sum256([]byte(filepath.Clean(abs)))
	safe := sanitizePathLabel(label)
	parent := filepath.Join(s.paths.Temp, "tag-build", fmt.Sprintf("%x", digest[:12]))
	_ = os.MkdirAll(parent, 0o755)
	return filepath.Join(parent, safe+"-"+newUUID())
}

func sanitizePathLabel(value string) string {
	var out strings.Builder
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-", r) {
			out.WriteRune(r)
		} else {
			out.WriteByte('-')
		}
		if out.Len() >= 50 {
			break
		}
	}
	if out.Len() == 0 {
		return "tag-build"
	}
	return out.String()
}

func (s *Service) run(ctx context.Context, repository string, timeout time.Duration, check, readOnly bool, args ...string) (process.Result, error) {
	result, err := s.git.RunContext(ctx, repository, timeout, check, readOnly, args...)
	if err != nil {
		return result, err
	}
	if check && result.ExitCode != 0 {
		return result, gitFailure("Git 命令失败", result)
	}
	return result, nil
}

func (s *Service) currentBranch(ctx context.Context, repository string) (string, error) {
	result, err := s.run(ctx, repository, 2*time.Minute, true, true, "branch", "--show-current")
	return strings.TrimSpace(result.Stdout), err
}
func (s *Service) gitTopLevel(ctx context.Context, repository string) (string, error) {
	result, err := s.run(ctx, repository, 2*time.Minute, true, true, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return filepath.Abs(strings.TrimSpace(result.Stdout))
}
func (s *Service) gitCommonDirectory(ctx context.Context, repository string) (string, error) {
	result, err := s.run(ctx, repository, 2*time.Minute, true, true, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return filepath.Abs(strings.TrimSpace(result.Stdout))
}
func (s *Service) withRepositoryLock(ctx context.Context, repository string, operation func() error) error {
	common, err := s.gitCommonDirectory(ctx, repository)
	if err != nil {
		return err
	}
	return s.locks.WithRepository(common, operation)
}

func (s *Service) remoteURL(ctx context.Context, repository, remote string) (string, error) {
	result, err := s.run(ctx, repository, 2*time.Minute, false, true, "remote", "get-url", remote)
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", nil
	}
	return strings.TrimSpace(result.Stdout), nil
}
func (s *Service) resolve(ctx context.Context, repository, ref string) (string, error) {
	result, err := s.run(ctx, repository, 2*time.Minute, true, true, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}
func (s *Service) isAncestor(ctx context.Context, repository, ancestor, descendant string) (bool, error) {
	result, err := s.run(ctx, repository, 10*time.Minute, false, true, "merge-base", "--is-ancestor", ancestor, descendant)
	if err != nil {
		return false, err
	}
	if result.ExitCode == 0 {
		return true, nil
	}
	if result.ExitCode == 1 {
		return false, nil
	}
	return false, gitFailure("Git 祖先关系检查失败", result)
}
func (s *Service) fetch(ctx context.Context, repository, remote string) error {
	if remote == "" {
		remote = "origin"
	}
	_, err := s.run(ctx, repository, 5*time.Minute, true, false, "fetch", "--prune", "--no-tags", remote)
	return err
}
func (s *Service) fetchTags(ctx context.Context, repository, remote string) error {
	if remote == "" {
		remote = "origin"
	}
	_, err := s.run(ctx, repository, 5*time.Minute, true, false, "fetch", "--tags", "--prune", "--prune-tags", "--force", remote)
	return err
}
func (s *Service) addDetachedWorktree(ctx context.Context, repository, target, ref string) error {
	_, err := s.run(ctx, repository, 5*time.Minute, true, false, "-c", "core.symlinks=false", "worktree", "add", "--detach", target, ref)
	return err
}
func (s *Service) removeWorktree(ctx context.Context, repository, target string, force bool) error {
	args := []string{"worktree", "remove"}
	if force {
		args = append(args, "--force")
	}
	args = append(args, target)
	_, err := s.run(ctx, repository, 5*time.Minute, true, false, args...)
	return err
}

func (s *Service) Preflight(ctx context.Context, taskFolder, selection string) (TagPreflight, error) {
	task, err := s.resolveTask(taskFolder)
	if err != nil {
		return TagPreflight{}, err
	}
	var preview TagPreflight
	err = s.withTaskLock(task.directory, func() error {
		target, err := domain.ResolveEffectiveTagTarget(task.config, task.manifest, selection)
		if err != nil {
			return err
		}
		validated, err := s.validateForMutation(ctx, task, target.Workspace)
		if err != nil {
			return err
		}
		return s.withRepositoryLock(ctx, validated.repository, func() error {
			var inner error
			preview, inner = s.preflightUnlocked(ctx, task.manifest, target, validated)
			if inner != nil {
				return inner
			}
			return s.requireTagBranchesAllowed(task.config, target, preview)
		})
	})
	return preview, err
}

func (s *Service) Status(ctx context.Context, taskFolder, operationID string) (TagOperationReport, error) {
	task, err := s.resolveTask(taskFolder)
	if err != nil {
		return TagOperationReport{}, err
	}
	if !task.config.TagEnabled {
		return TagOperationReport{}, errors.New("全局测试Tag已关闭")
	}
	operation, err := s.probeOperation(ctx, task, operationID)
	if err != nil {
		return TagOperationReport{}, err
	}
	return operationReport(operation, task.manifest.TaskDirectoryName), nil
}

func (s *Service) History(ctx context.Context, taskFolder string) ([]TagOperationReport, error) {
	task, err := s.resolveTask(taskFolder)
	if err != nil {
		return nil, err
	}
	operations, err := s.store.list(task.directory)
	if err != nil {
		return nil, err
	}
	sort.SliceStable(operations, func(i, j int) bool {
		if operations[i].UpdatedAt != operations[j].UpdatedAt {
			return operations[i].UpdatedAt > operations[j].UpdatedAt
		}
		return operations[i].OperationID > operations[j].OperationID
	})
	reports := make([]TagOperationReport, len(operations))
	for i, operation := range operations {
		reports[i] = operationReport(operation, task.manifest.TaskDirectoryName)
	}
	return reports, nil
}

func (s *Service) Retry(ctx context.Context, taskFolder, operationID string) (TagOperationReport, error) {
	task, err := s.resolveTask(taskFolder)
	if err != nil {
		return TagOperationReport{}, err
	}
	if !task.config.TagEnabled {
		return TagOperationReport{}, errors.New("全局测试Tag已关闭")
	}
	return s.retryTask(ctx, task, operationID)
}

func (s *Service) retryTask(ctx context.Context, task resolvedTask, operationID string) (TagOperationReport, error) {
	// Desktop retry validates saved state, as TagBuildService.retag does in
	// the source. Genbu probing belongs to the explicit refresh actions.
	operation, err := s.store.load(task.directory, operationID)
	if err != nil {
		return TagOperationReport{}, err
	}
	var updated domain.TagOperation
	err = s.withTaskLock(task.directory, func() error {
		var inner error
		switch retryKind(operation) {
		case TagRetryResolveConflict:
			updated, inner = s.resumeExisting(ctx, task, operationID, func(current domain.TagOperation) error {
				if current.State != domain.TagOperationConflict {
					return errors.New("只有冲突的测试Tag操作可以重试")
				}
				return nil
			})
		case TagRetryInterrupted:
			updated, inner = s.resumeExisting(ctx, task, operationID, func(current domain.TagOperation) error {
				if !oneOf(current.State, domain.TagOperationCreated, domain.TagOperationPreflightPassed, domain.TagOperationSourceBranchPushed) {
					return errors.New("只有构建中断的测试Tag操作可以重试")
				}
				return nil
			})
		case TagRetryBuild:
			updated, inner = s.resumeExisting(ctx, task, operationID, func(current domain.TagOperation) error {
				if current.State != domain.TagOperationFailed {
					return errors.New("只有失败的测试Tag操作可以重试")
				}
				return nil
			})
		case TagRetryPartial:
			updated, inner = s.resumePartial(ctx, task, operationID)
		case TagRetryRetag:
			updated, inner = s.resumeExisting(ctx, task, operationID, func(current domain.TagOperation) error {
				if current.State != domain.TagOperationSuccess || current.GenbuStatus.Build != domain.GenbuFailed {
					return errors.New("只有 Genbu 构建失败的测试Tag可以重新打Tag")
				}
				if current.GenbuStatus.FailureReason != nil {
					return errors.New("Genbu 实时状态查询失败，请刷新状态后再判断是否重新打Tag")
				}
				return nil
			})
		default:
			if guidance := operationGuidance(operation, task.manifest.TaskDirectoryName); guidance != nil {
				return errors.New(*guidance)
			}
			return fmt.Errorf("当前状态（%s）的测试Tag无需重试", stateLabel(operation.State))
		}
		return inner
	})
	if err != nil {
		return TagOperationReport{}, err
	}
	return operationReport(updated, task.manifest.TaskDirectoryName), nil
}

func (s *Service) resumeExisting(ctx context.Context, task resolvedTask, operationID string, validate func(domain.TagOperation) error) (domain.TagOperation, error) {
	previous, err := s.store.load(task.directory, operationID)
	if err != nil {
		return domain.TagOperation{}, err
	}
	if err = validate(previous); err != nil {
		return domain.TagOperation{}, err
	}
	updated, err := s.buildUnlocked(ctx, task, previous.GroupServiceID+":"+previous.ModuleID, &previous, "")
	if err == nil {
		return updated, nil
	}
	failed := previous
	failed.State, failed.UpdatedAt = domain.TagOperationFailed, formatSilverwingTime(time.Now())
	failed.SourceSHA, failed.TargetSHA, failed.Tag = nil, nil, nil
	failed.Message, failed.ConflictFiles, failed.GenbuStatus = stringPointer(err.Error()), []string{}, initialGenbuStatus()
	if saveErr := s.store.save(ctx, task.directory, failed); saveErr != nil {
		return domain.TagOperation{}, errors.Join(err, saveErr)
	}
	if historyErr := s.recordTagHistory(ctx, task.directory, failed); historyErr != nil {
		return domain.TagOperation{}, errors.Join(err, historyErr)
	}
	return failed, nil
}

func (s *Service) resumePartial(ctx context.Context, task resolvedTask, operationID string) (domain.TagOperation, error) {
	operation, err := s.store.load(task.directory, operationID)
	if err != nil {
		return domain.TagOperation{}, err
	}
	if operation.State != domain.TagOperationPartial {
		return domain.TagOperation{}, fmt.Errorf("只有部分失败的测试Tag操作可以恢复")
	}
	target, err := domain.ResolveHistoricalTagTarget(task.config, task.manifest, operation)
	if err != nil {
		return domain.TagOperation{}, err
	}
	validated, err := s.validateForMutation(ctx, task, target.Workspace)
	if err != nil {
		return domain.TagOperation{}, err
	}
	tag := value(operation.Tag)
	if tag == "" {
		return domain.TagOperation{}, errors.New("操作没有可恢复的本地测试Tag")
	}
	tagCommit := value(operation.TargetSHA)
	if tagCommit == "" {
		tagCommit = value(operation.SourceSHA)
	}
	if tagCommit == "" {
		return domain.TagOperation{}, errors.New("操作没有可恢复的测试Tag提交")
	}
	err = s.withRepositoryLock(ctx, validated.repository, func() error {
		pushed, pushErr := s.createAndPushTag(ctx, validated.repository, target.Remote, tag, tagCommit,
			s.auditMessage(task.manifest, target, value(operation.SourceSHA)),
			func(candidate string) error {
				return s.transition(ctx, task.directory, &operation, operation.State, nil, nil, &candidate, nil, nil)
			},
			func(candidate string) error {
				return s.transition(ctx, task.directory, &operation, domain.TagOperationLocalTagCreated, nil, nil, &candidate, nil, nil)
			},
		)
		if pushErr != nil {
			return s.finishTagFailure(ctx, task.directory, &operation, domain.TagOperationPartial, pushErr, nil)
		}
		if err := s.transition(ctx, task.directory, &operation, domain.TagOperationTagPushed, nil, nil, nil, nil, nil); err != nil {
			return s.finishTagFailure(ctx, task.directory, &operation, domain.TagOperationPartial, err, nil)
		}
		message := target.Workspace.ServiceName + "：" + pushed
		if err := s.transition(ctx, task.directory, &operation, domain.TagOperationSuccess, nil, nil, nil, &message, nil); err != nil {
			return s.finishTagFailure(ctx, task.directory, &operation, domain.TagOperationPartial, err, nil)
		}
		return s.recordTagHistoryWithCleanup(ctx, task.directory, operation)
	})
	return operation, err
}

func (s *Service) WorkspaceCheck(ctx context.Context, taskFolder, operationID string) (TagWorkspaceCheckReport, error) {
	task, err := s.resolveTask(taskFolder)
	if err != nil {
		return TagWorkspaceCheckReport{}, err
	}
	if !task.config.TagEnabled {
		return TagWorkspaceCheckReport{}, errors.New("全局测试Tag已关闭")
	}
	return s.workspaceCheckTask(ctx, task, operationID)
}

func (s *Service) workspaceCheckTask(ctx context.Context, task resolvedTask, operationID string) (TagWorkspaceCheckReport, error) {
	var report TagWorkspaceCheckReport
	err := s.withTaskLock(task.directory, func() error {
		operation, err := s.store.load(task.directory, operationID)
		if err != nil {
			return err
		}
		if operation.State != domain.TagOperationConflict && operation.State != domain.TagOperationFailed {
			return fmt.Errorf("只有冲突或失败的测试Tag需要检测工作区（当前：%s）", stateLabel(operation.State))
		}
		target, err := domain.ResolveEffectiveTagTarget(task.config, task.manifest, operation.GroupServiceID+":"+operation.ModuleID)
		if err != nil {
			return err
		}
		validated, err := s.validateForMutation(ctx, task, target.Workspace)
		if err != nil {
			return err
		}
		result, err := s.run(ctx, validated.worktree, 2*time.Minute, true, true, "status", "--porcelain=v1", "--untracked-files=all")
		if err != nil {
			return err
		}
		changes := tagWorkspaceChanges(result.Stdout)
		report = TagWorkspaceCheckReport{Clean: len(changes) == 0, Changes: changes}
		return nil
	})
	return report, err
}

func tagWorkspaceChanges(statusOutput string) []string {
	changes := make([]string, 0)
	for _, line := range strings.Split(statusOutput, "\n") {
		line = strings.TrimSuffix(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		code, path := line, ""
		if len(line) >= 2 {
			code = line[:2]
		}
		if len(line) > 2 {
			path = strings.TrimSpace(strings.TrimPrefix(line[2:], " "))
		}
		if path == "" {
			path = "未命名路径"
		}
		kind := "状态变更"
		switch {
		case code == "??":
			kind = "未跟踪"
		case strings.Contains(code, "U") || code == "AA" || code == "DD":
			kind = "未解决冲突"
		case len(code) >= 2 && code[0] != ' ' && code[1] != ' ':
			kind = "已暂存且有未暂存修改"
		case len(code) > 0 && code[0] != ' ':
			kind = "已暂存"
		case len(code) > 1 && code[1] != ' ':
			kind = "未暂存"
		}
		changes = append(changes, kind+"："+path)
	}
	return changes
}

func retryKind(operation domain.TagOperation) TagRetryKind {
	switch {
	case operation.State == domain.TagOperationConflict:
		return TagRetryResolveConflict
	case oneOf(operation.State, domain.TagOperationCreated, domain.TagOperationPreflightPassed, domain.TagOperationSourceBranchPushed):
		return TagRetryInterrupted
	case operation.State == domain.TagOperationFailed:
		return TagRetryBuild
	case operation.State == domain.TagOperationPartial:
		return TagRetryPartial
	case operation.State == domain.TagOperationSuccess && operation.GenbuStatus.Build == domain.GenbuFailed && operation.GenbuStatus.FailureReason == nil:
		return TagRetryRetag
	default:
		return TagRetryNone
	}
}

func operationReport(operation domain.TagOperation, taskFolder string) TagOperationReport {
	return TagOperationReport{Operation: normalizeTagOperation(operation), RetryKind: retryKind(operation), Guidance: operationGuidance(operation, taskFolder)}
}

func operationGuidance(operation domain.TagOperation, taskFolder string) *string {
	retry := "在桌面端打开任务“" + taskFolder + "”的 Tag 历史，选择记录 " + operation.OperationID + " 并点击“重试”"
	var guidance string
	switch retryKind(operation) {
	case TagRetryResolveConflict:
		target := "目标分支"
		if operation.TargetBranch != nil {
			target = operation.Remote + "/" + *operation.TargetBranch
		}
		guidance = mergeConflictMessage(operation.ServiceName, operation.SourceBranch, target) + "\n解决冲突后提交并推送 `" + target + "`，再" + retry + "。"
	case TagRetryInterrupted:
		guidance = "上次Tag构建被中断，尚未合并目标分支或创建测试Tag；" + retry + "。"
	case TagRetryBuild:
		guidance = "Tag构建失败：" + value(operation.Message) + "；修复后，" + retry + "。"
	case TagRetryPartial:
		guidance = "测试Tag " + value(operation.Tag) + " 的创建或推送尚未完成；解决记录中的错误后，" + retry + "。"
	case TagRetryRetag:
		guidance = "Genbu 构建失败；" + retry + "，版本号将在 " + value(operation.Tag) + " 基础上自动 +1。"
	case TagRetryNone:
		if operation.GenbuStatus.FailureReason != nil {
			guidance = "Genbu 实时状态查询失败，暂不能确认是否需要重新打Tag；请重新查询状态后再重试"
		}
	}
	if guidance == "" {
		return nil
	}
	return &guidance
}

func mergeConflictMessage(service, source, target string) string {
	return fmt.Sprintf("我在将服务 %s 的 %s 分支合并到 %s 时遇到了冲突，请你解决。", service, source, target)
}

func stateLabel(state domain.TagOperationState) string {
	switch state {
	case domain.TagOperationCreated:
		return "已创建"
	case domain.TagOperationPreflightPassed:
		return "预检通过"
	case domain.TagOperationSourceBranchPushed:
		return "源分支已推送"
	case domain.TagOperationTargetBranchPushed:
		return "目标分支已推送"
	case domain.TagOperationLocalTagCreated:
		return "本地Tag已创建"
	case domain.TagOperationTagPushed:
		return "Tag已推送"
	case domain.TagOperationSuccess:
		return "成功"
	case domain.TagOperationConflict:
		return "冲突"
	case domain.TagOperationFailed:
		return "失败"
	case domain.TagOperationPartial:
		return "部分失败"
	default:
		return string(state)
	}
}

func initialGenbuStatus() domain.GenbuTagProbeStatus {
	return domain.GenbuTagProbeStatus{Build: domain.GenbuUnknown, UAT: domain.GenbuUnknown, Production: domain.GenbuUnknown}
}

func (s *Service) probeOperation(ctx context.Context, task resolvedTask, operationID string) (domain.TagOperation, error) {
	operation, err := s.store.load(task.directory, operationID)
	if err != nil {
		return domain.TagOperation{}, err
	}
	group, err := task.config.Group(task.manifest.GroupID)
	if err != nil {
		return operation, nil
	}
	var service *domain.GroupServiceConfig
	for i := range group.Services {
		if group.Services[i].ID == operation.GroupServiceID {
			service = &group.Services[i]
			break
		}
	}
	if service == nil || !service.GenbuProbeEnabled || !domain.TagGenbuProbeEligible(operation.State, value(operation.Tag)) {
		return operation, nil
	}
	queried, queryErr := s.queryGenbu(ctx, task.config, strings.TrimSpace(service.GenbuServiceName), value(operation.Tag))
	if err := ctx.Err(); err != nil {
		return operation, err
	}
	updated := operation
	updated.GenbuStatus.CheckedAt = stringPointer(formatSilverwingTime(time.Now()))
	if queryErr != nil {
		updated.GenbuStatus.NotFound = false
		updated.GenbuStatus.FailureReason = stringPointer(queryErr.Error())
	} else {
		updated.GenbuStatus.Build, updated.GenbuStatus.UAT, updated.GenbuStatus.Production = queried.Build, queried.UAT, queried.Production
		updated.GenbuStatus.NotFound = queried.NotFound
		updated.GenbuStatus.BuiltCompletedAt = queried.BuiltCompletedAt
		updated.GenbuStatus.ReleasedCompletedAt = queried.UATReleasedCompletedAt
		updated.GenbuStatus.ProductionReleasedCompletedAt = queried.ProductionReleasedCompletedAt
		updated.GenbuStatus.FailureReason = nil
	}
	current, _, err := s.store.updateIfUnchanged(ctx, task.directory, operation, func(domain.TagOperation) domain.TagOperation { return updated })
	if err != nil {
		return domain.TagOperation{}, err
	}
	if current.OperationID == "" {
		return domain.TagOperation{}, fmt.Errorf("找不到Tag构建记录：%s", operationID)
	}
	return current, nil
}

type genbuTagQueryResult struct {
	Build                         domain.GenbuStageStatus `json:"-"`
	UAT                           domain.GenbuStageStatus `json:"-"`
	Production                    domain.GenbuStageStatus `json:"-"`
	NotFound                      bool                    `json:"notFound"`
	BuiltCompletedAt              *string                 `json:"builtCompletedAt,omitempty"`
	UATReleasedCompletedAt        *string                 `json:"uatReleasedCompletedAt,omitempty"`
	ProductionReleasedCompletedAt *string                 `json:"productionReleasedCompletedAt,omitempty"`
}
type genbuTagQueryJSON struct {
	BuildStatus           string  `json:"build_status"`
	BuildCompletedAt      *string `json:"build_completed_at"`
	UATStatus             string  `json:"uat_status"`
	UATCompletedAt        *string `json:"uat_completed_at"`
	ProductionStatus      string  `json:"production_status"`
	ProductionCompletedAt *string `json:"production_completed_at"`
}

func (s *Service) queryGenbu(ctx context.Context, config domain.AppConfig, service, tag string) (genbuTagQueryResult, error) {
	if strings.TrimSpace(service) == "" {
		return genbuTagQueryResult{}, errors.New("Genbu 服务名不能为空")
	}
	if strings.TrimSpace(tag) == "" {
		return genbuTagQueryResult{}, errors.New("Tag 不能为空")
	}
	configured := ""
	if config.GenbuExecutablePath != nil {
		configured = *config.GenbuExecutablePath
	}
	spec, err := commandruntime.Prepare(ctx, s.runner, &config, domain.ProxyTargetGenbu, executable.ToolGenbu, configured)
	if err != nil {
		return genbuTagQueryResult{}, err
	}
	spec.Arguments, spec.Timeout = []string{"query-tag", "--json", service, tag}, 20*time.Second
	result, err := s.runner.Run(ctx, spec)
	if err != nil {
		return genbuTagQueryResult{}, fmt.Errorf("Genbu CLI 执行失败：%s", commandruntime.Redact(config, err.Error()))
	}
	if result.ExitCode != 0 {
		detail := strings.TrimSpace(result.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(result.Stdout)
		}
		if isGenbuTagNotFound(detail) {
			return genbuTagQueryResult{Build: domain.GenbuUnknown, UAT: domain.GenbuUnknown, Production: domain.GenbuUnknown, NotFound: true}, nil
		}
		if detail == "" {
			detail = fmt.Sprintf("genbu query-tag 执行失败（退出码 %d）", result.ExitCode)
		}
		return genbuTagQueryResult{}, errors.New(commandruntime.Redact(config, detail))
	}
	var parsed genbuTagQueryJSON
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &parsed); err != nil {
		return genbuTagQueryResult{}, errors.New("无法识别 Genbu Tag 查询输出")
	}
	return genbuTagQueryResult{
		Build: parseGenbuStage(parsed.BuildStatus), UAT: parseGenbuStage(parsed.UATStatus), Production: parseGenbuStage(parsed.ProductionStatus),
		BuiltCompletedAt: nonEmptyPointer(value(parsed.BuildCompletedAt)), UATReleasedCompletedAt: nonEmptyPointer(value(parsed.UATCompletedAt)), ProductionReleasedCompletedAt: nonEmptyPointer(value(parsed.ProductionCompletedAt)),
	}, nil
}

func parseGenbuStage(value string) domain.GenbuStageStatus {
	switch strings.TrimSpace(value) {
	case "初始":
		return domain.GenbuInitial
	case "构建中":
		return domain.GenbuBuilding
	case "成功":
		return domain.GenbuSuccess
	case "失败":
		return domain.GenbuFailed
	default:
		return domain.GenbuUnknown
	}
}
func isGenbuTagNotFound(detail string) bool {
	return strings.Contains(detail, "未在产线") || regexp.MustCompile(`(?i)未找到.*tag`).MatchString(detail)
}

func nonEmptyPointer(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

// Probe refreshes persisted status for all configured Tag operations. Terminal
// records stop background polling unless force is true; explicit Status always
// queries the selected operation regardless of its previous terminal state.
func (s *Service) Probe(ctx context.Context, config domain.AppConfig, tasks []domain.TaskManifest, force bool) (bool, error) {
	return s.ProbeWithProgress(ctx, config, tasks, force, nil, nil)
}

func refreshedGenbuStatus(status domain.GenbuTagProbeStatus, result genbuTagQueryResult, err error) domain.GenbuTagProbeStatus {
	status.CheckedAt = stringPointer(formatSilverwingTime(time.Now()))
	if err != nil {
		status.NotFound = false
		status.FailureReason = stringPointer(err.Error())
		return status
	}
	status.Build, status.UAT, status.Production = result.Build, result.UAT, result.Production
	status.NotFound, status.BuiltCompletedAt = result.NotFound, result.BuiltCompletedAt
	status.ReleasedCompletedAt, status.ProductionReleasedCompletedAt = result.UATReleasedCompletedAt, result.ProductionReleasedCompletedAt
	status.FailureReason = nil
	return status
}

func handoffTemplate() string {
	return "# 任务交接\n\n## 目标\n\n- 待补充。\n\n## 范围与边界\n\n- 待补充。\n\n## 已验证事实\n\n- 待补充。\n\n## 风险与未决项\n\n- 待补充。\n\n## 建议下一步\n\n- 待补充。"
}

func gitFailure(message string, result process.Result) error {
	detail := strings.TrimSpace(result.Stderr)
	if detail == "" {
		detail = strings.TrimSpace(result.Stdout)
	}
	if detail == "" {
		return fmt.Errorf("%s（退出码 %d）", message, result.ExitCode)
	}
	return fmt.Errorf("%s：%s", message, detail)
}

func samePath(left, right string) bool {
	left = canonicalPath(left)
	right = canonicalPath(right)
	if runtime.GOOS == "windows" {
		left, right = strings.ToLower(left), strings.ToLower(right)
	}
	return filepath.Clean(left) == filepath.Clean(right)
}
func canonicalPath(value string) string {
	abs, err := filepath.Abs(value)
	if err != nil {
		abs = filepath.Clean(value)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(real)
	}
	return filepath.Clean(abs)
}
func value(pointer *string) string {
	if pointer == nil {
		return ""
	}
	return *pointer
}
func stringPointer(value string) *string { return &value }
func nonEmptyLines(value string) []string {
	lines := make([]string, 0)
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
func oneOf(value domain.TagOperationState, options ...domain.TagOperationState) bool {
	for _, option := range options {
		if value == option {
			return true
		}
	}
	return false
}
func formatSilverwingTime(now time.Time) string {
	zone, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		zone = time.FixedZone("CST", 8*60*60)
	}
	return now.In(zone).Format("2006-01-02 15:04:05")
}

func stringValue(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
func boolValue(payload map[string]any, key string) bool {
	value, _ := payload[key].(bool)
	return value
}
func stringSlice(payload map[string]any, keys ...string) []string {
	for _, key := range keys {
		switch values := payload[key].(type) {
		case []string:
			return append([]string(nil), values...)
		case []any:
			result := make([]string, 0, len(values))
			for _, value := range values {
				if text, ok := value.(string); ok {
					result = append(result, text)
				}
			}
			return result
		}
	}
	return nil
}
