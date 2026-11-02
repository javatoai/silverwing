package platform

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"silverwing-go/internal/domain"
)

type skillGitRunner func([]string) (string, error)

func skillGitArguments(args []string) []string {
	options := []string{"-c", "core.symlinks=false"}
	if runtime.GOOS == "windows" {
		options = append(options, "-c", "core.longpaths=true")
	}
	return append(options, args...)
}

func (s *Service) skillGit(ctx context.Context, cfg domain.AppConfig) (skillGitRunner, error) {
	_, executable, _, err := s.resolveCLIExecutable(ctx, cfg, "git")
	if err != nil {
		return nil, err
	}
	return func(args []string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		args = skillGitArguments(args)
		result, err := s.runTool(ctx, cfg, domain.ProxyTargetGit, executable, args, s.paths.Codex, 5*time.Minute)
		return strings.TrimSpace(result.Stdout), err
	}, nil
}

func (s *Service) ensureSkillCheckout(ctx context.Context, cfg domain.AppConfig, source domain.SkillSource) (string, *SkillInstalledOrigin, error) {
	checkout, err := s.skillCheckoutPath(source)
	if err != nil {
		return "", nil, err
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if err := os.MkdirAll(filepath.Dir(checkout), 0o755); err != nil {
		return "", nil, err
	}
	if err := ensureNoExistingSymlinks(checkout); err != nil {
		return "", nil, err
	}
	run, err := s.skillGit(ctx, cfg)
	if err != nil {
		return "", nil, err
	}
	origin, err := prepareSkillCheckout(ctx, source, checkout, run)
	return checkout, origin, err
}

func prepareSkillCheckout(ctx context.Context, source domain.SkillSource, checkout string, run skillGitRunner) (*SkillInstalledOrigin, error) {
	command := run
	run = func(args []string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return command(args)
	}
	if err := source.Validate(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := ensureNoExistingSymlinks(checkout); err != nil {
		return nil, err
	}
	clone := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if exists(checkout) {
			if err := removeWithin(filepath.Dir(checkout), checkout); err != nil {
				return err
			}
		}
		_, err := run([]string{"clone", "--depth", "1", "--no-checkout", source.RepositoryURL, checkout})
		return err
	}
	if !isDir(filepath.Join(checkout, ".git")) || isSymlink(filepath.Join(checkout, ".git")) {
		if err := clone(); err != nil {
			return nil, err
		}
	}
	remote, err := run([]string{"-C", checkout, "config", "--get", "remote.origin.url"})
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(remote) != source.RepositoryURL {
		// Never fetch/pull a cache whose origin belongs to a different source.
		if err := clone(); err != nil {
			return nil, err
		}
		remote, err = run([]string{"-C", checkout, "config", "--get", "remote.origin.url"})
		if err != nil || strings.TrimSpace(remote) != source.RepositoryURL {
			return nil, fmt.Errorf("Skill 缓存仓库与来源不匹配")
		}
	}
	ref := skillSourceRef(source)
	if ref == "" {
		output, err := run([]string{"-C", checkout, "ls-remote", "--symref", "origin", "HEAD"})
		if err != nil {
			return nil, err
		}
		ref, err = skillDefaultBranch(output)
		if err != nil {
			return nil, err
		}
	}
	fetchRef := ref
	if skillSourceRef(source) == "" {
		fetchRef = "refs/heads/" + ref
	}
	if _, err := run([]string{"-C", checkout, "fetch", "--depth", "1", "origin", fetchRef}); err != nil {
		return nil, err
	}
	if _, err := run([]string{"-C", checkout, "checkout", "--force", "--detach", "FETCH_HEAD"}); err != nil {
		return nil, err
	}
	commit, err := run([]string{"-C", checkout, "rev-parse", "--verify", "HEAD"})
	if err != nil {
		return nil, err
	}
	commit = strings.TrimSpace(commit)
	if !validSkillCommit(commit) {
		return nil, fmt.Errorf("无法确认 Skill 来源提交版本")
	}
	if err := validateSkillCheckoutLinks(ctx, checkout); err != nil {
		return nil, err
	}
	return &SkillInstalledOrigin{RepositoryURL: strings.TrimSpace(remote), Ref: ref, Commit: commit}, nil
}

func skillDefaultBranch(output string) (string, error) {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 3 && fields[0] == "ref:" && strings.HasPrefix(fields[1], "refs/heads/") && fields[2] == "HEAD" {
			branch := strings.TrimPrefix(fields[1], "refs/heads/")
			if branch != "" && domain.ValidateGitRef(&branch) == nil {
				return branch, nil
			}
		}
	}
	return "", fmt.Errorf("无法确认 Skill 来源的实际默认分支")
}

func validateSkillCheckoutLinks(ctx context.Context, checkout string) error {
	return filepath.WalkDir(checkout, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("Skill 缓存不允许符号链接：%s", path)
		}
		return nil
	})
}

func verifySkillCheckoutOrigin(source domain.SkillSource, status SkillSourceSnapshot, checkout string, run skillGitRunner) (*SkillInstalledOrigin, error) {
	if err := validateSkillSourceSnapshot(source, status); err != nil {
		return nil, err
	}
	remote, err := run([]string{"-C", checkout, "config", "--get", "remote.origin.url"})
	if err != nil {
		return nil, err
	}
	commit, err := run([]string{"-C", checkout, "rev-parse", "--verify", "HEAD"})
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(remote) != source.RepositoryURL || strings.TrimSpace(commit) != status.ResolvedOrigin.Commit {
		return nil, fmt.Errorf("Skill 缓存仓库或提交已变更，请成功刷新来源后重试")
	}
	origin := *status.ResolvedOrigin
	return &origin, nil
}
