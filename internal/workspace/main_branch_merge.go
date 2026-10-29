package workspace

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/storage"
)

type WorkspaceMainBranch struct {
	DisplayRef string `json:"displayRef"`
	LocalRef   string `json:"localRef"`
}

func (b WorkspaceMainBranch) Local() (domain.RemoteBranchRef, error) {
	return ParseRemoteBranchRef(b.LocalRef)
}

type WorkspaceMainBranchMergeOutcome string

const (
	MainBranchUpToDate      WorkspaceMainBranchMergeOutcome = "UP_TO_DATE"
	MainBranchFastForwarded WorkspaceMainBranchMergeOutcome = "FAST_FORWARDED"
	MainBranchMerged        WorkspaceMainBranchMergeOutcome = "MERGED"
	MainBranchConflict      WorkspaceMainBranchMergeOutcome = "CONFLICT"
	MainBranchFailed        WorkspaceMainBranchMergeOutcome = "FAILED"
)

type WorkspaceMainBranchMergeResult struct {
	WorkspacePath string                          `json:"workspacePath"`
	ServiceName   string                          `json:"serviceName"`
	ModuleName    string                          `json:"moduleName"`
	SourceRef     string                          `json:"sourceRef"`
	TargetBranch  string                          `json:"targetBranch"`
	Outcome       WorkspaceMainBranchMergeOutcome `json:"outcome"`
	Message       string                          `json:"message"`
	ConflictFiles []string                        `json:"conflictFiles"`
}

type WorkspaceMainBranchMergeService struct {
	Git           *GitClient
	Manifests     TaskManifestReader
	Locks         *OperationLocks
	Lifecycle     *WorkspaceLifecycle
	GitOperations *WorkspaceGitOperationService
}

func NewWorkspaceMainBranchMergeService(git *GitClient, manifests TaskManifestReader, locks *OperationLocks, lifecycle *WorkspaceLifecycle) *WorkspaceMainBranchMergeService {
	if git == nil {
		git = NewGitClient(nil, "")
	}
	if manifests == nil {
		manifests = storage.NewManifestStore()
	}
	if locks == nil {
		locks = NewOperationLocks("")
	}
	if lifecycle == nil {
		lifecycle = NewWorkspaceLifecycle(manifests, git, locks)
	}
	return &WorkspaceMainBranchMergeService{Git: git, Manifests: manifests, Locks: locks, Lifecycle: lifecycle, GitOperations: NewWorkspaceGitOperationService(git, locks)}
}

func ResolveWorkspaceMainBranch(config domain.AppConfig, task domain.TaskManifest, ws domain.ServiceWorkspace) (WorkspaceMainBranch, bool, error) {
	var saved *string = ws.BaseRef
	if saved == nil {
		service, err := config.GroupService(task.GroupID, ws.GroupServiceID)
		if err == nil {
			module := findModule(service.Modules, ws.ModuleID)
			if module != nil {
				branch := service.EffectiveMasterBranch(*module)
				saved = &branch
			} else {
				saved = &service.MasterBranch
			}
		}
	}
	if saved == nil {
		return WorkspaceMainBranch{}, false, nil
	}
	ref, err := ParseRemoteBranchRef(*saved)
	if err != nil {
		return WorkspaceMainBranch{}, false, err
	}
	local := ref
	if ws.Strategy == domain.WorkspaceIndependentClone {
		local = domain.RemoteBranchRef{Remote: "origin", Branch: ref.Branch}
	}
	return WorkspaceMainBranch{DisplayRef: FormatRemoteBranchRef(ref), LocalRef: FormatRemoteBranchRef(local)}, true, nil
}

func (s *WorkspaceMainBranchMergeService) Merge(ctx context.Context, config domain.AppConfig, taskPath string, expectedTask domain.TaskManifest, requested domain.ServiceWorkspace) (WorkspaceMainBranchMergeResult, error) {
	var result WorkspaceMainBranchMergeResult
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	err := s.Locks.WithTask(taskPath, func() error {
		task, err := s.Manifests.Load(taskPath)
		if err != nil {
			return err
		}
		if task.TaskDirectoryName != expectedTask.TaskDirectoryName || task.CreatedAt != expectedTask.CreatedAt || task.GroupID != expectedTask.GroupID || task.FeatureBranch != expectedTask.FeatureBranch {
			return fmt.Errorf("任务身份已变化，请重新选择任务后再试")
		}
		if task.LifecycleStatus != domain.TaskActive {
			return fmt.Errorf("归档任务不能合并远程主分支")
		}
		var ws *domain.ServiceWorkspace
		for i := range task.Services {
			if task.Services[i].GroupServiceID == requested.GroupServiceID && task.Services[i].ModuleID == requested.ModuleID {
				ws = &task.Services[i]
				break
			}
		}
		if ws == nil {
			return fmt.Errorf("任务中不存在指定服务模块")
		}
		if !reflect.DeepEqual(*ws, requested) {
			return fmt.Errorf("任务工作区记录已变化，请刷新后再试")
		}
		if ws.Health != domain.WorkspaceReady && ws.Health != domain.WorkspaceReadyWithWarnings {
			return fmt.Errorf("工作区尚未就绪，不能合并远程主分支")
		}
		if ws.TargetBranch == nil || *ws.TargetBranch != ws.Branch {
			return fmt.Errorf("该工作区没有任务特性分支，不能合并远程主分支")
		}
		source, ok, err := ResolveWorkspaceMainBranch(config, task, *ws)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("任务记录和服务配置均未提供主分支")
		}
		remote, err := source.Local()
		if err != nil {
			return err
		}
		if strings.HasPrefix(remote.Remote, "-") {
			return fmt.Errorf("远程名称不合法：%s", remote.Remote)
		}
		if ws.Branch == remote.Branch {
			return fmt.Errorf("当前工作区使用主分支，不能作为特性分支执行合并")
		}
		validated, err := s.Lifecycle.ValidateForMutationContext(ctx, config, taskPath, task, *ws)
		if err != nil {
			return err
		}
		common, err := gitLifecycleCommonDirectoryContext(ctx, s.Git, validated.Repository)
		if err != nil {
			return err
		}
		return s.Locks.WithRepository(common, func() error {
			validated, err = s.Lifecycle.ValidateForMutationContext(ctx, config, taskPath, task, *ws)
			if err != nil {
				return err
			}
			if !samePath(mustAbs(taskPath), mustAbs(filepathDir(validated.Worktree))) {
				return fmt.Errorf("工作区实际路径不属于当前任务目录，已拒绝合并")
			}
			if ws.Strategy == domain.WorkspaceIndependentClone {
				if err := requireOwnedClone(validated.Worktree, cloneOwnership(taskPath, ws.RepositoryID, ws.GroupServiceID, ws.ModuleID)); err != nil {
					return err
				}
			}
			before, err := s.GitOperations.PreviewContext(ctx, *ws)
			if err != nil {
				return err
			}
			if err := requireGitWriteAllowed(before.Branch, config.BlockedGitWriteBranches, "合并远程主分支"); err != nil {
				return err
			}
			if len(before.Files) > 0 {
				return fmt.Errorf("工作区存在未提交或未跟踪修改，请先提交或自行处理后再合并")
			}
			remoteURL, err := gitRemoteURLContext(ctx, s.Git, validated.Worktree, remote.Remote)
			if err != nil {
				return err
			}
			if strings.TrimSpace(remoteURL) == "" {
				return fmt.Errorf("主分支远程不存在：%s", remote.Remote)
			}
			refspec := "refs/heads/" + remote.Branch + ":refs/remotes/" + source.LocalRef
			if _, err := s.Git.RunContext(ctx, validated.Worktree, 5*time.Minute, true, false, "fetch", "--prune", "--no-tags", remote.Remote, refspec); err != nil {
				return err
			}
			if _, err := s.Lifecycle.ValidateForMutationContext(ctx, config, taskPath, task, *ws); err != nil {
				return err
			}
			current, err := s.GitOperations.PreviewContext(ctx, *ws)
			if err != nil {
				return err
			}
			if err := requireGitWriteAllowed(current.Branch, config.BlockedGitWriteBranches, "合并远程主分支"); err != nil {
				return err
			}
			if current.Head != before.Head || current.Branch != before.Branch || len(current.Files) > 0 {
				return fmt.Errorf("拉取期间工作区状态已变化，请检查后重新合并")
			}
			qualified := "refs/remotes/" + source.LocalRef
			exists, err := gitRefExistsContext(ctx, s.Git, validated.Worktree, qualified)
			if err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("远程主分支不存在：%s", source.DisplayRef)
			}
			sourceHead, err := gitResolveContext(ctx, s.Git, validated.Worktree, qualified)
			if err != nil {
				return err
			}
			makeResult := func(outcome WorkspaceMainBranchMergeOutcome, message string, files []string) WorkspaceMainBranchMergeResult {
				return WorkspaceMainBranchMergeResult{WorkspacePath: ws.WorktreePath, ServiceName: ws.ServiceName, ModuleName: ws.ModuleName, SourceRef: source.DisplayRef, TargetBranch: ws.Branch, Outcome: outcome, Message: message, ConflictFiles: files}
			}
			upToDate, err := gitIsAncestorContext(ctx, s.Git, validated.Worktree, sourceHead, before.Head)
			if err != nil {
				return err
			}
			if upToDate {
				result = makeResult(MainBranchUpToDate, "已包含 "+source.DisplayRef+" 的最新提交，无需合并", nil)
				return nil
			}
			fastForward, err := gitIsAncestorContext(ctx, s.Git, validated.Worktree, before.Head, sourceHead)
			if err != nil {
				return err
			}
			merged, runErr := s.Git.RunContext(ctx, validated.Worktree, 5*time.Minute, false, false, "merge", "--no-edit", "--no-autostash", "--no-squash", "--ff", "--commit", qualified)
			if runErr != nil {
				return runErr
			}
			if merged.ExitCode != 0 {
				conflicts, err := s.Git.RunContext(ctx, validated.Worktree, 2*time.Minute, false, true, "diff", "--name-only", "--diff-filter=U", "-z")
				if err != nil {
					return err
				}
				files := splitNonEmptyNUL(conflicts.Stdout)
				if len(files) > 0 {
					label := ws.ServiceName
					if ws.ModuleName != "" && ws.ModuleName != ws.ServiceName {
						label += " · " + ws.ModuleName
					}
					result = makeResult(MainBranchConflict, fmt.Sprintf("%s 合并 %s 到 %s 时存在冲突，请先解决冲突后再继续", label, source.DisplayRef, ws.Branch), files)
					return nil
				}
				return &GitException{Message: fmt.Sprintf("合并失败：%s → %s；请检查当前 Git 状态", source.DisplayRef, ws.Branch), Result: merged}
			}
			if fastForward {
				result = makeResult(MainBranchFastForwarded, fmt.Sprintf("已快进合并 %s 到 %s", source.DisplayRef, ws.Branch), nil)
			} else {
				result = makeResult(MainBranchMerged, fmt.Sprintf("已将 %s 合并到 %s，并创建合并提交", source.DisplayRef, ws.Branch), nil)
			}
			return nil
		})
	})
	return result, err
}

func filepathDir(path string) string { return filepath.Dir(path) }
func splitNonEmptyNUL(value string) []string {
	parts := strings.Split(value, "\x00")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
