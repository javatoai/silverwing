package workspace

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

type WorktreeRemoteCheck struct {
	Repository string
	BaseRef    string
	Failure    error
}

// CheckAppendWorktreeRemotes resolves the same repository and base selections
// as append preflight. The probe only reads the configured remote's heads.
func CheckAppendWorktreeRemotes(ctx context.Context, git *GitClient, config domain.AppConfig, manifest domain.TaskManifest, request AddWorkspaceRequest) ([]WorktreeRemoteCheck, error) {
	if manifest.LifecycleStatus != domain.TaskActive {
		return nil, errors.New("归档任务不能添加工作区")
	}
	group, err := config.Group(manifest.GroupID)
	if err != nil {
		return nil, err
	}
	selections, err := selectionsForAdd(group, request, manifest.FeatureBranch)
	if err != nil {
		return nil, err
	}
	var checks []WorktreeRemoteCheck
	seen := map[[2]string]bool{}
	for _, selection := range selections {
		for _, module := range selection.Service.Modules {
			if module.Strategy != domain.WorkspaceStandardWorktree {
				continue
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			repository, ok := repositoryByID(config, selection.Service.RepositoryID)
			if !ok {
				return nil, fmt.Errorf("服务 %s 的仓库配置不存在", selection.Service.DisplayName)
			}
			baseRef := moduleMasterBranch(selection.Service, module)
			base, err := ParseRemoteBranchRef(baseRef)
			if err != nil {
				return nil, err
			}
			base.Branch = normalizeBaseRef(baseRef)
			key := [2]string{repository.ID, FormatRemoteBranchRef(base)}
			if seen[key] {
				continue
			}
			seen[key] = true
			failure := git.checkWorktreeRemote(ctx, repository, base)
			if process.IsCleanupFailure(failure) {
				return nil, failure
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			checks = append(checks, WorktreeRemoteCheck{Repository: repository.Name, BaseRef: key[1], Failure: failure})
		}
	}
	return checks, nil
}

func (g *GitClient) checkWorktreeRemote(ctx context.Context, repository domain.RepositoryConfig, base domain.RemoteBranchRef) error {
	const timeout = 20 * time.Second
	identity, err := g.RunContext(ctx, repository.RootPath, timeout, true, true, "rev-parse", "--path-format=absolute", "--show-toplevel", "--git-common-dir")
	if err != nil {
		return diagnoseGitRemoteError(ctx, err)
	}
	top, common, valid := parseWorkspaceDirectoryIdentity(identity.Stdout)
	if !valid || !samePath(top, repository.RootPath) || !samePath(common, repository.GitCommonDirectory) {
		return errors.New("本地仓库路径或 Git 身份已变化，请刷新仓库配置后重试。")
	}
	remotes, err := g.RunContext(ctx, repository.RootPath, timeout, true, true, "remote")
	if err != nil {
		return diagnoseGitRemoteError(ctx, err)
	}
	found := false
	for _, name := range nonEmptyLines(remotes.Stdout) {
		if name == base.Remote {
			found = true
			break
		}
	}
	if !found {
		return &GitRemoteFailure{Kind: GitRemoteMissing}
	}
	ref := "refs/heads/" + base.Branch
	git := g.withRemotePrompt(false)
	result, err := git.RunContext(ctx, repository.RootPath, timeout, true, true, "ls-remote", "--exit-code", "--heads", "--", base.Remote, ref)
	if err != nil {
		var failure *GitException
		if errors.As(err, &failure) && failure.Cause == nil && result.ExitCode == 2 {
			return &GitRemoteFailure{Kind: GitRemoteBranchMissing}
		}
		return diagnoseGitRemoteError(ctx, err)
	}
	for _, line := range strings.Split(result.Stdout, "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[1] == ref {
			return nil
		}
	}
	return &GitRemoteFailure{Kind: GitRemoteBranchMissing}
}

func fetchWorktreeRemote(ctx context.Context, git *GitClient, root, remote string) error {
	if err := git.FetchContext(ctx, root, remote); err != nil {
		return fmt.Errorf("刷新远端 %s 失败：%w", remote, diagnoseGitRemoteError(ctx, err))
	}
	return nil
}
