package workspace

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

type WorkspaceModuleRemovalPreview struct {
	WorkspacePath            string   `json:"workspacePath"`
	ServiceID                string   `json:"serviceId"`
	ModuleID                 string   `json:"moduleId"`
	ServiceName              string   `json:"serviceName"`
	ModuleName               string   `json:"moduleName"`
	Branch                   string   `json:"branch"`
	BaseRef                  *string  `json:"baseRef"`
	Staged                   bool     `json:"staged"`
	Unstaged                 bool     `json:"unstaged"`
	Untracked                bool     `json:"untracked"`
	ChangedFiles             []string `json:"changedFiles"`
	CommitsAheadOfBase       int      `json:"commitsAheadOfBase"`
	CommitsBehindBase        int      `json:"commitsBehindBase"`
	UnpushedCommits          int      `json:"unpushedCommits"`
	PathMissing              bool     `json:"pathMissing"`
	RequiresRiskConfirmation bool     `json:"requiresRiskConfirmation"`
	Fingerprint              string   `json:"fingerprint"`
}

type WorkspaceModuleRemovalConfirmation struct {
	Fingerprint         string `json:"fingerprint"`
	AcknowledgeDataLoss bool   `json:"acknowledgeDataLoss"`
}

type WorkspaceModuleRemovalResult struct {
	Manifest           domain.TaskManifest `json:"manifest"`
	RetainedBackupPath string              `json:"retainedBackupPath"`
	CleanupError       string              `json:"cleanupError"`
}

type WorkspaceModuleRemovalService struct {
	Manifests *storage.ManifestStore
	Documents *AgentDocumentService
	Locks     *OperationLocks
	Git       *GitClient
	Now       func() time.Time
}

func NewWorkspaceModuleRemovalService(manifests *storage.ManifestStore, docs *AgentDocumentService, locks *OperationLocks, git *GitClient) *WorkspaceModuleRemovalService {
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
	return &WorkspaceModuleRemovalService{Manifests: manifests, Documents: docs, Locks: locks, Git: git, Now: time.Now}
}

func (s *WorkspaceModuleRemovalService) Inspect(config domain.AppConfig, taskPath, workspacePath string) (WorkspaceModuleRemovalPreview, error) {
	var preview WorkspaceModuleRemovalPreview
	err := s.Locks.WithTask(taskPath, func() error {
		manifest, err := s.Manifests.Load(taskPath)
		if err != nil {
			return err
		}
		if err := s.requireTaskDirectory(config, taskPath, manifest); err != nil {
			return err
		}
		ws, ok := physicalWorkspace(manifest, workspacePath)
		if !ok {
			return fmt.Errorf("任务中不存在指定模块：%s", workspacePath)
		}
		var inner error
		preview, inner = s.inspectUnlocked(config, taskPath, manifest, ws, false)
		return inner
	})
	return preview, err
}

func (s *WorkspaceModuleRemovalService) Remove(_ context.Context, config domain.AppConfig, taskPath string, preview WorkspaceModuleRemovalPreview, confirmation WorkspaceModuleRemovalConfirmation) (WorkspaceModuleRemovalResult, error) {
	var result WorkspaceModuleRemovalResult
	err := s.Locks.WithTask(taskPath, func() error {
		manifest, err := s.Manifests.Load(taskPath)
		if err != nil {
			return err
		}
		if err := s.requireTaskDirectory(config, taskPath, manifest); err != nil {
			return err
		}
		if len(manifest.Services) <= 1 {
			return fmt.Errorf("不能删除任务的最后一个工作区，请使用删除任务流程")
		}
		ws, ok := physicalWorkspace(manifest, preview.WorkspacePath)
		if !ok {
			return fmt.Errorf("任务中不存在指定模块：%s", preview.WorkspacePath)
		}
		target := mustAbs(ws.WorktreePath)
		if err := requireTaskChild(taskPath, target); err != nil {
			return err
		}
		lockRoot, err := s.repositoryLockRoot(config, ws, target)
		if err != nil {
			return err
		}
		execute := func() error {
			current, err := s.inspectUnlocked(config, taskPath, manifest, ws, true)
			if err != nil {
				return err
			}
			if current.Fingerprint != preview.Fingerprint || confirmation.Fingerprint != preview.Fingerprint {
				return fmt.Errorf("工作区状态已变化，请重新检查后确认")
			}
			if current.RequiresRiskConfirmation && !confirmation.AcknowledgeDataLoss {
				return fmt.Errorf("该模块存在未提交文件、提交差异或未推送提交，必须确认永久丢失风险")
			}
			var inner error
			result, inner = s.removeUnlocked(config, taskPath, manifest, ws, current.PathMissing)
			return inner
		}
		if lockRoot == "" {
			return execute()
		}
		return s.Locks.WithRepository(lockRoot, execute)
	})
	return result, err
}

func (s *WorkspaceModuleRemovalService) inspectUnlocked(config domain.AppConfig, taskPath string, manifest domain.TaskManifest, ws domain.ServiceWorkspace, repoLocked bool) (WorkspaceModuleRemovalPreview, error) {
	target := mustAbs(ws.WorktreePath)
	if err := requireTaskChild(taskPath, target); err != nil {
		return WorkspaceModuleRemovalPreview{}, err
	}
	if !fileExists(target) {
		return missingModulePreview(ws), nil
	}
	lockRoot, err := s.repositoryLockRoot(config, ws, target)
	if err != nil {
		return WorkspaceModuleRemovalPreview{}, err
	}
	inspect := func() (WorkspaceModuleRemovalPreview, error) {
		top, err := s.Git.TopLevel(target)
		if err != nil || !samePath(top, target) {
			return WorkspaceModuleRemovalPreview{}, fmt.Errorf("路径存在但不是有效的 Git 工作区，禁止自动删除：%s", target)
		}
		repository, hasRepo := repositoryByID(config, ws.RepositoryID)
		switch ws.Strategy {
		case domain.WorkspaceStandardWorktree:
			if !hasRepo {
				return WorkspaceModuleRemovalPreview{}, fmt.Errorf("模块仓库配置不存在：%s", ws.RepositoryID)
			}
			common, e := s.Git.CommonDirectory(target)
			if e != nil || !samePath(common, repository.GitCommonDirectory) {
				return WorkspaceModuleRemovalPreview{}, fmt.Errorf("Worktree Git 身份与任务记录不一致")
			}
			if err := s.Git.PruneWorktrees(repository.RootPath); err != nil {
				return WorkspaceModuleRemovalPreview{}, err
			}
			worktrees, err := s.Git.Worktrees(repository.RootPath)
			if err != nil {
				return WorkspaceModuleRemovalPreview{}, err
			}
			recordFound := false
			for _, wt := range worktrees {
				if samePath(wt.Path, target) {
					recordFound = true
					if wt.Locked {
						return WorkspaceModuleRemovalPreview{}, fmt.Errorf("Worktree 已锁定，请先手工 unlock")
					}
					break
				}
			}
			if !recordFound {
				return WorkspaceModuleRemovalPreview{}, fmt.Errorf("Git 未登记该 Worktree，禁止自动删除")
			}
		case domain.WorkspaceIndependentClone:
			remote, e := s.Git.RemoteURL(target, "origin")
			if e != nil || strings.TrimSpace(remote) != strings.TrimSpace(stringValue(ws.OriginURL)) {
				return WorkspaceModuleRemovalPreview{}, fmt.Errorf("独立克隆 origin 与任务记录不一致")
			}
			if err := requireOwnedClone(target, cloneOwnership(taskPath, ws.RepositoryID, ws.GroupServiceID, ws.ModuleID)); err != nil {
				return WorkspaceModuleRemovalPreview{}, err
			}
		default:
			return WorkspaceModuleRemovalPreview{}, fmt.Errorf("未知工作区策略：%s", ws.Strategy)
		}
		branch, err := s.Git.CurrentBranch(target)
		if err != nil {
			return WorkspaceModuleRemovalPreview{}, err
		}
		if branch == "" {
			return WorkspaceModuleRemovalPreview{}, fmt.Errorf("Detached HEAD 工作区不能自动删除")
		}
		if branch != ws.Branch {
			return WorkspaceModuleRemovalPreview{}, fmt.Errorf("当前分支与任务记录不一致：%s != %s", branch, ws.Branch)
		}
		status, err := s.Git.Status(target)
		if err != nil {
			return WorkspaceModuleRemovalPreview{}, err
		}
		if status.OperationInProgress != "" {
			return WorkspaceModuleRemovalPreview{}, fmt.Errorf("存在进行中的 Git 操作，完成或中止后再删除")
		}
		if ws.BaseRef == nil || strings.TrimSpace(*ws.BaseRef) == "" {
			return WorkspaceModuleRemovalPreview{}, fmt.Errorf("任务记录缺少主分支，无法可靠检查删除风险")
		}
		base, err := ParseRemoteBranchRef(*ws.BaseRef)
		if err != nil {
			return WorkspaceModuleRemovalPreview{}, err
		}
		if err := s.Git.Fetch(target, base.Remote); err != nil {
			return WorkspaceModuleRemovalPreview{}, err
		}
		// Fetch may change the upstream commit count used by both the risk
		// summary and confirmation fingerprint. Read the refreshed state once.
		status, err = s.Git.Status(target)
		if err != nil {
			return WorkspaceModuleRemovalPreview{}, err
		}
		if status.OperationInProgress != "" {
			return WorkspaceModuleRemovalPreview{}, fmt.Errorf("存在进行中的 Git 操作，完成或中止后再删除")
		}
		refExists, err := s.Git.RefExists(target, "refs/remotes/"+*ws.BaseRef)
		if err != nil {
			return WorkspaceModuleRemovalPreview{}, err
		}
		if !refExists {
			return WorkspaceModuleRemovalPreview{}, fmt.Errorf("远程主分支不存在：%s", *ws.BaseRef)
		}
		countsResult, err := s.Git.Run(target, "rev-list", "--left-right", "--count", *ws.BaseRef+"...HEAD")
		if err != nil {
			return WorkspaceModuleRemovalPreview{}, err
		}
		parts := strings.Fields(countsResult.Stdout)
		if len(parts) != 2 {
			return WorkspaceModuleRemovalPreview{}, fmt.Errorf("无法读取当前 HEAD 相对主分支的差异")
		}
		behind, err := strconv.Atoi(parts[0])
		if err != nil {
			return WorkspaceModuleRemovalPreview{}, err
		}
		ahead, err := strconv.Atoi(parts[1])
		if err != nil {
			return WorkspaceModuleRemovalPreview{}, err
		}
		filesResult, err := s.Git.Run(target, "status", "--short", "--untracked-files=all")
		if err != nil {
			return WorkspaceModuleRemovalPreview{}, err
		}
		files := nonEmptyLines(filesResult.Stdout)
		head, err := s.Git.Resolve(target, "HEAD")
		if err != nil {
			return WorkspaceModuleRemovalPreview{}, err
		}
		fingerprint := moduleRemovalFingerprint(ws, head, parts, status, files)
		risky := status.Staged || status.Unstaged || status.Untracked || ahead > 0 || status.UnpushedCommits > 0
		return WorkspaceModuleRemovalPreview{WorkspacePath: filepath.Clean(target), ServiceID: ws.GroupServiceID, ModuleID: ws.ModuleID, ServiceName: ws.ServiceName, ModuleName: ws.ModuleName, Branch: branch, BaseRef: ws.BaseRef, Staged: status.Staged, Unstaged: status.Unstaged, Untracked: status.Untracked, ChangedFiles: files, CommitsAheadOfBase: ahead, CommitsBehindBase: behind, UnpushedCommits: status.UnpushedCommits, RequiresRiskConfirmation: risky, Fingerprint: fingerprint}, nil
	}
	if repoLocked {
		return inspect()
	}
	var preview WorkspaceModuleRemovalPreview
	err = s.Locks.WithRepository(lockRoot, func() error { var inner error; preview, inner = inspect(); return inner })
	return preview, err
}

func (s *WorkspaceModuleRemovalService) removeUnlocked(config domain.AppConfig, taskPath string, manifest domain.TaskManifest, ws domain.ServiceWorkspace, pathMissing bool) (WorkspaceModuleRemovalResult, error) {
	target := mustAbs(ws.WorktreePath)
	if pathMissing {
		if fileExists(target) {
			return WorkspaceModuleRemovalResult{}, fmt.Errorf("工作区状态已变化，请重新检查后确认：%s", target)
		}
	} else if !fileExists(target) {
		return WorkspaceModuleRemovalResult{}, fmt.Errorf("工作区状态已变化，请重新检查后确认：%s", target)
	}
	backup := ""
	if !pathMissing {
		backup = filepath.Join(filepath.Dir(target), filepath.Base(target)+fmt.Sprintf(".silverwing-delete-%d", s.now().UnixMilli()))
	}
	repository, hasRepo := repositoryByID(config, ws.RepositoryID)
	if ws.Strategy == domain.WorkspaceStandardWorktree && !hasRepo {
		return WorkspaceModuleRemovalResult{}, fmt.Errorf("模块仓库配置不存在：%s", ws.RepositoryID)
	}
	if backup != "" {
		var err error
		if ws.Strategy == domain.WorkspaceStandardWorktree {
			err = s.Git.MoveWorktree(repository.RootPath, target, backup)
		} else {
			err = os.Rename(target, backup)
		}
		if err != nil {
			return WorkspaceModuleRemovalResult{}, err
		}
	}
	updated := manifest
	updated.UpdatedAt = domain.FormatSilverWingTime(s.now())
	remaining := make([]domain.ServiceWorkspace, 0, len(manifest.Services)-1)
	for _, candidate := range manifest.Services {
		if !samePath(candidate.WorktreePath, ws.WorktreePath) {
			remaining = append(remaining, candidate)
		}
	}
	updated.Services = remaining
	rollback := func(primary error) error {
		var failures []error
		if err := s.Manifests.Save(taskPath, manifest); err != nil {
			failures = append(failures, err)
		}
		if _, err := s.Documents.WriteTaskDocument(taskPath, manifest, config); err != nil {
			failures = append(failures, err)
		}
		if backup != "" && fileExists(backup) {
			var err error
			if ws.Strategy == domain.WorkspaceStandardWorktree {
				err = s.Git.MoveWorktree(repository.RootPath, backup, target)
			} else {
				err = os.Rename(backup, target)
			}
			if err != nil {
				failures = append(failures, err)
			}
		}
		if len(failures) > 0 {
			return fmt.Errorf("模块移除失败且自动恢复未完成：%w", errors.Join(append([]error{primary}, failures...)...))
		}
		return primary
	}
	if err := s.Manifests.Save(taskPath, updated); err != nil {
		return WorkspaceModuleRemovalResult{}, rollback(err)
	}
	if _, err := s.Documents.WriteTaskDocument(taskPath, updated, config); err != nil {
		return WorkspaceModuleRemovalResult{}, rollback(err)
	}
	if ws.Strategy == domain.WorkspaceStandardWorktree {
		cleanupErr := s.Git.PruneWorktrees(repository.RootPath)
		if cleanupErr == nil && backup != "" {
			cleanupErr = s.Git.RemoveWorktree(repository.RootPath, backup, true)
		}
		if cleanupErr == nil {
			cleanupErr = s.Git.PruneWorktrees(repository.RootPath)
		}
		if cleanupErr != nil {
			return WorkspaceModuleRemovalResult{Manifest: updated, RetainedBackupPath: backup, CleanupError: cleanupErr.Error()}, nil
		}
		return WorkspaceModuleRemovalResult{Manifest: updated}, nil
	}
	if backup == "" {
		return WorkspaceModuleRemovalResult{Manifest: updated}, nil
	}
	if err := deleteOwnedClone(taskPath, backup, cloneOwnership(taskPath, ws.RepositoryID, ws.GroupServiceID, ws.ModuleID)); err != nil {
		return WorkspaceModuleRemovalResult{Manifest: updated, RetainedBackupPath: backup, CleanupError: err.Error()}, nil
	}
	return WorkspaceModuleRemovalResult{Manifest: updated}, nil
}

func (s *WorkspaceModuleRemovalService) repositoryLockRoot(config domain.AppConfig, ws domain.ServiceWorkspace, target string) (string, error) {
	if ws.Strategy == domain.WorkspaceStandardWorktree {
		repository, ok := repositoryByID(config, ws.RepositoryID)
		if !ok {
			return "", fmt.Errorf("模块仓库配置不存在：%s", ws.RepositoryID)
		}
		return repository.GitCommonDirectory, nil
	}
	if fileExists(target) {
		return s.Git.CommonDirectory(target)
	}
	return "", nil
}

func (s *WorkspaceModuleRemovalService) requireTaskDirectory(config domain.AppConfig, taskPath string, manifest domain.TaskManifest) error {
	if config.TaskRoot == nil || !samePath(filepath.Dir(mustAbs(taskPath)), mustAbs(*config.TaskRoot)) || filepath.Base(taskPath) != manifest.TaskDirectoryName {
		return fmt.Errorf("任务目录与配置或任务清单不匹配：%s", taskPath)
	}
	return nil
}

func missingModulePreview(ws domain.ServiceWorkspace) WorkspaceModuleRemovalPreview {
	key := filepath.Clean(mustAbs(ws.WorktreePath))
	return WorkspaceModuleRemovalPreview{WorkspacePath: key, ServiceID: ws.GroupServiceID, ModuleID: ws.ModuleID, ServiceName: ws.ServiceName, ModuleName: ws.ModuleName, Branch: ws.Branch, BaseRef: ws.BaseRef, ChangedFiles: []string{}, PathMissing: true, Fingerprint: hashString("missing|" + key + "|" + ws.Branch)}
}

func moduleRemovalFingerprint(ws domain.ServiceWorkspace, head string, counts []string, status RepositoryStatus, files []string) string {
	return hashString(strings.Join([]string{filepath.Clean(mustAbs(ws.WorktreePath)), ws.Branch, stringValue(ws.BaseRef), head, strings.Join(counts, ","), fmt.Sprintf("%+v", status), strings.Join(files, "\n")}, "\x00"))
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return fmt.Sprintf("%x", sum[:])
}

func requireTaskChild(taskPath, target string) error {
	if !samePath(filepath.Dir(target), mustAbs(taskPath)) {
		return fmt.Errorf("拒绝操作任务目录之外的工作区：%s", target)
	}
	return nil
}

func (s *WorkspaceModuleRemovalService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
