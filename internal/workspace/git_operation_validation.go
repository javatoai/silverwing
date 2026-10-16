package workspace

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"silverwing-go/internal/domain"
)

func (s *WorkspaceGitOperationService) previewValidatedContext(ctx context.Context, ws domain.ServiceWorkspace, target string) (WorkspaceGitChangePreview, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	branchResult, err := s.Git.RunContext(ctx, target, 2*time.Minute, true, true, "branch", "--show-current")
	if err != nil {
		return WorkspaceGitChangePreview{}, err
	}
	branch := strings.TrimSpace(branchResult.Stdout)
	if branch == "" {
		return WorkspaceGitChangePreview{}, fmt.Errorf("Detached HEAD 不能执行 Git 操作")
	}
	head, err := s.Git.RunContext(ctx, target, 2*time.Minute, true, true, "rev-parse", "HEAD")
	if err != nil {
		return WorkspaceGitChangePreview{}, err
	}
	upstreamResult, err := s.Git.RunContext(ctx, target, 2*time.Minute, false, true, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return WorkspaceGitChangePreview{}, err
	}
	upstream := strings.TrimSpace(upstreamResult.Stdout)
	if upstreamResult.ExitCode != 0 || upstream == "" {
		upstream = ""
	}
	status, err := s.Git.RunContext(ctx, target, 2*time.Minute, true, true, "status", "--short", "--untracked-files=all")
	if err != nil {
		return WorkspaceGitChangePreview{}, err
	}
	stat, err := s.Git.RunContext(ctx, target, 2*time.Minute, false, true, "diff", "--stat", "HEAD")
	if err != nil {
		return WorkspaceGitChangePreview{}, err
	}
	files := make([]string, 0)
	for _, line := range strings.Split(status.Stdout, "\n") {
		if strings.TrimSpace(line) != "" {
			files = append(files, strings.TrimRight(line, "\r"))
		}
	}
	headValue := strings.TrimSpace(head.Stdout)
	fingerprint := workspaceFingerprint(branch, headValue, upstream, status.Stdout)
	return WorkspaceGitChangePreview{
		WorkspacePath: WorkspaceGitPathKey(ws), Branch: branch, Head: headValue,
		Upstream: upstream, Files: files, DiffStat: strings.TrimSpace(stat.Stdout), Fingerprint: fingerprint,
	}, nil
}

func (s *WorkspaceGitOperationService) validateContext(ctx context.Context, ws domain.ServiceWorkspace) (string, string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	target := mustAbs(ws.WorktreePath)
	if _, err := osStatDirectory(target); err != nil {
		return "", "", fmt.Errorf("工作区不存在：%s", target)
	}
	topResult, err := s.Git.RunContext(ctx, target, 2*time.Minute, true, true, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", err
	}
	top, err := filepath.Abs(strings.TrimSpace(topResult.Stdout))
	if err != nil {
		return "", "", err
	}
	if !samePath(top, target) {
		return "", "", fmt.Errorf("工作区路径不再是 Git 顶层目录：%s", target)
	}
	var common string
	if ws.Strategy == domain.WorkspaceStandardWorktree {
		expected, err := gitCommonDirectoryContext(ctx, s.Git, ws.RepositoryPath)
		if err != nil {
			return "", "", err
		}
		actual, err := gitCommonDirectoryContext(ctx, s.Git, target)
		if err != nil {
			return "", "", err
		}
		if !samePath(actual, expected) {
			return "", "", fmt.Errorf("Worktree Git 身份与任务记录不一致")
		}
		common = expected
	} else if ws.Strategy == domain.WorkspaceIndependentClone {
		origin, err := gitRemoteURLContext(ctx, s.Git, target, "origin")
		if err != nil {
			return "", "", err
		}
		if ws.OriginURL == nil || strings.TrimSpace(origin) != strings.TrimSpace(*ws.OriginURL) {
			return "", "", fmt.Errorf("独立克隆 origin 与任务记录不一致")
		}
	} else {
		return "", "", fmt.Errorf("工作区策略不受支持：%s", ws.Strategy)
	}
	branchResult, err := s.Git.RunContext(ctx, target, 2*time.Minute, true, true, "branch", "--show-current")
	if err != nil {
		return "", "", err
	}
	branch := strings.TrimSpace(branchResult.Stdout)
	if branch != ws.Branch {
		return "", "", fmt.Errorf("当前分支与任务记录不一致")
	}
	status, err := gitStatusForValidationContext(ctx, s.Git, target)
	if err != nil {
		return "", "", err
	}
	if status != "" {
		return "", "", fmt.Errorf("存在进行中的 Git 操作，完成或中止后再试")
	}
	name, err := s.Git.RunContext(ctx, target, time.Minute, false, true, "config", "user.name")
	if err != nil {
		return "", "", err
	}
	email, err := s.Git.RunContext(ctx, target, time.Minute, false, true, "config", "user.email")
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(name.Stdout) == "" || strings.TrimSpace(email.Stdout) == "" {
		return "", "", fmt.Errorf("Git user.name 和 user.email 必须配置")
	}
	if ws.Strategy == domain.WorkspaceIndependentClone {
		common, err = gitCommonDirectoryContext(ctx, s.Git, target)
		if err != nil {
			return "", "", err
		}
	}
	return target, common, nil
}

func (s *WorkspaceGitOperationService) requireWriteAllowedContext(ctx context.Context, ws domain.ServiceWorkspace, target string, blocked []string, operation string) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, _, err := s.validateContext(ctx, ws); err != nil {
		return "", err
	}
	branchResult, err := s.Git.RunContext(ctx, target, 2*time.Minute, true, true, "branch", "--show-current")
	if err != nil {
		return "", err
	}
	branch := strings.TrimSpace(branchResult.Stdout)
	if branch == "" {
		return "", fmt.Errorf("Detached HEAD 不能执行 Git 操作")
	}
	if err := requireGitWriteAllowed(branch, blocked, operation); err != nil {
		return "", err
	}
	return branch, nil
}

func gitCommonDirectoryContext(ctx context.Context, git *GitClient, repository string) (string, error) {
	result, err := git.RunContext(ctx, repository, 2*time.Minute, true, true, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", err
	}
	return filepath.Abs(strings.TrimSpace(result.Stdout))
}

func gitRemoteURLContext(ctx context.Context, git *GitClient, repository, remote string) (string, error) {
	result, err := git.RunContext(ctx, repository, 2*time.Minute, false, true, "remote", "get-url", remote)
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		return "", nil
	}
	return strings.TrimSpace(result.Stdout), nil
}

func gitOperationInProgressContext(ctx context.Context, git *GitClient, repository string) (string, error) {
	result, err := git.RunContext(ctx, repository, 2*time.Minute, true, true, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	gitDirectory := strings.TrimSpace(result.Stdout)
	for _, entry := range []struct{ name, kind string }{
		{"MERGE_HEAD", "merge"}, {"rebase-merge", "rebase"}, {"rebase-apply", "rebase"},
		{"CHERRY_PICK_HEAD", "cherry-pick"}, {"REVERT_HEAD", "revert"}, {"BISECT_LOG", "bisect"}, {"BISECT_START", "bisect"},
	} {
		if fileExists(filepath.Join(gitDirectory, entry.name)) {
			return entry.kind, nil
		}
	}
	return "", nil
}

func gitStatusForValidationContext(ctx context.Context, git *GitClient, repository string) (string, error) {
	if _, err := git.RunContext(ctx, repository, 2*time.Minute, true, true, "status", "--porcelain=v1", "--untracked-files=all"); err != nil {
		return "", err
	}
	upstream, err := git.RunContext(ctx, repository, 2*time.Minute, false, true, "rev-parse", "--abbrev-ref", "--symbolic-full-name", "@{upstream}")
	if err != nil {
		return "", err
	}
	countArgs := []string{"rev-list", "--count", "HEAD", "--not", "--remotes"}
	if upstream.ExitCode == 0 {
		countArgs = []string{"rev-list", "--count", "@{upstream}..HEAD"}
	}
	if _, err := git.RunContext(ctx, repository, 2*time.Minute, true, true, countArgs...); err != nil {
		return "", err
	}
	return gitOperationInProgressContext(ctx, git, repository)
}
