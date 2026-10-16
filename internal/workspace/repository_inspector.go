package workspace

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"silverwing-go/internal/domain"
)

type RepositoryInspector interface {
	Inspect(selectedDirectory string) (domain.RepositoryConfig, error)
}

// GitRepositoryInspector validates exactly one user-selected main checkout. It
// never searches parent or child directories to guess which repository was meant.
type GitRepositoryInspector struct{ Git *GitClient }

func NewRepositoryInspector(git *GitClient) *GitRepositoryInspector {
	if git == nil {
		git = NewGitClient(nil, "")
	}
	return &GitRepositoryInspector{Git: git}
}

func (i *GitRepositoryInspector) Inspect(selectedDirectory string) (domain.RepositoryConfig, error) {
	return i.InspectContext(context.Background(), selectedDirectory)
}

func (i *GitRepositoryInspector) InspectContext(ctx context.Context, selectedDirectory string) (domain.RepositoryConfig, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return domain.RepositoryConfig{}, err
	}
	selected, err := filepath.Abs(selectedDirectory)
	if err != nil {
		return domain.RepositoryConfig{}, err
	}
	selected = filepath.Clean(selected)
	info, err := statPath(selected)
	if err != nil || !info.IsDir() {
		return domain.RepositoryConfig{}, fmt.Errorf("目录不存在：%s", selectedDirectory)
	}
	result, err := i.Git.RunContext(ctx, selected, 2*time.Minute, true, true, "rev-parse", "--show-toplevel")
	if err != nil {
		return domain.RepositoryConfig{}, fmt.Errorf("所选目录不是 Git 仓库：%s: %w", selectedDirectory, err)
	}
	root, err := canonicalPath(strings.TrimSpace(result.Stdout))
	if err != nil {
		return domain.RepositoryConfig{}, err
	}
	gitMarker, err := statPath(filepath.Join(root, ".git"))
	if err != nil || !gitMarker.IsDir() {
		return domain.RepositoryConfig{}, fmt.Errorf("请选择主 Git 仓库目录，不能添加 Linked Worktree 或子模块：%s", root)
	}
	result, err = i.Git.RunContext(ctx, root, 2*time.Minute, true, true, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return domain.RepositoryConfig{}, err
	}
	common, err := canonicalPath(strings.TrimSpace(result.Stdout))
	if err != nil {
		return domain.RepositoryConfig{}, err
	}
	result, err = i.Git.RunContext(ctx, root, 2*time.Minute, false, true, "remote", "get-url", "origin")
	if err != nil {
		return domain.RepositoryConfig{}, err
	}
	origin := ""
	if result.ExitCode == 0 {
		origin = strings.TrimSpace(result.Stdout)
	}
	if origin != "" {
		if err := requireCredentialFreeURL(origin); err != nil {
			return domain.RepositoryConfig{}, err
		}
	}
	result, err = i.Git.RunContext(ctx, root, 2*time.Minute, true, true, "branch", "--show-current")
	if err != nil {
		return domain.RepositoryConfig{}, err
	}
	branch := strings.TrimSpace(result.Stdout)
	var originPtr, branchPtr, defaultBranchPtr *string
	if origin != "" {
		originPtr = &origin
		result, branchErr := i.Git.RunContext(ctx, root, 2*time.Minute, false, true, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD")
		if branchErr != nil {
			return domain.RepositoryConfig{}, branchErr
		}
		defaultBranch := ""
		if result.ExitCode == 0 {
			defaultBranch = strings.TrimPrefix(strings.TrimSpace(result.Stdout), "origin/")
		}
		if defaultBranch != "" {
			defaultBranchPtr = &defaultBranch
		}
	}
	if branch != "" {
		branchPtr = &branch
	}
	name := filepath.Base(root)
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = root
	}
	if err := ctx.Err(); err != nil {
		return domain.RepositoryConfig{}, err
	}
	return domain.RepositoryConfig{
		ID:                  repositoryID(common),
		Name:                name,
		RootPath:            root,
		GitCommonDirectory:  common,
		OriginURL:           originPtr,
		CurrentBranch:       branchPtr,
		DefaultRemoteBranch: defaultBranchPtr,
	}, nil
}

func repositoryID(commonDirectory string) string {
	identity := strings.ToLower(filepath.Clean(commonDirectory))
	digest := sha256.Sum256([]byte(identity))
	return fmt.Sprintf("repo-%x", digest[:8])
}

func requireCredentialFreeURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil {
		return nil
	}
	scheme := strings.ToLower(parsed.Scheme)
	if (scheme == "http" || scheme == "https") && parsed.User != nil && parsed.User.String() != "" {
		return fmt.Errorf("origin URL 包含内嵌凭证，请改用 Git Credential Manager 或 SSH 后再添加")
	}
	return nil
}
