package delivery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type recoveredTagWorktree struct {
	exists    bool
	mergedSHA string
	conflict  *mergeConflictError
}

func (s *Service) recoverTagWorktree(ctx context.Context, repository, path, base, source string) (recoveredTagWorktree, error) {
	if path == "" {
		return recoveredTagWorktree{}, nil
	}
	expected, err := s.conflictWorktreePath(filepath.Base(path))
	if err != nil {
		return recoveredTagWorktree{}, tagWorktreeNeedsReview(path, err)
	}
	if !samePathSpelling(path, expected) {
		return recoveredTagWorktree{}, tagWorktreeNeedsReview(path, errors.New("路径与 Tag 操作的固定工作区不匹配"))
	}
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return recoveredTagWorktree{}, nil
	} else if err != nil {
		return recoveredTagWorktree{}, tagWorktreeNeedsReview(path, err)
	}

	worktree, err := s.inspectTagWorktree(ctx, repository, path, base, source)
	if err != nil {
		return recoveredTagWorktree{}, tagWorktreeNeedsReview(path, err)
	}
	return worktree, nil
}

func (s *Service) inspectTagWorktree(ctx context.Context, repository, path, base, source string) (recoveredTagWorktree, error) {
	gitDir, err := s.tagWorktreeGitDir(ctx, repository, path)
	if err != nil {
		return recoveredTagWorktree{}, err
	}
	head, err := s.resolve(ctx, path, "HEAD")
	if err != nil {
		return recoveredTagWorktree{}, err
	}
	mergeHead, err := os.ReadFile(filepath.Join(gitDir, "MERGE_HEAD"))
	if err == nil {
		if head != base || strings.TrimSuffix(string(mergeHead), "\n") != source {
			return recoveredTagWorktree{}, errors.New("HEAD 或 MERGE_HEAD 与当前目标和源提交不匹配")
		}
		files, err := s.conflictFiles(ctx, path)
		if err != nil {
			return recoveredTagWorktree{}, err
		}
		return recoveredTagWorktree{exists: true, conflict: &mergeConflictError{files: files, path: path, targetSHA: base}}, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return recoveredTagWorktree{}, err
	}
	if err := s.ensureCleanFeatureWorktree(ctx, path); err != nil {
		return recoveredTagWorktree{}, err
	}
	if head == base {
		if err := s.requireNoExtraTagWorktreeFiles(ctx, path); err != nil {
			return recoveredTagWorktree{}, err
		}
		return recoveredTagWorktree{exists: true}, nil
	}
	if head == source {
		if fastForward, err := s.isAncestor(ctx, path, base, source); err != nil || fastForward {
			return recoveredTagWorktree{exists: true, mergedSHA: head}, err
		}
	}
	parents, err := s.run(ctx, path, 2*time.Minute, true, true, "rev-list", "--parents", "-n", "1", "HEAD")
	if err != nil {
		return recoveredTagWorktree{}, err
	}
	fields := strings.Fields(parents.Stdout)
	if len(fields) != 3 || fields[0] != head || fields[1] != base || fields[2] != source {
		return recoveredTagWorktree{}, errors.New("已有提交与当前目标、源提交或合并方向不匹配")
	}
	return recoveredTagWorktree{exists: true, mergedSHA: head}, nil
}

func (s *Service) tagWorktreeGitDir(ctx context.Context, repository, path string) (string, error) {
	gitFile := filepath.Join(path, ".git")
	info, err := os.Lstat(gitFile)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("已有目录缺少关联工作区的 .git 文件")
	}
	common, err := s.gitCommonDirectory(ctx, repository)
	if err != nil {
		return "", err
	}
	actual, err := s.gitCommonDirectory(ctx, path)
	if err != nil {
		return "", err
	}
	top, err := s.gitTopLevel(ctx, path)
	if err != nil {
		return "", err
	}
	branch, err := s.currentBranch(ctx, path)
	if err != nil {
		return "", err
	}
	if branch != "" || !sameExistingTagDirectory(actual, common) || !sameExistingTagDirectory(top, path) {
		return "", errors.New("已有目录不是原仓库的 detached worktree")
	}
	result, err := s.run(ctx, path, 2*time.Minute, true, true, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return "", err
	}
	gitDir := strings.TrimSpace(result.Stdout)
	if !sameExistingTagDirectory(filepath.Dir(gitDir), filepath.Join(common, "worktrees")) {
		return "", errors.New("关联工作区的 Git 元数据目录与原仓库不匹配")
	}
	registered, err := os.ReadFile(filepath.Join(gitDir, "gitdir"))
	if err != nil {
		return "", err
	}
	backlink := strings.TrimSuffix(strings.TrimSuffix(string(registered), "\n"), "\r")
	if !filepath.IsAbs(backlink) {
		backlink = filepath.Join(gitDir, backlink)
	}
	registeredInfo, err := os.Stat(backlink)
	if err != nil || !samePathSpelling(backlink, gitFile) || !os.SameFile(registeredInfo, info) {
		return "", errors.New("原仓库的工作区登记路径与现有目录不匹配")
	}
	return gitDir, nil
}

func (s *Service) requireNoExtraTagWorktreeFiles(ctx context.Context, path string) error {
	result, err := s.run(ctx, path, 2*time.Minute, true, true, "ls-files", "--others", "-z")
	if err != nil {
		return err
	}
	if result.Stdout != "" || result.StdoutTruncated {
		return errors.New("工作区仍有未跟踪或被忽略的文件")
	}
	return nil
}

func (s *Service) prepareTagTargetMerge(ctx context.Context, repository, path, base, source string, recovered recoveredTagWorktree) (string, error) {
	if recovered.mergedSHA != "" {
		s.notifyTag("merging", "复用中断前已完成的合并提交", "", nil)
		return recovered.mergedSHA, nil
	}
	if !recovered.exists {
		if err := s.addDetachedWorktree(ctx, repository, path, base); err != nil {
			return "", tagWorktreeNeedsReview(path, err)
		}
	}
	s.notifyTag("merging", "合并目标分支", "", nil)
	merge, err := s.run(ctx, path, 10*time.Minute, false, false, "merge", "--no-edit", "--no-overwrite-ignore", source)
	if err != nil {
		return "", tagWorktreeNeedsReview(path, err)
	}
	if merge.ExitCode != 0 {
		files, err := s.conflictFiles(ctx, path)
		if err != nil {
			return "", tagWorktreeNeedsReview(path, err)
		}
		if len(files) > 0 {
			return "", &mergeConflictError{files: files, path: path, targetSHA: base}
		}
		return "", tagWorktreeNeedsReview(path, gitFailure("合并目标分支失败", merge))
	}
	return s.resolve(ctx, path, "HEAD")
}

func (s *Service) removeCleanTagWorktree(ctx context.Context, repository, path string) error {
	if err := s.requireNoExtraTagWorktreeFiles(ctx, path); err != nil {
		return tagWorktreeNeedsReview(path, err)
	}
	if err := s.removeWorktree(ctx, repository, path, false); err != nil {
		return tagWorktreeNeedsReview(path, err)
	}
	return nil
}

func tagWorktreeNeedsReview(path string, err error) error {
	return fmt.Errorf("请检查或备份 Tag 工作区 %s 并核对其 Git 登记后重试：%w", path, err)
}
