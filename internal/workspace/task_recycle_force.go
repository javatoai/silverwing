package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"silverwing-go/internal/documents"
	"silverwing-go/internal/domain"
)

type forcedWorkspaceProof struct {
	workspace       domain.ServiceWorkspace
	target          string
	targetIdentity  string
	repositoryRoot  string
	commonDirectory string
	commonIdentity  string
	adminDirectory  string
	adminIdentity   string
	gitDirFile      string
	adminGitDirFile string
	branch          string
	head            string
	status          RepositoryStatus
	statusOutput    string
	forced          bool
	cloneOwner      cloneRecycleOwnership
}

type forceDeleteFingerprint struct {
	TaskPath      string
	TaskIdentity  string
	TaskDirectory string
	FolderName    string
	GroupID       string
	Requirement   string
	CreatedAt     string
	Workspaces    []forceWorkspaceFingerprint
}

type forceWorkspaceFingerprint struct {
	ServiceName     string
	RepositoryID    string
	Strategy        domain.WorkspaceStrategy
	RepositoryRoot  string
	CommonDirectory string
	CommonIdentity  string
	Target          string
	TargetIdentity  string
	Branch          string
	Head            string
	Status          RepositoryStatus
	StatusOutput    string
	OriginURL       string
	AdminDirectory  string
	AdminIdentity   string
	GitDirFile      string
	AdminGitDirFile string
	Forced          bool
	CloneOwner      cloneRecycleOwnership
	CloneRefs       string
}

func (s *TaskApplicationService) inspectForceDeletion(ctx context.Context, config domain.AppConfig, taskPath string, manifest domain.TaskManifest) (TaskForceDeletePreview, []forcedWorkspaceProof, []string, error) {
	preview := TaskForceDeletePreview{TaskPath: taskPath}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return preview, nil, nil, err
	}
	if err := s.Lifecycle.validateTaskDirectory(config, taskPath, manifest); err != nil {
		return preview, nil, nil, err
	}
	// Only inspect the recovery path for missing repository configuration or
	// for an archived clone without a current ownership marker.
	forceNeeded := false
	for _, ws := range physicalWorkspaces(manifest) {
		target, err := s.Lifecycle.validateTarget(taskPath, ws)
		if err != nil {
			preview.Reason = err.Error()
			return preview, nil, nil, nil
		}
		if _, err := os.Lstat(target); err == nil {
			_, configured := repositoryByID(config, ws.RepositoryID)
			if !configured {
				forceNeeded = true
				break
			}
			if manifest.LifecycleStatus == domain.TaskArchived && ws.Strategy == domain.WorkspaceIndependentClone {
				if _, ownerErr := os.Lstat(cloneMarker(target)); os.IsNotExist(ownerErr) {
					forceNeeded = true
					break
				}
			}
		} else if !os.IsNotExist(err) {
			preview.Reason = "无法检查工作区路径：" + target
			return preview, nil, nil, nil
		}
	}
	if !forceNeeded {
		return preview, nil, nil, nil
	}
	taskIdentity, err := documents.DirectoryFingerprint(taskPath)
	if err != nil {
		preview.Reason = "任务目录不安全或无法确认身份"
		return preview, nil, nil, nil
	}
	group, err := config.Group(manifest.GroupID)
	if err != nil {
		preview.Reason = "任务项目组配置不存在，无法验证工作区来源"
		return preview, nil, nil, nil
	}

	proofs := make([]forcedWorkspaceProof, 0, len(manifest.Services))
	knownRepositoryRoots := make(map[string]string)
	knownLockRoots := make(map[string]string)
	forced := false
	fingerprint := forceDeleteFingerprint{
		TaskPath: mustAbs(taskPath), TaskIdentity: taskIdentity,
		TaskDirectory: manifest.TaskDirectoryName, FolderName: manifest.FolderName,
		GroupID: manifest.GroupID, Requirement: manifest.RequirementLink, CreatedAt: manifest.CreatedAt,
	}
	recorded := make(map[string]bool)
	allWorkspacePaths := make([]string, 0, len(manifest.Services))
	for _, ws := range physicalWorkspaces(manifest) {
		if err := ctx.Err(); err != nil {
			return preview, nil, nil, err
		}
		target, err := s.Lifecycle.validateTarget(taskPath, ws)
		if err != nil {
			preview.Reason = err.Error()
			return preview, nil, nil, nil
		}
		info, err := os.Lstat(target)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil || !info.IsDir() {
			preview.Reason = "工作区不存在或不是安全目录：" + target
			return preview, nil, nil, nil
		}
		targetIdentity, err := documents.DirectoryFingerprint(target)
		if err != nil {
			preview.Reason = "工作区包含链接或无法确认身份：" + target
			return preview, nil, nil, nil
		}
		allWorkspacePaths = append(allWorkspacePaths, target)
		repository, configured := repositoryByID(config, ws.RepositoryID)
		proof := forcedWorkspaceProof{workspace: ws, target: target, targetIdentity: targetIdentity, forced: !configured}
		if !configured {
			forced = true
		}
		switch ws.Strategy {
		case domain.WorkspaceStandardWorktree:
			root := strings.TrimSpace(ws.RepositoryPath)
			expectedCommon, expectedOrigin := "", ""
			if configured {
				root = repository.RootPath
				expectedCommon = repository.GitCommonDirectory
				if repository.OriginURL != nil {
					expectedOrigin = strings.TrimSpace(*repository.OriginURL)
				}
			}
			if ws.OriginURL != nil && strings.TrimSpace(*ws.OriginURL) != "" {
				if expectedOrigin != "" && expectedOrigin != strings.TrimSpace(*ws.OriginURL) {
					preview.Reason = "仓库配置 origin 与任务工作区记录不一致：" + ws.ServiceName
					return preview, nil, nil, nil
				}
				expectedOrigin = strings.TrimSpace(*ws.OriginURL)
			}
			if err := s.validateLinkedWorktree(ctx, ws, target, root, expectedCommon, expectedOrigin, &proof); err != nil {
				preview.Reason = err.Error()
				return preview, nil, nil, nil
			}
		case domain.WorkspaceIndependentClone:
			if err := s.validateIndependentCloneForRecycle(ctx, ws, target); err != nil {
				preview.Reason = err.Error()
				return preview, nil, nil, nil
			}
			if strings.TrimSpace(ws.RepositoryPath) != "" {
				proof.repositoryRoot = mustAbs(ws.RepositoryPath)
			}
			cloneCommon, commonErr := gitCommonDirectoryContext(ctx, s.Git, target)
			if commonErr != nil || !samePath(cloneCommon, filepath.Join(target, ".git")) {
				preview.Reason = "无法确认独立克隆的 Git 目录：" + target
				return preview, nil, nil, nil
			}
			proof.adminDirectory = mustAbs(cloneCommon)
			proof.commonDirectory = proof.adminDirectory
			proof.commonIdentity, err = documents.DirectoryFingerprint(proof.commonDirectory)
			if err != nil {
				preview.Reason = "独立克隆 Git 目录不安全或无法确认身份：" + proof.commonDirectory
				return preview, nil, nil, nil
			}
			proof.cloneOwner, err = recycleCloneOwner(target, cloneOwnership(taskPath, ws.RepositoryID, ws.GroupServiceID, ws.ModuleID))
			if err != nil {
				preview.Reason = err.Error()
				return preview, nil, nil, nil
			}
			if proof.cloneOwner != cloneRecycleCurrent {
				if manifest.LifecycleStatus != domain.TaskArchived {
					preview.Reason = "仅归档任务可通过旧版克隆记录确认归属：" + target
					return preview, nil, nil, nil
				}
				proof.forced, forced = true, true
			}
			if proof.cloneOwner == cloneRecycleMissing {
				service, serviceErr := config.GroupService(manifest.GroupID, ws.GroupServiceID)
				if !configured || serviceErr != nil || service.RepositoryID != ws.RepositoryID ||
					!slices.ContainsFunc(service.Modules, func(module domain.ServiceModuleConfig) bool { return module.ID == ws.ModuleID }) ||
					strings.TrimSpace(stringValue(repository.OriginURL)) != strings.TrimSpace(stringValue(ws.OriginURL)) {
					preview.Reason = "克隆标记缺失，仓库配置与任务记录无法互相印证：" + target
					return preview, nil, nil, nil
				}
				if err := verifyUnmarkedArchivedCloneOrigin(target, strings.TrimSpace(stringValue(ws.OriginURL))); err != nil {
					preview.Reason = err.Error()
					return preview, nil, nil, nil
				}
			}
		default:
			preview.Reason = "工作区策略不受支持，无法安全回收：" + string(ws.Strategy)
			return preview, nil, nil, nil
		}
		if proof.repositoryRoot != "" && ws.Strategy == domain.WorkspaceStandardWorktree {
			rootKey := strings.ToLower(mustAbs(proof.repositoryRoot))
			knownRepositoryRoots[rootKey] = mustAbs(proof.repositoryRoot)
		}
		if proof.commonDirectory != "" {
			lockKey := strings.ToLower(mustAbs(proof.commonDirectory))
			knownLockRoots[lockKey] = mustAbs(proof.commonDirectory)
		}
		status, err := deleteRiskStatusContext(ctx, s.Git, target)
		if err != nil {
			preview.Reason = fmt.Sprintf("无法读取工作区状态，已停止强制回收：%s：%v", target, err)
			return preview, nil, nil, nil
		}
		if status.OperationInProgress != "" {
			preview.Reason = "工作区存在进行中的 Git 操作，不能回收：" + ws.ServiceName
			return preview, nil, nil, nil
		}
		proof.status = status
		proof.branch, proof.head, proof.statusOutput, err = workspaceStateFingerprint(ctx, s.Git, target)
		if err != nil {
			preview.Reason = fmt.Sprintf("无法核对工作区状态，已停止强制回收：%s：%v", target, err)
			return preview, nil, nil, nil
		}
		localOnly := 0
		cloneRefs := ""
		if ws.Strategy == domain.WorkspaceIndependentClone {
			cloneRefs, err = recycleCloneRefs(ctx, s.Git, target)
			if err != nil {
				preview.Reason = "无法核对独立克隆中的分支和引用：" + target
				return preview, nil, nil, nil
			}
			localOnly, err = deleteRiskLocalOnlyCommitCountContext(ctx, s.Git, target)
			if err != nil {
				preview.Reason = "无法核对独立克隆中的本地提交：" + target
				return preview, nil, nil, nil
			}
			if proof.cloneOwner == cloneRecycleMissing && (proof.branch != ws.Branch || status.HasUncommittedChanges() || status.UnpushedCommits > 0 || localOnly > 0) {
				preview.Reason = "克隆标记缺失且存在分支差异、本地改动或未推送提交：" + target
				return preview, nil, nil, nil
			}
		}
		if status.HasUncommittedChanges() || status.UnpushedCommits > 0 || localOnly > 0 {
			unpushed := status.UnpushedCommits
			if localOnly > unpushed {
				unpushed = localOnly
			}
			preview.Risks = append(preview.Risks, domain.DeleteRisk{
				ServiceName: ws.ServiceName, Staged: status.Staged, Unstaged: status.Unstaged,
				Untracked: status.Untracked, UnpushedCommits: unpushed,
			})
		}
		proof.target = target
		proofs = append(proofs, proof)
		recorded[strings.ToLower(filepath.Clean(target))] = true
		origin := ""
		if ws.OriginURL != nil {
			origin = strings.TrimSpace(*ws.OriginURL)
		}
		fingerprint.Workspaces = append(fingerprint.Workspaces, forceWorkspaceFingerprint{
			ServiceName: ws.ServiceName, RepositoryID: ws.RepositoryID, Strategy: ws.Strategy,
			RepositoryRoot: proof.repositoryRoot, CommonDirectory: proof.commonDirectory,
			CommonIdentity: proof.commonIdentity,
			Target:         target, TargetIdentity: targetIdentity, Branch: proof.branch, Head: proof.head,
			Status: proof.status, StatusOutput: proof.statusOutput, OriginURL: origin,
			AdminDirectory: proof.adminDirectory, AdminIdentity: proof.adminIdentity,
			GitDirFile: proof.gitDirFile, AdminGitDirFile: proof.adminGitDirFile, Forced: proof.forced, CloneOwner: proof.cloneOwner,
			CloneRefs: cloneRefs,
		})
	}
	if !forced {
		return preview, proofs, nil, nil
	}
	if len(preview.Workspaces) == 0 {
		preview.Workspaces = allWorkspacePaths
	}

	// Discover every configured or manifest-recorded repository represented by
	// the task's group before checking for unrecorded linked worktrees.
	groupRepositoryIDs := make(map[string]bool)
	for _, service := range group.Services {
		if groupRepositoryIDs[service.RepositoryID] {
			continue
		}
		groupRepositoryIDs[service.RepositoryID] = true
		repository, configured := repositoryByID(config, service.RepositoryID)
		root := ""
		common := ""
		if configured {
			root = mustAbs(repository.RootPath)
			common = mustAbs(repository.GitCommonDirectory)
		} else {
			hasStandardWorkspace := false
			for _, ws := range physicalWorkspaces(manifest) {
				if ws.RepositoryID == service.RepositoryID && ws.Strategy == domain.WorkspaceStandardWorktree {
					hasStandardWorkspace = true
				}
				if ws.RepositoryID == service.RepositoryID && ws.Strategy == domain.WorkspaceStandardWorktree && strings.TrimSpace(ws.RepositoryPath) != "" {
					candidate := mustAbs(ws.RepositoryPath)
					if root != "" && !samePath(root, candidate) {
						preview.Reason = "同一仓库 ID 对应多个来源路径，无法安全校验：" + service.RepositoryID
						return preview, nil, nil, nil
					}
					root = candidate
				}
			}
			if !hasStandardWorkspace {
				// An independently-owned clone is verified entirely from its own
				// .git directory, owner marker and origin. Its former source checkout
				// may no longer exist.
				continue
			}
		}
		if root == "" {
			// No workspace for this service was created in this task, so there
			// is no source root to enumerate. Nested Git roots are checked below.
			continue
		}
		root = mustAbs(root)
		_, rootErr := documents.DirectoryFingerprint(root)
		if configured && os.IsNotExist(rootErr) {
			// Configured repositories with no remaining checkout are handled like
			// the normal delete scan: there can be no registered target to verify.
			continue
		}
		if rootErr != nil {
			preview.Reason = "仓库来源目录不安全或无法确认身份：" + root
			return preview, nil, nil, nil
		}
		if !configured {
			top, err := s.Git.TopLevel(root)
			if err != nil || !samePath(top, root) {
				preview.Reason = "仓库来源目录与 Git 顶层不匹配：" + root
				return preview, nil, nil, nil
			}
			common, err = gitCommonDirectoryContext(ctx, s.Git, root)
			if err != nil {
				preview.Reason = "无法确认仓库 Git 目录：" + root
				return preview, nil, nil, nil
			}
			common = mustAbs(common)
			if _, err := documents.DirectoryFingerprint(common); err != nil {
				preview.Reason = "仓库 Git 公共目录不安全或无法确认身份：" + common
				return preview, nil, nil, nil
			}
		} else if _, err := documents.DirectoryFingerprint(common); err != nil {
			preview.Reason = "仓库 Git 公共目录不安全或无法确认身份：" + common
			return preview, nil, nil, nil
		}
		knownRepositoryRoots[strings.ToLower(root)] = root
		if common == "" {
			preview.Reason = "仓库配置缺少 Git 公共目录：" + root
			return preview, nil, nil, nil
		}
		knownLockRoots[strings.ToLower(common)] = common
	}
	if err := s.Lifecycle.requireNoUntrackedWorkspacesAtRoots(taskPath, recorded, valuesSorted(knownRepositoryRoots)); err != nil {
		preview.Reason = err.Error()
		return preview, nil, nil, nil
	}
	if err := s.Lifecycle.walkUnrecordedGitRoots(taskPath, recorded); err != nil {
		preview.Reason = err.Error()
		return preview, nil, nil, nil
	}
	for _, proof := range proofs {
		if proof.workspace.Strategy != domain.WorkspaceIndependentClone {
			continue
		}
		entries, err := s.Git.WorktreesContext(ctx, proof.target)
		if err != nil {
			preview.Reason = "无法检查独立克隆中的其他工作区：" + proof.target
			return preview, nil, nil, nil
		}
		for _, entry := range entries {
			if !samePath(entry.Path, proof.target) {
				preview.Reason = "独立克隆中存在未记录的 Linked Worktree：" + entry.Path
				return preview, nil, nil, nil
			}
		}
	}

	encoded, err := json.Marshal(fingerprint)
	if err != nil {
		return preview, nil, nil, err
	}
	sum := sha256.Sum256(encoded)
	preview.CanForce = true
	preview.Guard = hex.EncodeToString(sum[:])
	sort.Strings(preview.Workspaces)
	return preview, proofs, valuesSorted(knownLockRoots), nil
}

func valuesSorted(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return strings.ToLower(result[i]) < strings.ToLower(result[j]) })
	return result
}

func workspaceStateFingerprint(ctx context.Context, git *GitClient, target string) (branch, head, status string, err error) {
	branchResult, err := git.RunContext(ctx, target, 2*time.Minute, true, true, "branch", "--show-current")
	if err != nil {
		return "", "", "", err
	}
	headResult, err := git.RunContext(ctx, target, 2*time.Minute, true, true, "rev-parse", "HEAD")
	if err != nil {
		return "", "", "", err
	}
	status, err = recycleWorkspaceStatus(ctx, git, target)
	if err != nil {
		return "", "", "", err
	}
	return strings.TrimSpace(branchResult.Stdout), strings.TrimSpace(headResult.Stdout), status, nil
}

func (s *TaskApplicationService) validateLinkedWorktree(ctx context.Context, ws domain.ServiceWorkspace, target, root, expectedCommon, expectedOrigin string, proof *forcedWorkspaceProof) error {
	if strings.TrimSpace(root) == "" {
		return fmt.Errorf("Linked Worktree 缺少仓库来源路径：%s", target)
	}
	root = mustAbs(root)
	if _, err := documents.DirectoryFingerprint(root); err != nil {
		return fmt.Errorf("仓库来源目录不安全或无法确认身份：%s", root)
	}
	top, err := s.Git.RunContext(ctx, root, 2*time.Minute, true, true, "rev-parse", "--show-toplevel")
	if err != nil || !samePath(strings.TrimSpace(top.Stdout), root) {
		return fmt.Errorf("仓库来源目录与 Git 顶层不匹配：%s", root)
	}
	targetGitPath := filepath.Join(target, ".git")
	targetGitInfo, err := os.Lstat(targetGitPath)
	if err != nil || !targetGitInfo.Mode().IsRegular() {
		return fmt.Errorf("Linked Worktree 的 .git 登记文件不存在或格式错误：%s", target)
	}
	targetGitBytes, err := os.ReadFile(targetGitPath)
	if err != nil {
		return fmt.Errorf("无法读取 Linked Worktree 登记文件：%s", target)
	}
	targetGitLine := strings.TrimSpace(string(targetGitBytes))
	key, value, ok := strings.Cut(targetGitLine, ":")
	if !ok || !strings.EqualFold(strings.TrimSpace(key), "gitdir") || strings.TrimSpace(value) == "" {
		return fmt.Errorf("Linked Worktree 的 .git 登记格式错误：%s", target)
	}
	actualCommon, err := gitCommonDirectoryContext(ctx, s.Git, target)
	if err != nil {
		return fmt.Errorf("无法读取 Linked Worktree 公共 Git 目录：%s", target)
	}
	actualCommon = mustAbs(actualCommon)
	if expectedCommon != "" && !samePath(actualCommon, expectedCommon) {
		return fmt.Errorf("Linked Worktree 不属于配置的主仓库：%s", target)
	}
	rootCommon, err := gitCommonDirectoryContext(ctx, s.Git, root)
	if err != nil || !samePath(actualCommon, rootCommon) {
		return fmt.Errorf("Linked Worktree 不属于任务记录的主仓库：%s", target)
	}
	commonIdentity, err := documents.DirectoryFingerprint(actualCommon)
	if err != nil {
		return fmt.Errorf("Git 公共目录不安全或无法确认身份：%s", actualCommon)
	}
	origin, err := gitRemoteURLContext(ctx, s.Git, root, "origin")
	if err != nil {
		return fmt.Errorf("无法核对主仓库 origin：%s", root)
	}
	if expectedOrigin != "" && strings.TrimSpace(origin) != expectedOrigin {
		return fmt.Errorf("主仓库 origin 与任务记录不匹配：%s", root)
	}
	if proof.forced && expectedOrigin == "" {
		return fmt.Errorf("Linked Worktree 缺少可核验的任务 origin 记录：%s", target)
	}
	branchResult, err := s.Git.RunContext(ctx, target, 2*time.Minute, true, true, "branch", "--show-current")
	if err != nil || strings.TrimSpace(branchResult.Stdout) != ws.Branch {
		return fmt.Errorf("Linked Worktree 分支与任务记录不匹配：%s", target)
	}
	headResult, err := s.Git.RunContext(ctx, target, 2*time.Minute, true, true, "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("无法读取 Linked Worktree 提交：%s", target)
	}
	adminResult, err := s.Git.RunContext(ctx, target, 2*time.Minute, true, true, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return fmt.Errorf("无法读取 Linked Worktree 登记路径：%s", target)
	}
	adminDirectory := mustAbs(strings.TrimSpace(adminResult.Stdout))
	if !samePath(filepath.Dir(adminDirectory), filepath.Join(actualCommon, "worktrees")) {
		return fmt.Errorf("Linked Worktree 登记路径不在主仓库管理目录：%s", adminDirectory)
	}
	adminIdentity, err := documents.DirectoryFingerprint(adminDirectory)
	if err != nil {
		return fmt.Errorf("Linked Worktree 登记目录不安全或无法确认身份：%s", adminDirectory)
	}
	adminGitDirPath := filepath.Join(adminDirectory, "gitdir")
	adminGitDirInfo, err := os.Lstat(adminGitDirPath)
	if err != nil || !adminGitDirInfo.Mode().IsRegular() {
		return fmt.Errorf("主仓库 worktree 反向登记缺失或不安全：%s", target)
	}
	adminGitDirBytes, err := os.ReadFile(adminGitDirPath)
	if err != nil || !samePath(resolveGitFilePath(adminDirectory, strings.TrimSpace(string(adminGitDirBytes))), targetGitPath) {
		return fmt.Errorf("主仓库 worktree 登记未反向指向任务工作区：%s", target)
	}
	if !samePath(resolveGitFilePath(target, strings.TrimSpace(value)), adminDirectory) {
		return fmt.Errorf("任务工作区 .git 登记未指向主仓库 worktree 目录：%s", target)
	}
	commonFileInfo, err := os.Lstat(filepath.Join(adminDirectory, "commondir"))
	if err != nil || !commonFileInfo.Mode().IsRegular() {
		return fmt.Errorf("Linked Worktree 公共目录登记缺失或不安全：%s", target)
	}
	commondirBytes, err := os.ReadFile(filepath.Join(adminDirectory, "commondir"))
	if err != nil || !samePath(resolveGitFilePath(adminDirectory, strings.TrimSpace(string(commondirBytes))), actualCommon) {
		return fmt.Errorf("Linked Worktree 公共目录登记不匹配：%s", target)
	}
	entries, err := s.Git.WorktreesContext(ctx, root)
	if err != nil {
		return fmt.Errorf("无法查询主仓库 worktree 登记：%s", root)
	}
	registered := 0
	for _, entry := range entries {
		if !samePath(entry.Path, target) {
			continue
		}
		registered++
		if entry.Bare || entry.Detached || entry.Locked || entry.Branch != ws.Branch || !strings.EqualFold(entry.Head, strings.TrimSpace(headResult.Stdout)) {
			return fmt.Errorf("主仓库 worktree 登记与任务记录不匹配：%s", target)
		}
	}
	if registered != 1 {
		return fmt.Errorf("主仓库中找不到唯一对应的 worktree 登记：%s", target)
	}
	proof.repositoryRoot = root
	proof.commonDirectory = actualCommon
	proof.commonIdentity = commonIdentity
	proof.adminDirectory = adminDirectory
	proof.adminIdentity = adminIdentity
	proof.gitDirFile = targetGitLine
	proof.adminGitDirFile = strings.TrimSpace(string(adminGitDirBytes))
	proof.branch = ws.Branch
	proof.head = strings.TrimSpace(headResult.Stdout)
	return nil
}

func resolveGitFilePath(base, value string) string {
	if !filepath.IsAbs(value) {
		value = filepath.Join(base, value)
	}
	return mustAbs(value)
}

func (s *TaskApplicationService) validateIndependentCloneForRecycle(ctx context.Context, ws domain.ServiceWorkspace, target string) error {
	if ws.OriginURL == nil || strings.TrimSpace(*ws.OriginURL) == "" {
		return fmt.Errorf("独立克隆缺少任务记录的 origin URL：%s", target)
	}
	gitDirectory, err := os.Lstat(filepath.Join(target, ".git"))
	if err != nil || !gitDirectory.IsDir() {
		return fmt.Errorf("独立克隆 .git 目录不存在或不安全：%s", target)
	}
	top, err := s.Git.TopLevel(target)
	if err != nil || !samePath(top, target) {
		return fmt.Errorf("独立克隆 Git 顶层与任务记录不匹配：%s", target)
	}
	origin, err := gitRemoteURLContext(ctx, s.Git, target, "origin")
	if err != nil || strings.TrimSpace(origin) != strings.TrimSpace(*ws.OriginURL) {
		return fmt.Errorf("独立克隆 origin 与任务记录不匹配：%s", target)
	}
	return nil
}

func validateForceDeleteApproval(preview TaskForceDeletePreview, guard string) error {
	if !preview.CanForce {
		if preview.Reason != "" {
			return errors.New(preview.Reason)
		}
		return errors.New("未找到可安全强制回收的工作区")
	}
	if guard == "" || preview.Guard != guard {
		return errors.New("工作区身份或 Git 状态已变化，请重新检查")
	}
	return nil
}

func (s *TaskApplicationService) recycleVerifiedTask(ctx context.Context, config domain.AppConfig, taskPath string, manifest domain.TaskManifest, roots []string, guard string, before func() error) ([]string, error) {
	l := s.Lifecycle
	var warnings []string
	err := l.withRepositoryLocks(roots, 0, func() error {
		current, proofs, currentRoots, err := s.inspectForceDeletion(ctx, config, taskPath, manifest)
		if err != nil {
			return err
		}
		if err := validateForceDeleteApproval(current, guard); err != nil {
			return err
		}
		if !slices.Equal(currentRoots, roots) {
			return errors.New("工作区仓库范围已变化，请重新检查")
		}
		warnings, err = l.recycleTaskWithRecovery(ctx, config, taskPath, manifest, proofs, before, func() error {
			latest, _, latestRoots, err := s.inspectForceDeletion(ctx, config, taskPath, manifest)
			if err != nil {
				return err
			}
			if !slices.Equal(latestRoots, roots) {
				return errors.New("工作区仓库范围已变化，请重新检查")
			}
			return validateForceDeleteApproval(latest, guard)
		})
		return err
	})
	return warnings, err
}

func removeWorktreeRegistrationOnly(git *GitClient, proof forcedWorkspaceProof) error {
	if _, err := os.Lstat(proof.target); !os.IsNotExist(err) {
		return fmt.Errorf("任务已回收，工作区路径重新出现，保留 Git 登记：%s", proof.target)
	}
	worktreeAdminRoot := filepath.Join(proof.commonDirectory, "worktrees")
	admin := proof.adminDirectory
	if !filepath.IsAbs(admin) || !filepath.IsAbs(proof.commonDirectory) || !samePath(filepath.Dir(admin), worktreeAdminRoot) {
		return fmt.Errorf("任务已回收，Git 登记路径不安全，保留登记：%s", admin)
	}
	adminIdentity, err := documents.DirectoryFingerprint(admin)
	if err != nil || adminIdentity != proof.adminIdentity {
		return fmt.Errorf("任务已回收，Git 登记目录不安全或不存在：%s", admin)
	}
	commonIdentity, err := documents.DirectoryFingerprint(proof.commonDirectory)
	if err != nil || commonIdentity != proof.commonIdentity {
		return fmt.Errorf("任务已回收，Git 公共目录身份已变化，保留登记：%s", proof.commonDirectory)
	}
	if _, err := os.Lstat(filepath.Join(admin, "locked")); err == nil {
		return fmt.Errorf("任务已回收，worktree 登记已锁定，保留登记：%s", proof.target)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("任务已回收，无法核对 worktree 登记锁定状态：%w", err)
	}
	gitDirBytes, err := os.ReadFile(filepath.Join(admin, "gitdir"))
	if err != nil || strings.TrimSpace(string(gitDirBytes)) != proof.adminGitDirFile ||
		!samePath(resolveGitFilePath(admin, strings.TrimSpace(string(gitDirBytes))), filepath.Join(proof.target, ".git")) {
		return fmt.Errorf("任务已回收，Git 登记目标已变化，保留登记：%s", proof.target)
	}
	commondirBytes, err := os.ReadFile(filepath.Join(admin, "commondir"))
	if err != nil || !samePath(resolveGitFilePath(admin, strings.TrimSpace(string(commondirBytes))), proof.commonDirectory) {
		return fmt.Errorf("任务已回收，Git 公共目录登记已变化，保留登记：%s", proof.target)
	}
	entries, err := git.Worktrees(proof.repositoryRoot)
	if err != nil {
		return fmt.Errorf("任务已回收，无法复核 worktree 登记：%s", proof.target)
	}
	registered := 0
	for _, entry := range entries {
		if samePath(entry.Path, proof.target) && !entry.Locked && entry.Branch == proof.branch && strings.EqualFold(entry.Head, proof.head) {
			registered++
		}
	}
	if registered != 1 {
		return fmt.Errorf("任务已回收，worktree 登记发生变化，保留登记：%s", proof.target)
	}
	if _, err := os.Lstat(proof.target); !os.IsNotExist(err) {
		return fmt.Errorf("任务已回收，工作区路径重新出现，保留 Git 登记：%s", proof.target)
	}
	if current, err := documents.DirectoryFingerprint(admin); err != nil || current != proof.adminIdentity {
		return fmt.Errorf("任务已回收，Git 登记目录身份已变化，保留登记：%s", admin)
	}
	if err := os.RemoveAll(admin); err != nil {
		return fmt.Errorf("任务已回收，worktree 登记清理失败：%s：%w", proof.target, err)
	}
	return nil
}

func (l *WorkspaceLifecycle) requireNoUntrackedWorkspacesAtRoots(taskPath string, recorded map[string]bool, repositories []string) error {
	task, err := filepath.Abs(taskPath)
	if err != nil {
		return err
	}
	for _, repository := range repositories {
		entries, err := l.Git.Worktrees(repository)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			path := filepath.Clean(entry.Path)
			if isWithin(path, task) && !recorded[strings.ToLower(path)] {
				return fmt.Errorf("任务目录中存在未记录的 Linked Worktree，请先人工处理后再删除任务：%s", path)
			}
		}
	}
	return nil
}
