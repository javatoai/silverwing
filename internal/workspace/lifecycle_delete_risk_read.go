package workspace

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"silverwing-go/internal/domain"
)

// Delete-risk inspection has its own read path so cancellation does not change
// the existing mutation validation and removal workflows.
func (l *WorkspaceLifecycle) validateDeleteRiskIdentityContext(ctx context.Context, config domain.AppConfig, ws domain.ServiceWorkspace, target string) error {
	top, err := l.Git.RunContext(ctx, target, 2*time.Minute, true, true, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	topPath, err := filepath.Abs(strings.TrimSpace(top.Stdout))
	if err != nil {
		return err
	}
	if !samePath(mustAbs(topPath), mustAbs(target)) {
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
	configured, err := l.configuredDeleteRiskRepositoryContext(ctx, config, ws)
	if err != nil {
		return err
	}
	actual, err := gitCommonDirectoryContext(ctx, l.Git, target)
	if err != nil {
		return err
	}
	expected, err := gitCommonDirectoryContext(ctx, l.Git, configured)
	if err != nil {
		return err
	}
	if !samePath(mustAbs(actual), mustAbs(expected)) {
		return fmt.Errorf("Linked Worktree 不属于配置的主仓库")
	}
	return nil
}

func (l *WorkspaceLifecycle) configuredDeleteRiskRepositoryContext(ctx context.Context, config domain.AppConfig, ws domain.ServiceWorkspace) (string, error) {
	repository, ok := repositoryByID(config, ws.RepositoryID)
	if !ok {
		return "", fmt.Errorf("仓库配置不存在：%s", ws.RepositoryID)
	}
	actual, err := gitCommonDirectoryContext(ctx, l.Git, repository.RootPath)
	if err != nil {
		return "", err
	}
	if !samePath(mustAbs(actual), mustAbs(repository.GitCommonDirectory)) {
		return "", fmt.Errorf("主仓库 Git 身份与配置不匹配")
	}
	return repository.RootPath, nil
}

func deleteRiskStatusContext(ctx context.Context, git *GitClient, repository string) (RepositoryStatus, error) {
	result, err := git.RunContext(ctx, repository, 2*time.Minute, true, true, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return RepositoryStatus{}, err
	}
	status := parseRepositoryPorcelain(result.Stdout)
	upstream, err := git.RunContext(ctx, repository, 2*time.Minute, false, true,
		"rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return status, err
	}
	countArgs := []string{"rev-list", "--count", "HEAD", "--not", "--remotes"}
	if upstream.ExitCode == 0 {
		countArgs = []string{"rev-list", "--count", "@{upstream}..HEAD"}
	}
	countResult, err := git.RunContext(ctx, repository, 2*time.Minute, true, true, countArgs...)
	if err != nil {
		return status, err
	}
	status.UnpushedCommits, _ = strconv.Atoi(strings.TrimSpace(countResult.Stdout))
	status.OperationInProgress, err = gitOperationInProgressContext(ctx, git, repository)
	return status, err
}

func deleteRiskLocalOnlyCommitCountContext(ctx context.Context, git *GitClient, repository string) (int, error) {
	result, err := git.RunContext(ctx, repository, 2*time.Minute, true, true, "rev-list", "--count", "HEAD", "--all", "--not", "--remotes")
	if err != nil {
		return 0, err
	}
	count, parseErr := strconv.Atoi(strings.TrimSpace(result.Stdout))
	if parseErr != nil {
		return 0, fmt.Errorf("无法解析本地未推送提交数量：%s", repository)
	}
	return count, nil
}
