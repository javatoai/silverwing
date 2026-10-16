package workspace

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

type WorkspaceRemovalResult struct {
	RetainedBackupPaths []string `json:"retainedBackupPaths"`
}

type WorkspaceMutationTarget struct {
	Repository string
	Worktree   string
}

// WorkspaceLifecycle checks the full task/repository identity before every
// destructive or mutating operation. Worktrees and independent clones have
// deliberately separate removal paths.
type WorkspaceLifecycle struct {
	Manifests *storage.ManifestStore
	Git       *GitClient
	Locks     *OperationLocks
	Bootstrap *BootstrapService
}

func NewWorkspaceLifecycle(manifests *storage.ManifestStore, git *GitClient, locks *OperationLocks) *WorkspaceLifecycle {
	if manifests == nil {
		manifests = storage.NewManifestStore()
	}
	if git == nil {
		git = NewGitClient(nil, "")
	}
	if locks == nil {
		locks = NewOperationLocks("")
	}
	return &WorkspaceLifecycle{Manifests: manifests, Git: git, Locks: locks, Bootstrap: NewBootstrapService(git.Runner, git)}
}

func (l *WorkspaceLifecycle) InspectDeleteRisks(ctx context.Context, config domain.AppConfig, taskPath string, manifest domain.TaskManifest) ([]domain.DeleteRisk, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := l.validateTaskDirectory(config, taskPath, manifest); err != nil {
		if cancelErr := ctx.Err(); cancelErr != nil {
			return nil, cancelErr
		}
		return nil, err
	}
	riskList := make([]domain.DeleteRisk, 0)
	for _, ws := range physicalWorkspaces(manifest) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		target, err := l.validateTarget(taskPath, ws)
		if err != nil {
			riskList = append(riskList, domain.DeleteRisk{ServiceName: ws.ServiceName, StatusCheckError: ptr(err.Error())})
			continue
		}
		if _, err := os.Stat(target); os.IsNotExist(err) {
			continue
		} else if err != nil {
			riskList = append(riskList, domain.DeleteRisk{ServiceName: ws.ServiceName, StatusCheckError: ptr(err.Error())})
			continue
		}
		if err := l.validateDeleteRiskIdentityContext(ctx, config, ws, target); err != nil {
			if cancelErr := ctx.Err(); cancelErr != nil {
				return nil, cancelErr
			}
			riskList = append(riskList, domain.DeleteRisk{ServiceName: ws.ServiceName, StatusCheckError: ptr("工作区 Git 身份校验失败：" + err.Error())})
			continue
		}
		status, err := deleteRiskStatusContext(ctx, l.Git, target)
		if err != nil {
			if cancelErr := ctx.Err(); cancelErr != nil {
				return nil, cancelErr
			}
			riskList = append(riskList, domain.DeleteRisk{ServiceName: ws.ServiceName, StatusCheckError: ptr("无法读取工作区 Git 状态：" + err.Error())})
			continue
		}
		// Standard worktree commits remain in the shared repository after the
		// checkout is removed. Only an independent clone loses local branches.
		unpushed := 0
		if ws.Strategy == domain.WorkspaceIndependentClone {
			local, localErr := deleteRiskLocalOnlyCommitCountContext(ctx, l.Git, target)
			if localErr != nil {
				if cancelErr := ctx.Err(); cancelErr != nil {
					return nil, cancelErr
				}
				riskList = append(riskList, domain.DeleteRisk{ServiceName: ws.ServiceName, Staged: status.Staged, Unstaged: status.Unstaged, Untracked: status.Untracked, OperationInProgress: optionalPtr(status.OperationInProgress), StatusCheckError: ptr(localErr.Error())})
				continue
			}
			if local > unpushed {
				unpushed = local
			}
			if status.UnpushedCommits > unpushed {
				unpushed = status.UnpushedCommits
			}
		}
		if !status.HasUncommittedChanges() && unpushed == 0 {
			continue
		}
		riskList = append(riskList, domain.DeleteRisk{ServiceName: ws.ServiceName, Staged: status.Staged, Unstaged: status.Unstaged, Untracked: status.Untracked, OperationInProgress: optionalPtr(status.OperationInProgress), UnpushedCommits: unpushed})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return riskList, nil
}

func (l *WorkspaceLifecycle) RemoveAll(ctx context.Context, config domain.AppConfig, taskPath string, manifest domain.TaskManifest, force bool) (WorkspaceRemovalResult, error) {
	if err := l.validateTaskDirectory(config, taskPath, manifest); err != nil {
		return WorkspaceRemovalResult{}, err
	}
	workspaces := physicalWorkspaces(manifest)
	if err := l.validateRemovalCandidates(config, taskPath, workspaces); err != nil {
		return WorkspaceRemovalResult{}, err
	}
	var result WorkspaceRemovalResult
	err := l.withRemovalRepositoryLocks(config, taskPath, manifest, func() error {
		if err := l.validateRemovalCandidates(config, taskPath, workspaces); err != nil {
			return err
		}
		if err := l.requireNoUntrackedWorkspaces(config, taskPath, manifest); err != nil {
			return err
		}
		existing, err := l.existingWorkspaces(taskPath, workspaces)
		if err != nil {
			return err
		}
		if err := l.requireNoGitOperation(existing, taskPath); err != nil {
			return err
		}
		if !force {
			if err := l.requireClean(existing, taskPath); err != nil {
				return err
			}
		}
		var inner error
		result, inner = l.stageAndRemoveTask(ctx, config, taskPath, existing)
		return inner
	})
	return result, err
}

// CleanupAll removes only physical checkouts while preserving the task
// manifest, notes and Agent documents for later restoration.
func (l *WorkspaceLifecycle) CleanupAll(ctx context.Context, config domain.AppConfig, taskPath string, manifest domain.TaskManifest, force bool) (WorkspaceRemovalResult, error) {
	if err := l.validateTaskDirectory(config, taskPath, manifest); err != nil {
		return WorkspaceRemovalResult{}, err
	}
	workspaces := physicalWorkspaces(manifest)
	if err := l.validateRemovalCandidates(config, taskPath, workspaces); err != nil {
		return WorkspaceRemovalResult{}, err
	}
	var result WorkspaceRemovalResult
	err := l.withRemovalRepositoryLocks(config, taskPath, manifest, func() error {
		if err := l.validateRemovalCandidates(config, taskPath, workspaces); err != nil {
			return err
		}
		if err := l.requireNoUntrackedWorkspaces(config, taskPath, manifest); err != nil {
			return err
		}
		existing, err := l.existingWorkspaces(taskPath, workspaces)
		if err != nil {
			return err
		}
		if err := l.requireNoGitOperation(existing, taskPath); err != nil {
			return err
		}
		if !force {
			if err := l.requireClean(existing, taskPath); err != nil {
				return err
			}
		}
		var inner error
		result, inner = l.stageAndRemovePhysical(ctx, config, taskPath, existing)
		return inner
	})
	return result, err
}

func (l *WorkspaceLifecycle) RestoreAll(ctx context.Context, config domain.AppConfig, taskPath string, manifest domain.TaskManifest) ([]domain.ServiceWorkspace, error) {
	if err := l.validateTaskDirectory(config, taskPath, manifest); err != nil {
		return nil, err
	}
	if _, err := l.Manifests.Load(taskPath); err != nil {
		return nil, err
	}
	var restored []domain.ServiceWorkspace
	created := []domain.ServiceWorkspace{}
	rollback := func(primary error) ([]domain.ServiceWorkspace, error) {
		var failures []error
		for i := len(created) - 1; i >= 0; i-- {
			ws := created[i]
			if ws.Strategy == domain.WorkspaceIndependentClone {
				err := deleteOwnedClone(taskPath, ws.WorktreePath, cloneOwnership(taskPath, ws.RepositoryID, ws.GroupServiceID, ws.ModuleID))
				if err != nil {
					failures = append(failures, err)
				}
			} else {
				repository, err := l.configuredRepository(config, ws)
				if err == nil {
					err = l.Git.RemoveWorktree(repository, ws.WorktreePath, true)
				}
				if err != nil {
					failures = append(failures, err)
				}
			}
		}
		if len(failures) > 0 {
			return nil, fmt.Errorf("工作区恢复失败，自动回滚未完成：%w", errors.Join(primary, errors.Join(failures...)))
		}
		return nil, primary
	}
	for _, ws := range physicalWorkspaces(manifest) {
		target, err := l.validateTarget(taskPath, ws)
		if err != nil {
			return rollback(err)
		}
		if _, err := os.Lstat(target); err == nil {
			if identityErr := l.validateExistingIdentity(config, ws, target); identityErr == nil {
				restored = append(restored, ws)
				continue
			}
			return rollback(fmt.Errorf("恢复目标已存在且身份不匹配：%s", target))
		} else if !os.IsNotExist(err) {
			return rollback(err)
		}
		var updated domain.ServiceWorkspace
		switch ws.Strategy {
		case domain.WorkspaceStandardWorktree:
			repository, err := l.configuredRepository(config, ws)
			if err != nil {
				return rollback(err)
			}
			if err := l.Git.AddExistingWorktree(repository, target, ws.Branch, ws.ForceWorktreeAttach); err != nil {
				return rollback(err)
			}
			created = append(created, ws)
			service, _ := config.GroupService(manifest.GroupID, ws.GroupServiceID)
			boot, err := l.Bootstrap.Initialize(ctx, repository, target, service.Bootstrap, nil)
			if err != nil {
				return rollback(err)
			}
			updated = ws
			updated.Health, updated.Warnings = healthFromWarnings(boot.Warnings)
		case domain.WorkspaceIndependentClone:
			if ws.OriginURL == nil || strings.TrimSpace(*ws.OriginURL) == "" {
				return rollback(fmt.Errorf("独立克隆缺少任务记录的 origin URL"))
			}
			ownership := cloneOwnership(taskPath, ws.RepositoryID, ws.GroupServiceID, ws.ModuleID)
			if err := cloneIntoPlace(taskPath, target, ownership, func(staging string) error { return l.Git.CloneRepository(ctx, *ws.OriginURL, staging, ws.Branch) }); err != nil {
				return rollback(err)
			}
			created = append(created, ws)
			service, _ := config.GroupService(manifest.GroupID, ws.GroupServiceID)
			source := ""
			if repo, ok := repositoryByID(config, ws.RepositoryID); ok {
				source = repo.RootPath
			}
			boot, err := l.Bootstrap.Initialize(ctx, source, target, service.Bootstrap, nil)
			if err != nil {
				return rollback(err)
			}
			updated = ws
			updated.Health, updated.Warnings = healthFromWarnings(boot.Warnings)
		default:
			return rollback(fmt.Errorf("工作区策略不受支持：%s", ws.Strategy))
		}
		created[len(created)-1] = updated
		restored = append(restored, updated)
	}
	return restored, nil
}

func (l *WorkspaceLifecycle) ValidateForMutation(config domain.AppConfig, taskPath string, manifest domain.TaskManifest, ws domain.ServiceWorkspace) (WorkspaceMutationTarget, error) {
	return l.ValidateForMutationContext(context.Background(), config, taskPath, manifest, ws)
}

func (l *WorkspaceLifecycle) stageAndRemovePhysical(ctx context.Context, config domain.AppConfig, taskPath string, workspaces []domain.ServiceWorkspace) (WorkspaceRemovalResult, error) {
	parent := filepath.Dir(taskPath)
	transactionRoot, err := os.MkdirTemp(parent, "."+filepath.Base(taskPath)+".silverwing-cleanup-")
	if err != nil {
		return WorkspaceRemovalResult{}, err
	}
	defer func() {
		if _, err := os.Stat(transactionRoot); err == nil {
			_ = os.Remove(transactionRoot)
		}
	}()
	staged := []stagedWorkspace{}
	rollback := func(primary error) (WorkspaceRemovalResult, error) {
		var rollbackErrors []error
		for i := len(staged) - 1; i >= 0; i-- {
			if err := l.restoreStagedWorkspace(config, taskPath, staged[i]); err != nil {
				rollbackErrors = append(rollbackErrors, err)
			}
		}
		return WorkspaceRemovalResult{}, errors.Join(primary, errors.Join(rollbackErrors...))
	}
	for index, ws := range workspaces {
		original, err := l.validateTarget(taskPath, ws)
		if err != nil {
			return rollback(err)
		}
		backup := filepath.Join(transactionRoot, fmt.Sprintf("%d-%s", index, filepath.Base(original)))
		entry := stagedWorkspace{Workspace: ws, Original: original, Backup: backup}
		if ws.Strategy == domain.WorkspaceStandardWorktree {
			entry.Repository, err = l.configuredRepository(config, ws)
			if err == nil {
				err = l.Git.MoveWorktree(entry.Repository, original, backup)
			}
		} else {
			entry.Ownership = cloneOwnership(taskPath, ws.RepositoryID, ws.GroupServiceID, ws.ModuleID)
			err = requireOwnedClone(original, entry.Ownership)
			if err == nil {
				err = os.Rename(original, backup)
			}
		}
		if err != nil {
			return rollback(err)
		}
		staged = append(staged, entry)
	}
	var retained []string
	for _, entry := range staged {
		if entry.Workspace.Strategy == domain.WorkspaceStandardWorktree {
			if err := l.Git.RemoveWorktree(entry.Repository, entry.Backup, true); err != nil {
				retained = append(retained, entry.Backup)
				continue
			}
			_ = l.Git.PruneWorktrees(entry.Repository)
		} else if err := deleteOwnedClone(transactionRoot, entry.Backup, entry.Ownership); err != nil {
			retained = append(retained, entry.Backup)
		}
	}
	return WorkspaceRemovalResult{RetainedBackupPaths: retained}, nil
}

func (l *WorkspaceLifecycle) stageAndRemoveTask(ctx context.Context, config domain.AppConfig, taskPath string, workspaces []domain.ServiceWorkspace) (WorkspaceRemovalResult, error) {
	parent := filepath.Dir(taskPath)
	transactionRoot, err := os.MkdirTemp(parent, "."+filepath.Base(taskPath)+".silverwing-delete-")
	if err != nil {
		return WorkspaceRemovalResult{}, err
	}
	metadataBackup := filepath.Join(transactionRoot, "task-metadata")
	metadataRecovery := filepath.Join(transactionRoot, "task-metadata-recovery")
	owner := fmt.Sprintf("%d-%s", time.Now().UnixNano(), filepath.Base(taskPath))
	ownerMarker := filepath.Join(transactionRoot, "metadata-owner")
	if err := os.WriteFile(ownerMarker, []byte(owner), 0o600); err != nil {
		_ = os.RemoveAll(transactionRoot)
		return WorkspaceRemovalResult{}, err
	}
	staged := []stagedWorkspace{}
	metadataMoved := false
	metadataRecoveryReady := false
	rollback := func(primary error) (WorkspaceRemovalResult, error) {
		var rollbackErrors []error
		if _, err := os.Stat(taskPath); os.IsNotExist(err) {
			recovery := metadataBackup
			if metadataRecoveryReady {
				recovery = metadataRecovery
			}
			if _, err := os.Stat(recovery); err == nil {
				if moveErr := os.Rename(recovery, taskPath); moveErr != nil {
					rollbackErrors = append(rollbackErrors, moveErr)
				}
			}
		}
		for i := len(staged) - 1; i >= 0; i-- {
			if err := l.restoreStagedWorkspace(config, taskPath, staged[i]); err != nil {
				rollbackErrors = append(rollbackErrors, err)
			}
		}
		if len(rollbackErrors) == 0 {
			_ = os.RemoveAll(transactionRoot)
		}
		return WorkspaceRemovalResult{}, errors.Join(primary, errors.Join(rollbackErrors...))
	}
	for index, ws := range workspaces {
		original, err := l.validateTarget(taskPath, ws)
		if err != nil {
			return rollback(err)
		}
		backup := filepath.Join(transactionRoot, fmt.Sprintf("%d-%s", index, filepath.Base(original)))
		entry := stagedWorkspace{Workspace: ws, Original: original, Backup: backup}
		if ws.Strategy == domain.WorkspaceStandardWorktree {
			entry.Repository, err = l.configuredRepository(config, ws)
			if err == nil {
				err = l.Git.MoveWorktree(entry.Repository, original, backup)
			}
		} else {
			entry.Ownership = cloneOwnership(taskPath, ws.RepositoryID, ws.GroupServiceID, ws.ModuleID)
			err = requireOwnedClone(original, entry.Ownership)
			if err == nil {
				err = os.Rename(original, backup)
			}
		}
		if err != nil {
			return rollback(err)
		}
		staged = append(staged, entry)
	}
	if err := os.Rename(taskPath, metadataBackup); err != nil {
		return rollback(err)
	}
	metadataMoved = true
	if err := copyDirectory(metadataBackup, metadataRecovery); err != nil {
		return rollback(err)
	}
	metadataRecoveryReady = true
	if err := os.RemoveAll(metadataBackup); err != nil {
		return rollback(err)
	}
	if !metadataMoved {
		return rollback(fmt.Errorf("任务元数据未能进入删除事务"))
	}
	var retained []string
	for _, entry := range staged {
		if entry.Workspace.Strategy == domain.WorkspaceStandardWorktree {
			if err := l.Git.RemoveWorktree(entry.Repository, entry.Backup, true); err != nil {
				retained = append(retained, entry.Backup)
				continue
			}
			_ = l.Git.PruneWorktrees(entry.Repository)
		} else if err := deleteOwnedClone(transactionRoot, entry.Backup, entry.Ownership); err != nil {
			retained = append(retained, entry.Backup)
		}
	}
	if marker, err := os.ReadFile(ownerMarker); err != nil || string(marker) != owner {
		retained = append(retained, metadataRecovery)
	} else if err := os.RemoveAll(metadataRecovery); err != nil {
		retained = append(retained, metadataRecovery)
	}
	if len(retained) == 0 {
		_ = os.Remove(ownerMarker)
		_ = os.Remove(transactionRoot)
	}
	return WorkspaceRemovalResult{RetainedBackupPaths: retained}, nil
}

type stagedWorkspace struct {
	Workspace                               domain.ServiceWorkspace
	Original, Backup, Repository, Ownership string
}

func (l *WorkspaceLifecycle) restoreStagedWorkspace(config domain.AppConfig, taskPath string, entry stagedWorkspace) error {
	if _, err := os.Stat(entry.Backup); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := os.Stat(entry.Original); err == nil {
		return fmt.Errorf("无法自动回滚，原工作区路径已被占用：%s", entry.Original)
	}
	if entry.Workspace.Strategy == domain.WorkspaceStandardWorktree {
		return l.Git.MoveWorktree(entry.Repository, entry.Backup, entry.Original)
	}
	return os.Rename(entry.Backup, entry.Original)
}

func (l *WorkspaceLifecycle) requireClean(workspaces []domain.ServiceWorkspace, taskPath string) error {
	var unsafe []string
	for _, ws := range workspaces {
		target, err := l.validateTarget(taskPath, ws)
		if err != nil {
			return err
		}
		status, err := l.Git.Status(target)
		if err != nil {
			return err
		}
		localOnly := 0
		if ws.Strategy == domain.WorkspaceIndependentClone {
			localOnly, err = l.Git.LocalOnlyCommitCount(target)
			if err != nil {
				return err
			}
		}
		if status.HasUncommittedChanges() || localOnly > 0 {
			unsafe = append(unsafe, ws.ModuleName)
		}
	}
	if len(unsafe) > 0 {
		return fmt.Errorf("工作区状态在删除确认后发生变化，已停止删除：%s", strings.Join(unsafe, ", "))
	}
	return nil
}

func (l *WorkspaceLifecycle) requireNoGitOperation(workspaces []domain.ServiceWorkspace, taskPath string) error {
	var active []string
	for _, ws := range workspaces {
		target, err := l.validateTarget(taskPath, ws)
		if err != nil {
			return err
		}
		status, err := l.Git.Status(target)
		if err != nil {
			return err
		}
		if status.OperationInProgress != "" {
			active = append(active, ws.ServiceName+" ("+status.OperationInProgress+")")
		}
	}
	if len(active) > 0 {
		return fmt.Errorf("存在进行中的 Git 操作，不能删除工作区：%s", strings.Join(active, ", "))
	}
	return nil
}

func (l *WorkspaceLifecycle) validateRemovalCandidates(config domain.AppConfig, taskPath string, workspaces []domain.ServiceWorkspace) error {
	for _, ws := range workspaces {
		target, err := l.validateTarget(taskPath, ws)
		if err != nil {
			return err
		}
		if _, err := os.Stat(target); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		if ws.Strategy == domain.WorkspaceStandardWorktree {
			if _, err := l.configuredRepository(config, ws); err != nil {
				return err
			}
		} else {
			if _, ok := repositoryByID(config, ws.RepositoryID); !ok {
				return fmt.Errorf("仓库配置不存在：%s", ws.RepositoryID)
			}
			if ws.OriginURL == nil || strings.TrimSpace(*ws.OriginURL) == "" {
				return fmt.Errorf("独立克隆缺少任务记录的 origin URL")
			}
			if err := requireOwnedClone(target, cloneOwnership(taskPath, ws.RepositoryID, ws.GroupServiceID, ws.ModuleID)); err != nil {
				return err
			}
		}
		if err := l.validateExistingIdentity(config, ws, target); err != nil {
			return err
		}
	}
	return nil
}

func (l *WorkspaceLifecycle) validateTaskDirectory(config domain.AppConfig, taskPath string, manifest domain.TaskManifest) error {
	if config.TaskRoot == nil || strings.TrimSpace(*config.TaskRoot) == "" {
		return fmt.Errorf("尚未配置任务根目录")
	}
	root, err := filepath.Abs(filepath.Clean(*config.TaskRoot))
	if err != nil {
		return err
	}
	task, err := filepath.Abs(filepath.Clean(taskPath))
	if err != nil {
		return err
	}
	if !samePath(filepath.Dir(task), root) || filepath.Base(task) != manifest.TaskDirectoryName {
		return fmt.Errorf("任务目录与配置或任务清单不匹配：%s", task)
	}
	if filepath.VolumeName(task) == "" && filepath.Clean(task) == string(filepath.Separator) {
		return fmt.Errorf("拒绝对文件系统根目录执行任务操作")
	}
	return nil
}

func (l *WorkspaceLifecycle) validateTarget(taskPath string, ws domain.ServiceWorkspace) (string, error) {
	task, err := filepath.Abs(filepath.Clean(taskPath))
	if err != nil {
		return "", err
	}
	target, err := filepath.Abs(filepath.Clean(ws.WorktreePath))
	if err != nil {
		return "", err
	}
	if !samePath(filepath.Dir(target), task) {
		return "", fmt.Errorf("工作区路径不在任务目录的直接子级，已拒绝操作：%s", target)
	}
	return target, nil
}

func (l *WorkspaceLifecycle) configuredRepository(config domain.AppConfig, ws domain.ServiceWorkspace) (string, error) {
	return l.configuredRepositoryContext(context.Background(), config, ws)
}

func (l *WorkspaceLifecycle) validateExistingIdentity(config domain.AppConfig, ws domain.ServiceWorkspace, target string) error {
	return l.validateExistingIdentityContext(context.Background(), config, ws, target)
}

func (l *WorkspaceLifecycle) repositoryLockRoots(config domain.AppConfig, manifest domain.TaskManifest) ([]string, error) {
	roots := map[string]string{}
	for _, ws := range physicalWorkspaces(manifest) {
		var root string
		if ws.Strategy == domain.WorkspaceStandardWorktree {
			repository, err := l.configuredRepository(config, ws)
			if err != nil {
				return nil, err
			}
			root, err = l.Git.CommonDirectory(repository)
			if err != nil {
				return nil, err
			}
		} else {
			repository, ok := repositoryByID(config, ws.RepositoryID)
			if !ok {
				return nil, fmt.Errorf("仓库配置不存在：%s", ws.RepositoryID)
			}
			root = repository.GitCommonDirectory
		}
		root = mustAbs(root)
		roots[strings.ToLower(root)] = root
	}
	group, err := config.Group(manifest.GroupID)
	if err != nil {
		return nil, err
	}
	for _, service := range group.Services {
		repository, ok := repositoryByID(config, service.RepositoryID)
		if !ok {
			return nil, fmt.Errorf("仓库配置不存在：%s", service.RepositoryID)
		}
		root := mustAbs(repository.GitCommonDirectory)
		roots[strings.ToLower(root)] = root
	}
	result := make([]string, 0, len(roots))
	for _, path := range roots {
		result = append(result, path)
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i]) < strings.ToLower(result[j]) })
	return result, nil
}

// Clone checkouts may appear while a logical source-repository lock is being
// acquired. Resolve their actual common directories only after those locks,
// then revalidate all removal candidates inside both layers of locks.
func (l *WorkspaceLifecycle) withRemovalRepositoryLocks(config domain.AppConfig, taskPath string, manifest domain.TaskManifest, action func() error) error {
	roots, err := l.repositoryLockRoots(config, manifest)
	if err != nil {
		return err
	}
	return l.withRepositoryLocks(roots, 0, func() error {
		existing, err := l.existingWorkspaces(taskPath, physicalWorkspaces(manifest))
		if err != nil {
			return err
		}
		locked := map[string]bool{}
		for _, root := range roots {
			locked[strings.ToLower(root)] = true
		}
		cloneRoots := []string{}
		for _, ws := range existing {
			if ws.Strategy != domain.WorkspaceIndependentClone {
				continue
			}
			target, err := l.validateTarget(taskPath, ws)
			if err != nil {
				return err
			}
			common, err := l.Git.CommonDirectory(target)
			if err != nil {
				return err
			}
			common = mustAbs(common)
			key := strings.ToLower(common)
			if !locked[key] {
				locked[key] = true
				cloneRoots = append(cloneRoots, common)
			}
		}
		sort.Slice(cloneRoots, func(i, j int) bool { return strings.ToLower(cloneRoots[i]) < strings.ToLower(cloneRoots[j]) })
		return l.withRepositoryLocks(cloneRoots, 0, action)
	})
}

func (l *WorkspaceLifecycle) withRepositoryLocks(roots []string, index int, action func() error) error {
	if index >= len(roots) {
		return action()
	}
	return l.Locks.WithRepository(roots[index], func() error { return l.withRepositoryLocks(roots, index+1, action) })
}

func (l *WorkspaceLifecycle) existingWorkspaces(taskPath string, workspaces []domain.ServiceWorkspace) ([]domain.ServiceWorkspace, error) {
	result := []domain.ServiceWorkspace{}
	for _, ws := range workspaces {
		target, err := l.validateTarget(taskPath, ws)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(target); err == nil {
			result = append(result, ws)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return result, nil
}

func (l *WorkspaceLifecycle) requireNoUntrackedWorkspaces(config domain.AppConfig, taskPath string, manifest domain.TaskManifest) error {
	task, _ := filepath.Abs(taskPath)
	recorded := map[string]bool{}
	for _, ws := range physicalWorkspaces(manifest) {
		target, err := l.validateTarget(taskPath, ws)
		if err != nil {
			return err
		}
		recorded[strings.ToLower(filepath.Clean(target))] = true
	}
	ids := map[string]bool{}
	for _, ws := range physicalWorkspaces(manifest) {
		ids[ws.RepositoryID] = true
	}
	group, err := config.Group(manifest.GroupID)
	if err != nil {
		return err
	}
	for _, service := range group.Services {
		ids[service.RepositoryID] = true
	}
	for repositoryID := range ids {
		repository, ok := repositoryByID(config, repositoryID)
		if !ok {
			return fmt.Errorf("仓库配置不存在：%s", repositoryID)
		}
		if _, err := os.Stat(repository.RootPath); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		entries, err := l.Git.Worktrees(repository.RootPath)
		if err != nil {
			return err
		}
		for _, wt := range entries {
			path := filepath.Clean(wt.Path)
			if isWithin(path, task) && !recorded[strings.ToLower(path)] {
				return fmt.Errorf("任务目录中存在未记录的 Linked Worktree，请先人工处理后再删除任务：%s", path)
			}
		}
	}
	for _, ws := range physicalWorkspaces(manifest) {
		if ws.Strategy != domain.WorkspaceIndependentClone {
			continue
		}
		clone, err := l.validateTarget(taskPath, ws)
		if err != nil {
			return err
		}
		if _, err := os.Stat(clone); os.IsNotExist(err) {
			continue
		} else if err != nil {
			return err
		}
		entries, err := l.Git.Worktrees(clone)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if !samePath(entry.Path, clone) {
				return fmt.Errorf("任务目录中存在未记录的 Linked Worktree，请先人工处理后再删除任务：%s", entry.Path)
			}
		}
	}
	return l.walkUnrecordedGitRoots(task, recorded)
}

func (l *WorkspaceLifecycle) walkUnrecordedGitRoots(task string, recorded map[string]bool) error {
	return filepath.WalkDir(task, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		if filepath.Base(path) == ".git" {
			return filepath.SkipDir
		}
		if recorded[strings.ToLower(filepath.Clean(path))] {
			return nil
		}
		isGitRoot := false
		if info, e := os.Stat(filepath.Join(path, ".git")); e == nil {
			isGitRoot = info.IsDir()
			if info.Mode().IsRegular() {
				gitDir, gitErr := l.Git.Run(path, "rev-parse", "--absolute-git-dir")
				commonDir, commonErr := l.Git.CommonDirectory(path)
				isGitRoot = gitErr != nil || commonErr != nil || !samePath(strings.TrimSpace(gitDir.Stdout), commonDir)
			}
		}
		if !isGitRoot {
			head, headErr := os.Stat(filepath.Join(path, "HEAD"))
			objects, objectsErr := os.Stat(filepath.Join(path, "objects"))
			refs, refsErr := os.Stat(filepath.Join(path, "refs"))
			isGitRoot = headErr == nil && head.Mode().IsRegular() && objectsErr == nil && objects.IsDir() && refsErr == nil && refs.IsDir()
		}
		if isGitRoot {
			return fmt.Errorf("任务目录中存在未记录的 Git checkout，请先人工处理后再删除任务：%s", path)
		}
		return nil
	})
}

func physicalWorkspaces(manifest domain.TaskManifest) []domain.ServiceWorkspace {
	seen := map[string]bool{}
	result := []domain.ServiceWorkspace{}
	for _, ws := range manifest.Services {
		key := strings.ToLower(filepath.Clean(ws.WorktreePath))
		if !seen[key] {
			seen[key] = true
			result = append(result, ws)
		}
	}
	return result
}

func copyDirectory(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, rel)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
				return err
			}
			return os.Symlink(link, destination)
		}
		if info.IsDir() {
			return os.MkdirAll(destination, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("不支持恢复特殊文件：%s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, data, info.Mode().Perm())
	})
}

func (l *WorkspaceLifecycle) taskDirectoryFromManifest(manifest domain.TaskManifest, config domain.AppConfig) string {
	return filepath.Join(stringValue(config.TaskRoot), manifest.TaskDirectoryName)
}
func taskPathOf(manifest domain.TaskManifest, config domain.AppConfig) string {
	return filepath.Join(stringValue(config.TaskRoot), manifest.TaskDirectoryName)
}
func isWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
func healthFromWarnings(warnings []string) (domain.WorkspaceHealth, []string) {
	if len(warnings) == 0 {
		return domain.WorkspaceReady, nil
	}
	return domain.WorkspaceReadyWithWarnings, warnings
}
