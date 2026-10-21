package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"silverwing-go/internal/domain"
)

// ValidateForMutationContext shares the same identity checks with the existing
// Background entry point while allowing a merge's caller to cancel every read.
func (l *WorkspaceLifecycle) ValidateForMutationContext(ctx context.Context, config domain.AppConfig, taskPath string, manifest domain.TaskManifest, ws domain.ServiceWorkspace) (WorkspaceMutationTarget, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return WorkspaceMutationTarget{}, err
	}
	if err := l.validateTaskDirectory(config, taskPath, manifest); err != nil {
		return WorkspaceMutationTarget{}, err
	}
	target, err := l.validateTarget(taskPath, ws)
	if err != nil {
		return WorkspaceMutationTarget{}, err
	}
	if _, err := os.Stat(target); err != nil {
		return WorkspaceMutationTarget{}, fmt.Errorf("任务工作区不存在：%s", target)
	}
	branchResult, err := l.Git.RunContext(ctx, target, 10*time.Minute, true, false, "branch", "--show-current")
	if err != nil {
		return WorkspaceMutationTarget{}, err
	}
	actual := strings.TrimSpace(branchResult.Stdout)
	if actual != ws.Branch {
		return WorkspaceMutationTarget{}, fmt.Errorf("工作区当前分支与任务记录不一致：期望 %s，实际 %s", ws.Branch, actual)
	}
	if err := l.validateExistingIdentityContext(ctx, config, ws, target); err != nil {
		return WorkspaceMutationTarget{}, err
	}
	repository := target
	if ws.Strategy == domain.WorkspaceStandardWorktree {
		repository, err = l.configuredRepositoryContext(ctx, config, ws)
		if err != nil {
			return WorkspaceMutationTarget{}, err
		}
	}
	return WorkspaceMutationTarget{Repository: repository, Worktree: target}, nil
}

func (l *WorkspaceLifecycle) configuredRepositoryContext(ctx context.Context, config domain.AppConfig, ws domain.ServiceWorkspace) (string, error) {
	repository, ok := repositoryByID(config, ws.RepositoryID)
	if !ok {
		return "", fmt.Errorf("仓库配置不存在：%s", ws.RepositoryID)
	}
	actual, err := gitLifecycleCommonDirectoryContext(ctx, l.Git, repository.RootPath)
	if err != nil {
		return "", err
	}
	if !samePath(mustAbs(actual), mustAbs(repository.GitCommonDirectory)) {
		return "", fmt.Errorf("主仓库 Git 身份与配置不匹配")
	}
	return repository.RootPath, nil
}

func (l *WorkspaceLifecycle) validateExistingIdentityContext(ctx context.Context, config domain.AppConfig, ws domain.ServiceWorkspace, target string) error {
	topResult, err := l.Git.RunContext(ctx, target, 10*time.Minute, true, false, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	top, err := filepath.Abs(strings.TrimSpace(topResult.Stdout))
	if err != nil {
		return err
	}
	if !samePath(mustAbs(top), mustAbs(target)) {
		return fmt.Errorf("Git 顶层目录不匹配：%s", target)
	}
	if ws.Strategy == domain.WorkspaceIndependentClone {
		if ws.OriginURL == nil {
			return fmt.Errorf("独立克隆缺少 origin URL")
		}
		origin, err := gitRemoteURLContext(ctx, l.Git, target, "origin")
		if err != nil {
			return err
		}
		if strings.TrimSpace(origin) != strings.TrimSpace(*ws.OriginURL) {
			return fmt.Errorf("独立克隆 origin 与任务记录不匹配")
		}
		return requireOwnedClone(target, cloneOwnership(filepath.Dir(target), ws.RepositoryID, ws.GroupServiceID, ws.ModuleID))
	}
	configured, err := l.configuredRepositoryContext(ctx, config, ws)
	if err != nil {
		return err
	}
	actual, err := gitLifecycleCommonDirectoryContext(ctx, l.Git, target)
	if err != nil {
		return err
	}
	expected, err := gitLifecycleCommonDirectoryContext(ctx, l.Git, configured)
	if err != nil {
		return err
	}
	if !samePath(mustAbs(actual), mustAbs(expected)) {
		return fmt.Errorf("Linked Worktree 不属于配置的主仓库")
	}
	return nil
}

// Lifecycle callers retain CommonDirectory's ten-minute command limit and
// existing arguments; the separate preview validation helper uses two minutes.
func gitLifecycleCommonDirectoryContext(ctx context.Context, git *GitClient, repository string) (string, error) {
	result, err := git.RunContext(ctx, repository, 10*time.Minute, true, false, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return filepath.Abs(strings.TrimSpace(result.Stdout))
}

func gitRefExistsContext(ctx context.Context, git *GitClient, repository, ref string) (bool, error) {
	result, err := git.RunContext(ctx, repository, 10*time.Minute, false, false, "show-ref", "--verify", "--quiet", ref)
	if err != nil {
		return false, err
	}
	return result.ExitCode == 0, nil
}

func gitResolveContext(ctx context.Context, git *GitClient, repository, ref string) (string, error) {
	result, err := git.RunContext(ctx, repository, 10*time.Minute, true, false, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result.Stdout), nil
}

func gitIsAncestorContext(ctx context.Context, git *GitClient, repository, ancestor, descendant string) (bool, error) {
	result, err := git.RunContext(ctx, repository, 10*time.Minute, false, false, "merge-base", "--is-ancestor", ancestor, descendant)
	if err != nil {
		return false, err
	}
	if result.ExitCode == 0 {
		return true, nil
	}
	if result.ExitCode == 1 {
		return false, nil
	}
	return false, &GitException{Message: "Git 祖先关系检查失败", Result: result}
}
