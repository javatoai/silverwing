package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"time"

	"silverwing-go/internal/domain"
)

type WorkspaceGitOperationResult struct {
	Message string `json:"message"`
}
type WorkspaceGitBatchMode string

const (
	WorkspaceGitBatchCommit        WorkspaceGitBatchMode = "COMMIT"
	WorkspaceGitBatchPush          WorkspaceGitBatchMode = "PUSH"
	WorkspaceGitBatchCommitAndPush WorkspaceGitBatchMode = "COMMIT_AND_PUSH"
)

type WorkspaceGitStepState string

const (
	WorkspaceGitNotRun  WorkspaceGitStepState = "NOT_RUN"
	WorkspaceGitSuccess WorkspaceGitStepState = "SUCCESS"
	WorkspaceGitSkipped WorkspaceGitStepState = "SKIPPED"
	WorkspaceGitFailed  WorkspaceGitStepState = "FAILED"
)

type WorkspaceGitBatchItemResult struct {
	WorkspacePath string                `json:"workspacePath"`
	ServiceName   string                `json:"serviceName"`
	Branch        string                `json:"branch"`
	CommitState   WorkspaceGitStepState `json:"commitState"`
	PushState     WorkspaceGitStepState `json:"pushState"`
	Message       string                `json:"message"`
}
type WorkspaceGitBatchResult struct {
	Items []WorkspaceGitBatchItemResult `json:"items"`
}
type WorkspaceGitChangePreview struct {
	WorkspacePath string   `json:"workspacePath"`
	Branch        string   `json:"branch"`
	Head          string   `json:"head"`
	Upstream      string   `json:"upstream"`
	Files         []string `json:"files"`
	DiffStat      string   `json:"diffStat"`
	Fingerprint   string   `json:"fingerprint"`
}
type workspacePushPlan struct {
	Remote, Branch string
	SetUpstream    bool
}
type workspaceGitPreflight struct {
	Workspace      domain.ServiceWorkspace
	Target, Common string
	Preview        WorkspaceGitChangePreview
	Plan           *workspacePushPlan
}

type WorkspaceGitOperationService struct {
	Git              *GitClient
	Locks            *OperationLocks
	batchPreflightFn func(context.Context, domain.ServiceWorkspace) (workspaceGitPreflight, error)
	batchExecuteFn   func(context.Context, workspaceGitPreflight) (WorkspaceGitBatchItemResult, error)
}

func NewWorkspaceGitOperationService(git *GitClient, locks *OperationLocks) *WorkspaceGitOperationService {
	if git == nil {
		git = NewGitClient(nil, "")
	}
	if locks == nil {
		locks = NewOperationLocks("")
	}
	return &WorkspaceGitOperationService{Git: git, Locks: locks}
}

func WorkspaceGitPathKey(workspace domain.ServiceWorkspace) string {
	return mustAbs(workspace.WorktreePath)
}

func (s *WorkspaceGitOperationService) Preview(workspace domain.ServiceWorkspace) (WorkspaceGitChangePreview, error) {
	return s.PreviewContext(context.Background(), workspace)
}

func (s *WorkspaceGitOperationService) PreviewContext(ctx context.Context, workspace domain.ServiceWorkspace) (WorkspaceGitChangePreview, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return WorkspaceGitChangePreview{}, err
	}
	target, _, err := s.validateContext(ctx, workspace)
	if err != nil {
		return WorkspaceGitChangePreview{}, err
	}
	preview, err := s.previewValidatedContext(ctx, workspace, target)
	if err != nil {
		return WorkspaceGitChangePreview{}, err
	}
	if err := ctx.Err(); err != nil {
		return WorkspaceGitChangePreview{}, err
	}
	return preview, nil
}

func (s *WorkspaceGitOperationService) PreviewsContext(ctx context.Context, workspaces []domain.ServiceWorkspace) (map[string]WorkspaceGitChangePreview, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result := map[string]WorkspaceGitChangePreview{}
	for _, workspace := range uniquePhysicalWorkspaces(workspaces) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		preview, err := s.PreviewContext(ctx, workspace)
		if err != nil {
			return nil, err
		}
		result[WorkspaceGitPathKey(workspace)] = preview
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *WorkspaceGitOperationService) Commit(ctx context.Context, workspace domain.ServiceWorkspace, message, expectedFingerprint string, blockedBranches []string) (WorkspaceGitOperationResult, error) {
	result := WorkspaceGitOperationResult{}
	err := s.withRepositoryLock(ctx, workspace, func(target string) error {
		branch, err := s.requireWriteAllowed(ctx, workspace, target, blockedBranches, "提交")
		if err != nil {
			return err
		}
		if expectedFingerprint != "" {
			preview, err := s.previewValidatedContext(ctx, workspace, target)
			if err != nil {
				return err
			}
			if preview.Fingerprint != expectedFingerprint {
				return fmt.Errorf("工作区状态已变化，请重新预览后确认：%s", workspaceOperationName(workspace))
			}
		}
		message = strings.TrimSpace(message)
		if message == "" {
			return fmt.Errorf("提交信息不能为空")
		}
		if _, err := s.Git.RunContext(ctx, target, 5*time.Minute, true, false, "add", "-A"); err != nil {
			return err
		}
		staged, err := s.Git.RunContext(ctx, target, 2*time.Minute, false, false, "diff", "--cached", "--quiet")
		if err != nil {
			return err
		}
		if staged.ExitCode == 0 {
			return fmt.Errorf("没有可提交的变更")
		}
		if _, err := s.Git.RunContext(ctx, target, 5*time.Minute, true, false, "commit", "-m", message); err != nil {
			return err
		}
		result.Message = "已提交 " + branch
		return nil
	})
	return result, err
}

func (s *WorkspaceGitOperationService) Push(ctx context.Context, workspace domain.ServiceWorkspace, blockedBranches []string) (WorkspaceGitOperationResult, error) {
	var result WorkspaceGitOperationResult
	err := s.withRepositoryLock(ctx, workspace, func(target string) error {
		branch, err := s.requireWriteAllowed(ctx, workspace, target, blockedBranches, "推送")
		if err != nil {
			return err
		}
		plan, err := s.resolvePushPlan(ctx, workspace, target, branch)
		if err != nil {
			return err
		}
		if err := s.pushPlanned(ctx, target, plan); err != nil {
			return err
		}
		result.Message = fmt.Sprintf("已推送 %s/%s", plan.Remote, plan.Branch)
		return nil
	})
	return result, err
}

func (s *WorkspaceGitOperationService) CommitAndPush(ctx context.Context, workspace domain.ServiceWorkspace, message, expectedFingerprint string, blockedBranches []string) (WorkspaceGitOperationResult, error) {
	var result WorkspaceGitOperationResult
	err := s.withRepositoryLock(ctx, workspace, func(target string) error {
		branch, err := s.requireWriteAllowed(ctx, workspace, target, blockedBranches, "提交和推送")
		if err != nil {
			return err
		}
		if expectedFingerprint != "" {
			preview, e := s.previewValidatedContext(ctx, workspace, target)
			if e != nil {
				return e
			}
			if preview.Fingerprint != expectedFingerprint {
				return fmt.Errorf("工作区状态已变化，请重新预览后确认：%s", workspaceOperationName(workspace))
			}
		}
		message = strings.TrimSpace(message)
		if message == "" {
			return fmt.Errorf("提交信息不能为空")
		}
		if _, err = s.Git.RunContext(ctx, target, 5*time.Minute, true, false, "add", "-A"); err != nil {
			return err
		}
		staged, err := s.Git.RunContext(ctx, target, 2*time.Minute, false, false, "diff", "--cached", "--quiet")
		if err != nil {
			return err
		}
		if staged.ExitCode == 0 {
			return fmt.Errorf("没有可提交的变更")
		}
		if _, err = s.Git.RunContext(ctx, target, 5*time.Minute, true, false, "commit", "-m", message); err != nil {
			return err
		}
		branch, err = s.requireWriteAllowed(ctx, workspace, target, blockedBranches, "提交和推送")
		if err != nil {
			return err
		}
		plan, err := s.resolvePushPlan(ctx, workspace, target, branch)
		if err != nil {
			return err
		}
		if err = s.pushPlanned(ctx, target, plan); err != nil {
			return err
		}
		result.Message = fmt.Sprintf("已提交并推送 %s/%s", plan.Remote, plan.Branch)
		return nil
	})
	return result, err
}

func (s *WorkspaceGitOperationService) Batch(ctx context.Context, workspaces []domain.ServiceWorkspace, mode WorkspaceGitBatchMode, messages, expectedFingerprints map[string]string, blockedBranches []string) (WorkspaceGitBatchResult, error) {
	if mode != WorkspaceGitBatchCommit && mode != WorkspaceGitBatchPush && mode != WorkspaceGitBatchCommitAndPush {
		return WorkspaceGitBatchResult{}, fmt.Errorf("Git 批量操作模式不受支持：%s", mode)
	}
	unique := uniquePhysicalWorkspaces(workspaces)
	prepare := s.batchPreflightFn
	if prepare == nil {
		prepare = func(ctx context.Context, ws domain.ServiceWorkspace) (workspaceGitPreflight, error) {
			return s.preflightBatch(ctx, ws, mode, messages, expectedFingerprints, blockedBranches)
		}
	}
	preflights, err := runWorkspaceGitBatchPreflights(ctx, unique, workspaceGitBatchParallelism, prepare)
	if err != nil {
		return WorkspaceGitBatchResult{}, err
	}
	execute := s.batchExecuteFn
	if execute == nil {
		execute = func(ctx context.Context, item workspaceGitPreflight) (WorkspaceGitBatchItemResult, error) {
			return s.executeBatch(ctx, item, mode, messages[WorkspaceGitPathKey(item.Workspace)], blockedBranches)
		}
	}
	return runWorkspaceGitBatchGroups(ctx, preflights, workspaceGitBatchParallelism, mode, execute)
}

func (s *WorkspaceGitOperationService) preflightBatch(ctx context.Context, ws domain.ServiceWorkspace, mode WorkspaceGitBatchMode, messages, expectedFingerprints map[string]string, blockedBranches []string) (workspaceGitPreflight, error) {
	target, common, err := s.validateContext(ctx, ws)
	if err != nil {
		return workspaceGitPreflight{}, err
	}
	preview, err := s.previewValidatedContext(ctx, ws, target)
	if err != nil {
		return workspaceGitPreflight{}, err
	}
	if err := requireGitWriteAllowed(preview.Branch, blockedBranches, workspaceBatchLabel(mode)); err != nil {
		return workspaceGitPreflight{}, err
	}
	key := WorkspaceGitPathKey(ws)
	if expected := expectedFingerprints[key]; expected != "" && expected != preview.Fingerprint {
		return workspaceGitPreflight{}, fmt.Errorf("工作区状态已变化，请重新预览后确认：%s", workspaceOperationName(ws))
	}
	var plan *workspacePushPlan
	if mode != WorkspaceGitBatchCommit {
		resolved, err := s.resolvePushPlan(ctx, ws, target, preview.Branch)
		if err != nil {
			return workspaceGitPreflight{}, err
		}
		plan = &resolved
	}
	if mode != WorkspaceGitBatchPush && len(preview.Files) > 0 && strings.TrimSpace(messages[key]) == "" {
		return workspaceGitPreflight{}, fmt.Errorf("提交信息不能为空：%s", workspaceOperationName(ws))
	}
	return workspaceGitPreflight{Workspace: ws, Target: target, Common: common, Preview: preview, Plan: plan}, nil
}

func (s *WorkspaceGitOperationService) executeBatch(ctx context.Context, item workspaceGitPreflight, mode WorkspaceGitBatchMode, message string, blocked []string) (WorkspaceGitBatchItemResult, error) {
	result := WorkspaceGitBatchItemResult{WorkspacePath: WorkspaceGitPathKey(item.Workspace), ServiceName: workspaceOperationName(item.Workspace), Branch: item.Workspace.Branch, CommitState: WorkspaceGitNotRun, PushState: WorkspaceGitNotRun}
	entered := false
	err := s.Locks.WithRepository(item.Common, func() error {
		entered = true
		branch, err := s.requireWriteAllowedContext(ctx, item.Workspace, item.Target, blocked, workspaceBatchLabel(mode))
		if err != nil {
			if mode == WorkspaceGitBatchPush {
				result.PushState = WorkspaceGitFailed
			} else {
				result.CommitState = WorkspaceGitFailed
			}
			return err
		}
		if mode == WorkspaceGitBatchPush || mode == WorkspaceGitBatchCommitAndPush && len(item.Preview.Files) == 0 {
			current, err := s.previewValidatedContext(ctx, item.Workspace, item.Target)
			if err != nil {
				result.PushState = WorkspaceGitFailed
				return err
			}
			if current.Fingerprint != item.Preview.Fingerprint {
				result.PushState = WorkspaceGitFailed
				return fmt.Errorf("工作区状态已变化，请重新预览后确认：%s", result.ServiceName)
			}
		}
		if mode != WorkspaceGitBatchPush {
			if len(item.Preview.Files) == 0 {
				result.CommitState = WorkspaceGitSkipped
				result.Message = "没有需要提交的变更"
			} else {
				current, err := s.previewValidatedContext(ctx, item.Workspace, item.Target)
				if err != nil {
					result.CommitState = WorkspaceGitFailed
					return err
				}
				if current.Fingerprint != item.Preview.Fingerprint {
					result.CommitState = WorkspaceGitFailed
					return fmt.Errorf("工作区状态已变化，请重新预览后确认：%s", result.ServiceName)
				}
				if _, err = s.Git.RunContext(ctx, item.Target, 5*time.Minute, true, false, "add", "-A"); err != nil {
					result.CommitState = WorkspaceGitFailed
					return err
				}
				staged, err := s.Git.RunContext(ctx, item.Target, 2*time.Minute, false, false, "diff", "--cached", "--quiet")
				if err != nil {
					result.CommitState = WorkspaceGitFailed
					return err
				}
				if staged.ExitCode == 0 {
					result.CommitState = WorkspaceGitSkipped
					result.Message = "没有需要提交的变更"
				} else if _, err = s.Git.RunContext(ctx, item.Target, 5*time.Minute, true, false, "commit", "-m", strings.TrimSpace(message)); err != nil {
					result.CommitState = WorkspaceGitFailed
					return err
				} else {
					result.CommitState = WorkspaceGitSuccess
					result.Message = "已提交 " + branch
				}
			}
		}
		if mode == WorkspaceGitBatchPush || mode == WorkspaceGitBatchCommitAndPush && result.CommitState != WorkspaceGitFailed {
			branch, err = s.requireWriteAllowedContext(ctx, item.Workspace, item.Target, blocked, workspaceBatchLabel(mode))
			if err != nil {
				result.PushState = WorkspaceGitFailed
				return err
			}
			plan := item.Plan
			if plan == nil {
				resolved, e := s.resolvePushPlan(ctx, item.Workspace, item.Target, branch)
				if e != nil {
					result.PushState = WorkspaceGitFailed
					return e
				}
				plan = &resolved
			}
			if err = s.pushPlanned(ctx, item.Target, *plan); err != nil {
				result.PushState = WorkspaceGitFailed
				return err
			}
			result.PushState = WorkspaceGitSuccess
			result.Message = strings.TrimSpace(result.Message + "；已推送 " + plan.Remote + "/" + plan.Branch)
		}
		return nil
	})
	if err != nil {
		result.Message = strings.TrimSpace(result.Message + "；" + err.Error())
		// Source acquires the repository lock outside its per-step catches.
		// A lock acquisition error fails the action; an entered step retains
		// its commit/push state, including a commit before a rejected push.
		if !entered {
			return result, err
		}
	}
	return result, nil
}

func (s *WorkspaceGitOperationService) withRepositoryLock(ctx context.Context, ws domain.ServiceWorkspace, action func(string) error) error {
	target, common, err := s.validateContext(ctx, ws)
	if err != nil {
		return err
	}
	return s.Locks.WithRepository(common, func() error {
		if _, _, err := s.validateContext(ctx, ws); err != nil {
			return err
		}
		return action(target)
	})
}
func (s *WorkspaceGitOperationService) requireWriteAllowed(ctx context.Context, ws domain.ServiceWorkspace, target string, blocked []string, operation string) (string, error) {
	return s.requireWriteAllowedContext(ctx, ws, target, blocked, operation)
}
func (s *WorkspaceGitOperationService) resolvePushPlan(ctx context.Context, ws domain.ServiceWorkspace, target, branch string) (workspacePushPlan, error) {
	up, err := s.Git.RunContext(ctx, target, 2*time.Minute, false, true, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return workspacePushPlan{}, err
	}
	upstream := ""
	if up.ExitCode == 0 {
		upstream = strings.TrimSpace(up.Stdout)
	}
	remote := ws.PushRemote
	if slash := strings.IndexByte(upstream, '/'); slash > 0 {
		remote = upstream[:slash]
	}
	if remote == "" {
		remote = "origin"
	}
	remoteURL, err := gitRemoteURLContext(ctx, s.Git, target, remote)
	if err != nil {
		return workspacePushPlan{}, err
	}
	if strings.TrimSpace(remoteURL) == "" {
		return workspacePushPlan{}, fmt.Errorf("Push 远程不存在：%s", remote)
	}
	remoteBranch, err := s.Git.RunContext(ctx, target, 5*time.Minute, false, true, "ls-remote", "--exit-code", "--heads", remote, "refs/heads/"+branch)
	if err != nil {
		return workspacePushPlan{}, err
	}
	if remoteBranch.ExitCode != 0 && remoteBranch.ExitCode != 2 {
		return workspacePushPlan{}, &GitException{Message: fmt.Sprintf("远程分支检查失败：%s/%s", remote, branch), Result: remoteBranch}
	}
	return workspacePushPlan{Remote: remote, Branch: branch, SetUpstream: upstream != remote+"/"+branch || remoteBranch.ExitCode == 2}, nil
}
func (s *WorkspaceGitOperationService) pushPlanned(ctx context.Context, target string, plan workspacePushPlan) error {
	args := []string{"push"}
	if plan.SetUpstream {
		args = append(args, "-u")
	}
	args = append(args, plan.Remote, "HEAD:refs/heads/"+plan.Branch)
	_, err := s.Git.RunContext(ctx, target, 10*time.Minute, true, false, args...)
	return err
}
func requireGitWriteAllowed(branch string, blocked []string, operation string) error {
	policy := domain.NewGitWritePolicy(blocked)
	return policy.RequireAllowed(branch, operation)
}
func workspaceOperationName(ws domain.ServiceWorkspace) string {
	if ws.ModuleName != "" {
		return ws.ModuleName
	}
	return ws.ServiceName
}
func workspaceBatchLabel(mode WorkspaceGitBatchMode) string {
	switch mode {
	case WorkspaceGitBatchCommit:
		return "提交"
	case WorkspaceGitBatchPush:
		return "推送"
	default:
		return "提交和推送"
	}
}
func uniquePhysicalWorkspaces(values []domain.ServiceWorkspace) []domain.ServiceWorkspace {
	seen := map[string]bool{}
	result := []domain.ServiceWorkspace{}
	for _, ws := range values {
		key := strings.ToLower(WorkspaceGitPathKey(ws))
		if !seen[key] {
			seen[key] = true
			result = append(result, ws)
		}
	}
	return result
}
func workspaceFingerprint(branch, head, upstream, porcelain string) string {
	sum := sha256.Sum256([]byte(branch + "\x00" + head + "\x00" + upstream + "\x00" + porcelain))
	return hex.EncodeToString(sum[:])
}

func osStatDirectory(path string) (os.FileInfo, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("not a directory")
	}
	return info, nil
}
