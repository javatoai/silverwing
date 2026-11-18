package workspace

import (
	"context"
	"errors"
	"net/url"
	"runtime"
	"sort"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/process"
)

const materialsBranchOutputLimit = 8 * 1024 * 1024

func MaterialsGitHTTPSUpgrade(repositoryURL string) string {
	if domain.ValidateRemoteGitURL(repositoryURL) != nil {
		return ""
	}
	parsed, err := url.Parse(repositoryURL)
	if err != nil || !strings.EqualFold(parsed.Scheme, "http") {
		return ""
	}
	parsed.Scheme = "https"
	return parsed.String()
}

func MaterialsGitUsesHTTPS(repositoryURL string) bool {
	return strings.HasPrefix(strings.ToLower(repositoryURL), "https://")
}

func (s *MaterialsGitSyncService) checkRemoteScope(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.Check != nil {
		return s.Check()
	}
	return nil
}

func (s *MaterialsGitSyncService) prepareRemoteGit(ctx context.Context, scope MaterialsGitScope, root string, interactive bool) (*GitClient, []string, error) {
	if err := s.checkRemoteScope(ctx); err != nil {
		return nil, nil, err
	}
	git := s.Git.withRemotePrompt(interactive)
	if !MaterialsGitUsesHTTPS(scope.URL) {
		return git, nil, nil
	}
	git.Environment["GIT_ALLOW_PROTOCOL"] = "https"
	if runtime.GOOS != "darwin" {
		return git, nil, nil
	}
	// Check for helper configuration before using the system Keychain fallback.
	// Only the exit code is needed. Git itself talks to the credential helper.
	args := []string{"--no-optional-locks", "-c", "core.longpaths=true"}
	if root != "" {
		args = append(args, "-C", root)
	}
	args = append(args, "config", "--get-urlmatch", "credential.helper", scope.URL)
	spec := git.processSpec(args, root, 5*time.Second)
	spec.MaxStdoutBytes = 1
	result, err := git.runProcess(ctx, spec)
	if err != nil {
		return nil, nil, diagnoseGitRemoteError(ctx, err)
	}
	if err := s.checkRemoteScope(ctx); err != nil {
		return nil, nil, err
	}
	switch result.ExitCode {
	case 0:
		return git, nil, nil
	case 1:
		return git, []string{"-c", "credential.helper=osxkeychain"}, nil
	default:
		return nil, nil, errors.New("无法检查 Git 凭据助手配置，请检查 Git 设置")
	}
}

func (s *MaterialsGitSyncService) readRemoteBranchHeads(ctx context.Context, scope MaterialsGitScope, interactive bool, timeout time.Duration) (process.Result, error) {
	git, options, err := s.prepareRemoteGit(ctx, scope, "", interactive)
	if err != nil {
		return process.Result{}, err
	}
	result, err := git.runReadBudget(ctx, "", timeout, materialsBranchOutputLimit, append(options, "ls-remote", "--heads", "--", scope.URL)...)
	if process.IsCleanupFailure(err) {
		return process.Result{}, diagnoseGitRemoteError(ctx, err)
	}
	if scopeErr := s.checkRemoteScope(ctx); scopeErr != nil {
		return process.Result{}, scopeErr
	}
	if errors.Is(err, errGitReadOutputLimit) {
		return process.Result{}, errors.New("远程分支列表超过读取上限，请检查远端仓库")
	}
	if err != nil {
		return process.Result{}, diagnoseGitRemoteError(ctx, err)
	}
	return result, nil
}

func (s *MaterialsGitSyncService) VerifyHTTPSLogin(ctx context.Context, scope MaterialsGitScope) error {
	if err := validateMaterialsHTTPSLogin(scope.URL); err != nil {
		return err
	}
	_, err := s.readRemoteBranchHeads(ctx, scope, true, 2*time.Minute)
	return err
}

func (s *MaterialsGitSyncService) HTTPSLoginArguments(ctx context.Context, scope MaterialsGitScope) ([]string, error) {
	if err := validateMaterialsHTTPSLogin(scope.URL); err != nil {
		return nil, err
	}
	_, options, err := s.prepareRemoteGit(ctx, scope, "", false)
	if err != nil {
		return nil, err
	}
	return append(options, "ls-remote", "--heads", "--", scope.URL), nil
}

func validateMaterialsHTTPSLogin(repositoryURL string) error {
	if err := domain.ValidateRemoteGitURL(repositoryURL); err != nil {
		return err
	}
	if !MaterialsGitUsesHTTPS(repositoryURL) {
		return errors.New("请先确认并保存 HTTPS 仓库地址")
	}
	return nil
}

func (s *MaterialsGitSyncService) RemoteBranches(ctx context.Context, scope MaterialsGitScope) ([]string, error) {
	result, err := s.readRemoteBranchHeads(ctx, scope, false, 20*time.Second)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	branches := []string{}
	for _, line := range strings.Split(result.Stdout, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 || !strings.HasPrefix(fields[1], "refs/heads/") {
			return nil, errors.New("远程分支列表格式不完整，请重新读取")
		}
		branch := strings.TrimPrefix(fields[1], "refs/heads/")
		if branch == "" {
			return nil, errors.New("远程分支名称为空，请重新读取")
		}
		if !seen[branch] {
			seen[branch] = true
			branches = append(branches, branch)
		}
	}
	sort.Strings(branches)
	return branches, nil
}
