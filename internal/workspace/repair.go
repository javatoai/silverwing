package workspace

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

type WorkspaceRepairAction string

const (
	RepairReattachWorktree          WorkspaceRepairAction = "REATTACH_WORKTREE"
	RepairTrackRemoteAndAttach      WorkspaceRepairAction = "TRACK_REMOTE_AND_ATTACH"
	RepairBackupAndRecreateWorktree WorkspaceRepairAction = "BACKUP_AND_RECREATE_WORKTREE"
	RepairReclone                   WorkspaceRepairAction = "RECLONE"
	RepairSwitchBranch              WorkspaceRepairAction = "SWITCH_BRANCH"
	RepairManual                    WorkspaceRepairAction = "MANUAL"
)

type WorkspaceRepairPreview struct {
	WorkspacePath                    string                   `json:"workspacePath"`
	ServiceName                      string                   `json:"serviceName"`
	Issue                            domain.WorkspaceGitIssue `json:"issue"`
	Action                           WorkspaceRepairAction    `json:"action"`
	CanRepair                        bool                     `json:"canRepair"`
	ExpectedBranch                   string                   `json:"expectedBranch"`
	ActualBranch                     *string                  `json:"actualBranch"`
	Remote                           string                   `json:"remote"`
	RemoteBranchExists               bool                     `json:"remoteBranchExists"`
	RemoteBranchSHA                  string                   `json:"remoteBranchSha"`
	RequiresRemoteReuseConfirmation  bool                     `json:"requiresRemoteReuseConfirmation"`
	RequiresSharedBranchConfirmation bool                     `json:"requiresSharedBranchConfirmation"`
	OccupiedWorktreePaths            []string                 `json:"occupiedWorktreePaths"`
	LockedWorktreePaths              []string                 `json:"lockedWorktreePaths"`
	BackupPath                       string                   `json:"backupPath"`
	RunsBootstrap                    bool                     `json:"runsBootstrap"`
	Steps                            []string                 `json:"steps"`
	Message                          string                   `json:"message"`
	StateFingerprint                 string                   `json:"stateFingerprint"`
}
type WorkspaceRepairConfirmation struct {
	ReuseRemoteBranch     bool `json:"reuseRemoteBranch"`
	ShareCheckedOutBranch bool `json:"shareCheckedOutBranch"`
}
type WorkspaceRepairResult struct {
	WorkspacePath string   `json:"workspacePath"`
	ServiceName   string   `json:"serviceName"`
	BackupPath    string   `json:"backupPath"`
	Warnings      []string `json:"warnings"`
	Message       string   `json:"message"`
}

type WorkspaceRepairService struct {
	Manifests    *storage.ManifestStore
	Documents    *AgentDocumentService
	Locks        *OperationLocks
	Git          *GitClient
	Bootstrap    *BootstrapService
	HealthReader WorkspaceGitStatusReader
	Now          func() time.Time
}

func NewWorkspaceRepairService(manifests *storage.ManifestStore, docs *AgentDocumentService, locks *OperationLocks, git *GitClient, bootstrap *BootstrapService) *WorkspaceRepairService {
	if manifests == nil {
		manifests = storage.NewManifestStore()
	}
	if git == nil {
		git = NewGitClient(nil, "")
	}
	if docs == nil {
		docs = NewAgentDocumentService(storage.NewPaths("."), manifests)
	}
	if locks == nil {
		locks = NewOperationLocks("")
	}
	if bootstrap == nil {
		bootstrap = NewBootstrapService(git.Runner, git)
	}
	return &WorkspaceRepairService{Manifests: manifests, Documents: docs, Locks: locks, Git: git, Bootstrap: bootstrap, HealthReader: NewWorkspaceGitStatusReader(git), Now: time.Now}
}

func (s *WorkspaceRepairService) Inspect(config domain.AppConfig, taskPath, workspacePath string) (WorkspaceRepairPreview, error) {
	manifest, err := s.Manifests.Load(taskPath)
	if err != nil {
		return WorkspaceRepairPreview{}, err
	}
	ws, ok := physicalWorkspace(manifest, workspacePath)
	if !ok {
		return WorkspaceRepairPreview{}, fmt.Errorf("任务中不存在工作区：%s", workspacePath)
	}
	return s.inspectWorkspace(config, taskPath, manifest, ws, ""), nil
}

func (s *WorkspaceRepairService) Repair(ctx context.Context, config domain.AppConfig, taskPath string, requested WorkspaceRepairPreview, confirmation WorkspaceRepairConfirmation) (WorkspaceRepairResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	var result WorkspaceRepairResult
	err := s.Locks.WithTask(taskPath, func() error {
		manifest, err := s.Manifests.Load(taskPath)
		if err != nil {
			return err
		}
		ws, ok := physicalWorkspace(manifest, requested.WorkspacePath)
		if !ok {
			return fmt.Errorf("任务中不存在工作区：%s", requested.WorkspacePath)
		}
		repository, _ := repositoryByID(config, ws.RepositoryID)
		execute := func() error {
			current := s.inspectWorkspace(config, taskPath, manifest, ws, requested.BackupPath)
			if current.StateFingerprint != requested.StateFingerprint {
				return fmt.Errorf("工作区状态在确认后发生变化，请重新检查修复方案")
			}
			if !current.CanRepair {
				return fmt.Errorf("%s", current.Message)
			}
			if current.RequiresRemoteReuseConfirmation && !confirmation.ReuseRemoteBranch {
				return fmt.Errorf("必须确认复用远程分支后才能修复")
			}
			if current.RequiresSharedBranchConfirmation && !confirmation.ShareCheckedOutBranch {
				return fmt.Errorf("必须确认共享已检出分支的风险后才能修复")
			}
			var inner error
			result, inner = s.executeRepair(ctx, config, taskPath, manifest, ws, current, repository)
			return inner
		}
		lockRoot := repository.GitCommonDirectory
		if ws.Strategy == domain.WorkspaceIndependentClone {
			target := mustAbs(ws.WorktreePath)
			if _, err := os.Stat(target); err == nil {
				if common, e := s.Git.CommonDirectory(target); e == nil {
					lockRoot = common
				} else {
					lockRoot = target
				}
			}
		}
		return s.Locks.WithRepository(lockRoot, execute)
	})
	return result, err
}

func (s *WorkspaceRepairService) inspectWorkspace(config domain.AppConfig, taskPath string, manifest domain.TaskManifest, ws domain.ServiceWorkspace, fixedBackup string) WorkspaceRepairPreview {
	target := mustAbs(ws.WorktreePath)
	health := s.HealthReader.Read(ws)
	exists := false
	if _, err := os.Stat(target); err == nil {
		exists = true
	}
	repo, hasRepo := repositoryByID(config, ws.RepositoryID)
	var backup string
	if fixedBackup != "" {
		backup = fixedBackup
	} else if exists && (health.Issue == domain.WorkspaceGitIssueNotGit || health.Issue == domain.WorkspaceGitIssueIdentityMismatch) {
		backup = filepath.Join(filepath.Dir(target), filepath.Base(target)+fmt.Sprintf(".silverwing-repair-backup-%d", s.now().UnixMilli()))
	}
	if ws.Strategy == domain.WorkspaceStandardWorktree {
		return s.inspectWorktree(ws, health, target, repo, hasRepo, backup)
	}
	return s.inspectClone(ws, health, target, repo, hasRepo, backup)
}

func (s *WorkspaceRepairService) inspectWorktree(ws domain.ServiceWorkspace, health domain.WorkspaceGitHealth, target string, repo domain.RepositoryConfig, hasRepo bool, backup string) WorkspaceRepairPreview {
	exists := fileExists(target)
	if !hasRepo {
		return s.manual(ws, health, "任务记录对应的原仓库配置不存在")
	}
	repository := mustAbs(repo.RootPath)
	top, err := s.Git.TopLevel(repository)
	if err != nil || !samePath(top, repository) {
		return s.manual(ws, health, "原仓库 Git 身份已失效，无法自动修复 Worktree")
	}
	common, err := s.Git.CommonDirectory(repository)
	if err != nil || !samePath(common, repo.GitCommonDirectory) {
		return s.manual(ws, health, "原仓库 Git 身份已失效，无法自动修复 Worktree")
	}
	local, err := s.Git.RefExists(repository, "refs/heads/"+ws.Branch)
	if err != nil {
		return s.manual(ws, health, err.Error())
	}
	sha, err := s.remoteBranchSHA(repository, ws.PushRemote, ws.Branch)
	if err != nil {
		return s.manual(ws, health, err.Error())
	}
	remoteExists := sha != ""
	worktrees, err := s.Git.Worktrees(repository)
	if err != nil {
		return s.manual(ws, health, err.Error())
	}
	occupied := []WorktreeRecord{}
	if local {
		for _, wt := range worktrees {
			if wt.Branch == ws.Branch && !samePath(wt.Path, target) {
				occupied = append(occupied, wt)
			}
		}
	}
	locked := []WorktreeRecord{}
	for _, wt := range occupied {
		if wt.Locked {
			locked = append(locked, wt)
		}
	}
	fingerprint := repairFingerprint(ws, health, target, local, sha, occupied)
	if len(locked) > 0 {
		return s.repairPreview(ws, health, RepairManual, false, fingerprint, "期望分支被锁定 Worktree 占用，请先执行 git worktree unlock", occupied, locked)
	}
	if health.Issue == domain.WorkspaceGitIssueOperationInProgress || health.Issue == domain.WorkspaceGitIssueInspectionFailed {
		return s.repairPreview(ws, health, RepairManual, false, fingerprint, repairMessage(health, "无法自动修复"), nil, nil)
	}
	if health.Issue == domain.WorkspaceGitIssueBranchMismatch || health.Issue == domain.WorkspaceGitIssueDetachedHead {
		if exists {
			status, err := s.Git.Status(target)
			if err != nil {
				return s.repairPreview(ws, health, RepairManual, false, fingerprint, err.Error(), occupied, locked)
			}
			if status.OperationInProgress != "" {
				return s.repairPreview(ws, health, RepairManual, false, fingerprint, "存在进行中的 Git 操作："+status.OperationInProgress, occupied, locked)
			}
			if status.HasUncommittedChanges() {
				return s.repairPreview(ws, health, RepairManual, false, fingerprint, "工作区存在改动，提交或清理后才能切换分支", occupied, locked)
			}
		}
		if !local && !remoteExists {
			return s.repairPreview(ws, health, RepairManual, false, fingerprint, "期望分支在本地和远程都不存在，无法安全切换", occupied, locked)
		}
		preview := s.repairPreview(ws, health, RepairSwitchBranch, true, fingerprint, "将当前工作区切换回任务定义的分支", occupied, locked)
		preview.Remote = ws.PushRemote
		preview.RemoteBranchExists = remoteExists
		preview.RemoteBranchSHA = sha
		preview.RequiresRemoteReuseConfirmation = !local && remoteExists
		preview.RequiresSharedBranchConfirmation = len(occupied) > 0
		preview.Steps = []string{"确认工作区干净", "切换到任务分支 " + ws.Branch, "重新检查 Git 状态"}
		return preview
	}
	if health.Issue != domain.WorkspaceGitIssueMissing && health.Issue != domain.WorkspaceGitIssueNotGit && health.Issue != domain.WorkspaceGitIssueIdentityMismatch {
		return s.repairPreview(ws, health, RepairManual, false, fingerprint, "当前工作区不需要修复", nil, nil)
	}
	if !local && !remoteExists {
		return s.repairPreview(ws, health, RepairManual, false, fingerprint, "任务分支在本地和远程都不存在，无法证明应恢复到哪个提交", nil, nil)
	}
	action := RepairReattachWorktree
	if exists {
		action = RepairBackupAndRecreateWorktree
	} else if !local {
		action = RepairTrackRemoteAndAttach
	}
	preview := s.repairPreview(ws, health, action, true, fingerprint, "将重新创建任务 Worktree", occupied, locked)
	preview.Remote = ws.PushRemote
	preview.RemoteBranchExists = remoteExists
	preview.RemoteBranchSHA = sha
	preview.RequiresRemoteReuseConfirmation = !local && remoteExists
	preview.RequiresSharedBranchConfirmation = len(occupied) > 0
	preview.BackupPath = backup
	preview.RunsBootstrap = true
	preview.Steps = []string{}
	if exists {
		preview.Steps = append(preview.Steps, "将失效目录备份到 "+backup)
	}
	preview.Steps = append(preview.Steps, "清理陈旧 Worktree 登记")
	if local {
		preview.Steps = append(preview.Steps, "重新附加本地分支 "+ws.Branch)
	} else {
		preview.Steps = append(preview.Steps, "复用 "+ws.PushRemote+"/"+ws.Branch+" 创建本地跟踪分支")
	}
	preview.Steps = append(preview.Steps, "执行服务 Bootstrap", "重新检查 Git 状态")
	return preview
}

func (s *WorkspaceRepairService) inspectClone(ws domain.ServiceWorkspace, health domain.WorkspaceGitHealth, target string, repo domain.RepositoryConfig, hasRepo bool, backup string) WorkspaceRepairPreview {
	exists := fileExists(target)
	if ws.OriginURL == nil || strings.TrimSpace(*ws.OriginURL) == "" {
		return s.manual(ws, health, "独立克隆缺少 origin URL")
	}
	commandRoot := target
	if hasRepo {
		if info, err := os.Stat(repo.RootPath); err == nil && info.IsDir() {
			commandRoot = repo.RootPath
		}
	} else {
		commandRoot = filepath.Dir(target)
	}
	sha, err := s.remoteBranchSHA(commandRoot, *ws.OriginURL, ws.Branch)
	if err != nil {
		return s.manual(ws, health, err.Error())
	}
	remoteExists := sha != ""
	local := false
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		local, _ = s.Git.RefExists(target, "refs/heads/"+ws.Branch)
	}
	fingerprint := repairFingerprint(ws, health, target, local, sha, nil)
	if health.Issue == domain.WorkspaceGitIssueOperationInProgress || health.Issue == domain.WorkspaceGitIssueInspectionFailed {
		return s.repairPreview(ws, health, RepairManual, false, fingerprint, repairMessage(health, "无法自动修复"), nil, nil)
	}
	if health.Issue == domain.WorkspaceGitIssueBranchMismatch || health.Issue == domain.WorkspaceGitIssueDetachedHead {
		if info, err := os.Stat(target); err == nil && info.IsDir() {
			status, e := s.Git.Status(target)
			if e != nil {
				return s.repairPreview(ws, health, RepairManual, false, fingerprint, e.Error(), nil, nil)
			}
			if status.OperationInProgress != "" {
				return s.repairPreview(ws, health, RepairManual, false, fingerprint, "存在进行中的 Git 操作："+status.OperationInProgress, nil, nil)
			}
			if status.HasUncommittedChanges() {
				return s.repairPreview(ws, health, RepairManual, false, fingerprint, "独立克隆存在改动，提交或清理后才能切换分支", nil, nil)
			}
		}
		if !local && !remoteExists {
			return s.repairPreview(ws, health, RepairManual, false, fingerprint, "期望分支在本地和远程都不存在", nil, nil)
		}
		p := s.repairPreview(ws, health, RepairSwitchBranch, true, fingerprint, "将独立克隆切换回任务分支", nil, nil)
		p.Remote = ws.PushRemote
		p.RemoteBranchExists = remoteExists
		p.RemoteBranchSHA = sha
		p.RequiresRemoteReuseConfirmation = !local && remoteExists
		p.Steps = []string{"确认独立克隆工作区干净", "切换到 " + ws.Branch, "重新检查 Git 状态"}
		return p
	}
	if health.Issue != domain.WorkspaceGitIssueMissing && health.Issue != domain.WorkspaceGitIssueNotGit && health.Issue != domain.WorkspaceGitIssueIdentityMismatch {
		return s.repairPreview(ws, health, RepairManual, false, fingerprint, "当前工作区不需要修复", nil, nil)
	}
	if !remoteExists {
		return s.repairPreview(ws, health, RepairManual, false, fingerprint, "远程分支不存在，无法重新克隆", nil, nil)
	}
	p := s.repairPreview(ws, health, RepairReclone, true, fingerprint, "将重新创建独立克隆", nil, nil)
	p.Remote = *ws.OriginURL
	p.RemoteBranchExists = true
	p.RemoteBranchSHA = sha
	p.BackupPath = backup
	p.RunsBootstrap = true
	p.Steps = []string{}
	if exists {
		p.Steps = append(p.Steps, "将失效目录备份到 "+backup)
	}
	p.Steps = append(p.Steps, "从远程重新克隆分支 "+ws.Branch, "执行服务 Bootstrap", "重新检查 Git 状态")
	return p
}

func (s *WorkspaceRepairService) executeRepair(ctx context.Context, config domain.AppConfig, taskPath string, manifest domain.TaskManifest, ws domain.ServiceWorkspace, preview WorkspaceRepairPreview, repo domain.RepositoryConfig) (WorkspaceRepairResult, error) {
	target, err := s.validateTarget(config, taskPath, manifest, ws)
	if err != nil {
		return WorkspaceRepairResult{}, err
	}
	backup := preview.BackupPath
	backupMoved, created, manifestSaved := false, false, false
	var previousBranch, previousHead string
	warnings := []string{}
	ownership := ""
	rollback := func(primary error) error {
		rollbackContext := context.WithoutCancel(ctx)
		var failures []error
		if manifestSaved {
			if e := s.Manifests.Save(taskPath, manifest); e != nil {
				failures = append(failures, e)
			}
			if _, e := s.Documents.WriteTaskDocument(taskPath, manifest, config); e != nil {
				failures = append(failures, e)
			}
		}
		if previousHead != "" {
			if _, e := os.Stat(target); e == nil {
				if st, se := s.Git.Status(target); se == nil && !st.HasUncommittedChanges() {
					if previousBranch != "" {
						_, e = s.Git.RunContext(rollbackContext, target, 2*time.Minute, true, false, "switch", previousBranch)
					} else {
						_, e = s.Git.RunContext(rollbackContext, target, 2*time.Minute, true, false, "switch", "--detach", previousHead)
					}
				}
				if e != nil {
					failures = append(failures, e)
				}
			}
		}
		if created {
			if ws.Strategy == domain.WorkspaceStandardWorktree {
				if e := s.Git.RemoveWorktree(repo.RootPath, target, true); e != nil {
					failures = append(failures, e)
				}
				_ = s.Git.PruneWorktrees(repo.RootPath)
			} else if e := deleteOwnedClone(taskPath, target, ownership); e != nil {
				failures = append(failures, e)
			}
		}
		if backupMoved {
			if _, e := os.Stat(backup); e == nil {
				if _, e2 := os.Stat(target); os.IsNotExist(e2) {
					if e3 := os.Rename(backup, target); e3 != nil {
						failures = append(failures, e3)
					}
				} else {
					failures = append(failures, fmt.Errorf("无法恢复备份，目标已占用：%s", target))
				}
			}
		}
		if len(failures) > 0 {
			return fmt.Errorf("工作区修复失败，且自动回滚未完整完成：%w", errors.Join(primary, errors.Join(failures...)))
		}
		return primary
	}
	if backup != "" {
		if _, err := os.Stat(target); err == nil {
			if !samePath(filepath.Dir(target), mustAbs(taskPath)) {
				return WorkspaceRepairResult{}, fmt.Errorf("只能备份任务目录内的工作区")
			}
			if _, err := os.Stat(backup); err == nil {
				return WorkspaceRepairResult{}, fmt.Errorf("备份目标已经存在：%s", backup)
			}
			if err := os.Rename(target, backup); err != nil {
				return WorkspaceRepairResult{}, err
			}
			backupMoved = true
		}
	}
	switch ws.Strategy {
	case domain.WorkspaceStandardWorktree:
		repository := repo.RootPath
		if preview.Action == RepairSwitchBranch {
			previousBranch, _ = s.Git.CurrentBranch(target)
			previousHead, _ = s.Git.Resolve(target, "HEAD")
			if err := s.Git.Fetch(repository, ws.PushRemote); err != nil {
				return WorkspaceRepairResult{}, rollback(err)
			}
			if err := s.switchToExpected(target, ws, preview.RequiresSharedBranchConfirmation); err != nil {
				return WorkspaceRepairResult{}, rollback(err)
			}
		} else {
			if err := s.Git.PruneWorktrees(repository); err != nil {
				return WorkspaceRepairResult{}, rollback(err)
			}
			if err := s.Git.Fetch(repository, ws.PushRemote); err != nil {
				return WorkspaceRepairResult{}, rollback(err)
			}
			local, err := s.Git.RefExists(repository, "refs/heads/"+ws.Branch)
			if err != nil {
				return WorkspaceRepairResult{}, rollback(err)
			}
			remote, err := s.Git.RefExists(repository, "refs/remotes/"+ws.PushRemote+"/"+ws.Branch)
			if err != nil {
				return WorkspaceRepairResult{}, rollback(err)
			}
			if !local && remote {
				sha, err := s.Git.Resolve(repository, ws.PushRemote+"/"+ws.Branch)
				if err != nil {
					return WorkspaceRepairResult{}, rollback(err)
				}
				if sha != preview.RemoteBranchSHA {
					return WorkspaceRepairResult{}, rollback(fmt.Errorf("远程任务分支状态已变化，请重新预检"))
				}
			}
			if local {
				err = s.Git.AddExistingWorktree(repository, target, ws.Branch, preview.RequiresSharedBranchConfirmation)
			} else if remote {
				err = s.Git.AddTrackedRemoteWorktree(repository, target, ws.Branch, ws.PushRemote)
			} else {
				err = fmt.Errorf("确认后任务分支已经消失，请重新预检")
			}
			if err != nil {
				return WorkspaceRepairResult{}, rollback(err)
			}
			created = true
			warnings, err = s.initialize(config, manifest, ws, repository, target, ctx)
			if err != nil {
				return WorkspaceRepairResult{}, rollback(err)
			}
		}
	case domain.WorkspaceIndependentClone:
		if preview.Action == RepairSwitchBranch {
			previousBranch, _ = s.Git.CurrentBranch(target)
			previousHead, _ = s.Git.Resolve(target, "HEAD")
			if err := s.Git.Fetch(target, ws.PushRemote); err != nil {
				return WorkspaceRepairResult{}, rollback(err)
			}
			if err := s.switchToExpected(target, ws, false); err != nil {
				return WorkspaceRepairResult{}, rollback(err)
			}
		} else {
			ownership = cloneOwnership(taskPath, ws.RepositoryID, ws.GroupServiceID, ws.ModuleID)
			if err := cloneIntoPlace(taskPath, target, ownership, func(staging string) error {
				if err := s.Git.CloneRepository(ctx, strings.TrimSpace(stringValue(ws.OriginURL)), staging, ws.Branch); err != nil {
					return err
				}
				head, err := s.Git.Resolve(staging, "HEAD")
				if err != nil {
					return err
				}
				if head != preview.RemoteBranchSHA {
					return fmt.Errorf("远程任务分支状态已变化，请重新预检")
				}
				return nil
			}); err != nil {
				return WorkspaceRepairResult{}, rollback(err)
			}
			created = true
			source := repo.RootPath
			warnings, err = s.initialize(config, manifest, ws, source, target, ctx)
			if err != nil {
				return WorkspaceRepairResult{}, rollback(err)
			}
		}
	}
	updated := manifest
	updated.Services = append([]domain.ServiceWorkspace(nil), manifest.Services...)
	updated.UpdatedAt = s.now().UTC().Format(time.RFC3339Nano)
	for i := range updated.Services {
		if samePath(updated.Services[i].WorktreePath, ws.WorktreePath) {
			updated.Services[i].Health = domain.WorkspaceReady
			updated.Services[i].Warnings = warnings
			if len(warnings) > 0 {
				updated.Services[i].Health = domain.WorkspaceReadyWithWarnings
			}
		}
	}
	if err := s.Manifests.Save(taskPath, updated); err != nil {
		return WorkspaceRepairResult{}, rollback(err)
	}
	manifestSaved = true
	if _, err := s.Documents.WriteTaskDocument(taskPath, updated, config); err != nil {
		return WorkspaceRepairResult{}, rollback(err)
	}
	return WorkspaceRepairResult{WorkspacePath: ws.WorktreePath, ServiceName: workspaceOperationName(ws), BackupPath: backup, Warnings: warnings, Message: "工作区已修复并重新检查"}, nil
}

func (s *WorkspaceRepairService) switchToExpected(target string, ws domain.ServiceWorkspace, allowShared bool) error {
	branch, err := s.Git.CurrentBranch(target)
	if err != nil {
		return err
	}
	if branch == ws.Branch {
		return nil
	}
	local, err := s.Git.RefExists(target, "refs/heads/"+ws.Branch)
	if err != nil {
		return err
	}
	remoteRef := ws.PushRemote + "/" + ws.Branch
	remote, err := s.Git.RefExists(target, "refs/remotes/"+remoteRef)
	if err != nil {
		return err
	}
	if local {
		args := []string{"switch", ws.Branch}
		if allowShared {
			args = []string{"switch", "--ignore-other-worktrees", ws.Branch}
		}
		_, err = s.Git.RunContext(context.Background(), target, 2*time.Minute, true, false, args...)
		return err
	}
	if remote {
		_, err = s.Git.RunContext(context.Background(), target, 2*time.Minute, true, false, "switch", "-c", ws.Branch, "--track", remoteRef)
		return err
	}
	return fmt.Errorf("期望分支已经不存在，请重新预检")
}
func (s *WorkspaceRepairService) initialize(config domain.AppConfig, manifest domain.TaskManifest, ws domain.ServiceWorkspace, source, target string, ctx context.Context) ([]string, error) {
	service, err := config.GroupService(manifest.GroupID, ws.GroupServiceID)
	if err != nil {
		return []string{"服务配置已经不存在，未执行 Bootstrap"}, nil
	}
	result, err := s.Bootstrap.Initialize(ctx, source, target, service.Bootstrap, nil)
	if err != nil {
		return nil, err
	}
	return result.Warnings, nil
}
func (s *WorkspaceRepairService) remoteBranchSHA(repository, remote, branch string) (string, error) {
	result, err := s.Git.RunContext(context.Background(), repository, 5*time.Minute, false, true, "ls-remote", "--exit-code", "--heads", remote, "refs/heads/"+branch)
	if err != nil {
		return "", err
	}
	if result.ExitCode == 2 {
		return "", nil
	}
	if result.ExitCode != 0 {
		return "", &GitException{Message: fmt.Sprintf("远程分支检查失败：%s/%s", remote, branch), Result: result}
	}
	for _, line := range strings.Split(result.Stdout, "\n") {
		if strings.TrimSpace(line) != "" {
			return strings.TrimSpace(strings.SplitN(line, "\t", 2)[0]), nil
		}
	}
	return "", nil
}
func (s *WorkspaceRepairService) repairPreview(ws domain.ServiceWorkspace, h domain.WorkspaceGitHealth, action WorkspaceRepairAction, can bool, fingerprint, message string, occupied, locked []WorktreeRecord) WorkspaceRepairPreview {
	paths := []string{}
	lockedPaths := []string{}
	for _, w := range occupied {
		paths = append(paths, w.Path)
		if w.Locked {
			lockedPaths = append(lockedPaths, w.Path)
		}
	}
	return WorkspaceRepairPreview{WorkspacePath: ws.WorktreePath, ServiceName: workspaceOperationName(ws), Issue: h.Issue, Action: action, CanRepair: can, ExpectedBranch: ws.Branch, ActualBranch: h.ActualBranch, OccupiedWorktreePaths: paths, LockedWorktreePaths: lockedPaths, Steps: []string{}, Message: message, StateFingerprint: fingerprint}
}
func (s *WorkspaceRepairService) manual(ws domain.ServiceWorkspace, h domain.WorkspaceGitHealth, message string) WorkspaceRepairPreview {
	target := mustAbs(ws.WorktreePath)
	fingerprint := repairFingerprint(ws, h, target, false, "", nil)
	return s.repairPreview(ws, h, RepairManual, false, fingerprint, message, nil, nil)
}
func (s *WorkspaceRepairService) validateTarget(config domain.AppConfig, taskPath string, manifest domain.TaskManifest, ws domain.ServiceWorkspace) (string, error) {
	if config.TaskRoot == nil || !samePath(filepath.Dir(mustAbs(taskPath)), mustAbs(*config.TaskRoot)) || filepath.Base(taskPath) != manifest.TaskDirectoryName {
		return "", fmt.Errorf("任务目录不属于当前任务根目录或与任务清单不一致：%s", taskPath)
	}
	target := mustAbs(ws.WorktreePath)
	if !samePath(filepath.Dir(target), mustAbs(taskPath)) {
		return "", fmt.Errorf("工作区路径不是任务目录的直接子级，已拒绝修复：%s", target)
	}
	return target, nil
}
func repairFingerprint(ws domain.ServiceWorkspace, h domain.WorkspaceGitHealth, target string, local bool, remoteSHA string, worktrees []WorktreeRecord) string {
	parts := []string{"path=" + target, "exists=" + fmt.Sprint(fileExists(target)), "issue=" + string(h.Issue), "actual=" + stringValue(h.ActualBranch), "expected=" + ws.Branch, "local=" + fmt.Sprint(local), "remote=" + remoteSHA}
	sort.Slice(worktrees, func(i, j int) bool { return worktrees[i].Path < worktrees[j].Path })
	items := []string{}
	for _, wt := range worktrees {
		items = append(items, wt.Path+"|"+fmt.Sprint(wt.Locked))
	}
	parts = append(parts, "worktrees="+strings.Join(items, ","))
	return strings.Join(parts, ";")
}
func (s *WorkspaceRepairService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func physicalWorkspace(manifest domain.TaskManifest, path string) (domain.ServiceWorkspace, bool) {
	for _, ws := range manifest.Services {
		if samePath(ws.WorktreePath, path) {
			return ws, true
		}
	}
	return domain.ServiceWorkspace{}, false
}

func repairMessage(health domain.WorkspaceGitHealth, fallback string) string {
	if health.Message != nil && strings.TrimSpace(*health.Message) != "" {
		return *health.Message
	}
	return fallback
}
