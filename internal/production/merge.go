package production

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (s *Service) ensureMerge(b *Batch, target, source, before string, noFF bool) error {
	if b.Intent.Merge != nil {
		return nil
	}
	if source == "" || before == "" {
		return errors.New("合并源或目标提交缺失")
	}
	id := newID()
	b.Intent.Merge = &Merge{ID: id, TargetBranch: target, SourceSHA: source, BeforeSHA: before, NoFF: noFF,
		Worktree: filepath.Join(s.Paths.Home, "production-worktrees", b.ID, id), Branch: "sw-production/" + b.ID + "/" + id}
	return s.save(b)
}

// The lease is an atomic expected-ref check, not permission to rewrite history.
// Existing targets must be ancestors of the result. An empty expectation only
// permits creation; a competing branch, even at an ancestor, is rejected.
func (s *Service) pushBranch(ctx context.Context, t Target, branch, before, after string) error {
	if before != "" {
		ok, err := s.ancestor(ctx, t, before, after)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("推送结果未保留原目标历史，不能推送")
		}
	}
	ref := "refs/heads/" + branch
	_, err := s.run(ctx, t.Repository, "push", "--no-follow-tags", "--recurse-submodules=no", "--force-with-lease="+ref+":"+before, t.Remote, after+":"+ref)
	return err
}

func (s *Service) mergeAndPush(ctx context.Context, b *Batch, recovering bool) (string, error) {
	m, t := b.Intent.Merge, b.Target
	if err := s.validateMergeLocation(b, m); err != nil {
		return "", err
	}
	if recovering {
		refs, err := s.remoteRefs(ctx, t, "refs/heads/"+m.TargetBranch)
		if err != nil {
			return "", err
		}
		actual := refs["refs/heads/"+m.TargetBranch]
		if m.AfterSHA != "" && actual == m.AfterSHA {
			s.cleanupWorktree(ctx, b, m)
			return m.AfterSHA, nil
		}
		if m.AfterSHA == "" && actual != m.BeforeSHA {
			// A user may have committed and pushed in the retained IDE worktree.
			if resolved, e := s.resolvedWorktree(ctx, t, m); e == nil && resolved == actual {
				m.AfterSHA = resolved
				if err = s.save(b); err != nil {
					return "", err
				}
				s.cleanupWorktree(ctx, b, m)
				return resolved, nil
			}
		}
		if actual != m.BeforeSHA {
			return "", fmt.Errorf("远端 %s 已变化：预期 %s，实际 %s；已保留操作现场", m.TargetBranch, m.BeforeSHA, actual)
		}
	}
	contained, err := s.ancestor(ctx, t, m.SourceSHA, m.BeforeSHA)
	if err != nil {
		return "", err
	}
	if contained {
		return m.BeforeSHA, nil
	}
	if m.AfterSHA == "" {
		needsMerge := false
		if _, err = os.Lstat(m.Worktree); os.IsNotExist(err) {
			if err = os.MkdirAll(filepath.Dir(m.Worktree), 0o700); err != nil {
				return "", err
			}
			if _, err = s.run(ctx, t.Repository, "worktree", "add", "-b", m.Branch, m.Worktree, m.BeforeSHA); err != nil {
				return "", err
			}
			needsMerge = true
		} else if err != nil {
			return "", err
		} else {
			head, readErr := s.checkedWorktreeHead(ctx, t, m)
			if errors.Is(readErr, errMergeUncommitted) {
				return "", s.conflict(ctx, b, m, "请在 IDE 中解决冲突并完成 merge commit，再校验并继续")
			}
			if readErr != nil {
				return "", readErr
			}
			if head == m.BeforeSHA {
				// An interruption may have happened after worktree creation
				// but before Git started merging. Its checked state is reusable.
				needsMerge = true
			} else {
				m.AfterSHA, err = s.resolvedWorktree(ctx, t, m)
				if err != nil {
					return "", err
				}
			}
		}
		if needsMerge {
			// Trust Git's successful merge in an app-owned worktree. Recovery
			// and manually resolved commits take the identity/parent checks.
			mode := "--ff"
			if m.NoFF {
				mode = "--no-ff"
			}
			_, mergeErr := s.run(ctx, m.Worktree, "merge", mode, "--no-edit", "--no-autostash", "--no-squash", "--commit", m.SourceSHA)
			if mergeErr != nil {
				check, checkErr := s.Git.RunContext(ctx, m.Worktree, time.Minute, false, true, "rev-parse", "--verify", "MERGE_HEAD")
				if checkErr == nil && check.ExitCode == 0 {
					return "", s.conflict(ctx, b, m, "合并发生冲突，请在 IDE 中处理并提交")
				}
				return "", mergeErr
			}
			m.AfterSHA, err = s.run(ctx, m.Worktree, "rev-parse", "HEAD")
			if err != nil {
				return "", err
			}
		}
		if err = s.save(b); err != nil {
			return "", err
		}
	}
	if _, err = s.validate(ctx, t); err != nil {
		return "", err
	}
	if err = s.pushBranch(ctx, t, m.TargetBranch, m.BeforeSHA, m.AfterSHA); err != nil {
		return "", err
	}
	s.cleanupWorktree(ctx, b, m)
	return m.AfterSHA, nil
}

func (s *Service) validateMergeLocation(b *Batch, m *Merge) error {
	if m == nil || !batchIDPattern.MatchString(m.ID) || !batchIDPattern.MatchString(b.ID) {
		return errors.New("隔离工作区标识无效")
	}
	expected := filepath.Join(s.Paths.Home, "production-worktrees", b.ID, m.ID)
	if filepath.Clean(m.Worktree) != filepath.Clean(expected) || m.Branch != "sw-production/"+b.ID+"/"+m.ID {
		return errors.New("隔离工作区不属于当前生产操作")
	}
	home, err := filepath.EvalSymlinks(s.Paths.Home)
	if err != nil {
		return err
	}
	for current := m.Worktree; filepath.Clean(current) != filepath.Clean(s.Paths.Home); current = filepath.Dir(current) {
		if current == filepath.Dir(current) {
			return errors.New("隔离工作区路径无效")
		}
		if real, err := filepath.EvalSymlinks(current); err == nil {
			rel, _ := filepath.Rel(s.Paths.Home, current)
			if filepath.Clean(real) != filepath.Clean(filepath.Join(home, rel)) {
				return errors.New("隔离工作区路径包含重定向")
			}
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

var errMergeUncommitted = errors.New("合并尚未提交")

func (s *Service) checkedWorktreeHead(ctx context.Context, t Target, m *Merge) (string, error) {
	common, err := s.run(ctx, m.Worktree, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil || filepath.Clean(common) != filepath.Clean(t.CommonDirectory) {
		return "", errors.New("隔离工作区身份已变化")
	}
	branch, err := s.run(ctx, m.Worktree, "symbolic-ref", "--short", "HEAD")
	if err != nil || branch != m.Branch {
		return "", errors.New("隔离工作区分支已变化")
	}
	r, err := s.Git.RunContext(ctx, m.Worktree, time.Minute, false, true, "rev-parse", "--verify", "MERGE_HEAD")
	if err != nil {
		return "", err
	}
	if r.ExitCode == 0 {
		return "", errMergeUncommitted
	}
	status, err := s.run(ctx, m.Worktree, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return "", err
	}
	if status != "" {
		return "", errors.New("隔离工作区尚未提交干净")
	}
	head, err := s.run(ctx, m.Worktree, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return head, nil
}

func (s *Service) resolvedWorktree(ctx context.Context, t Target, m *Merge) (string, error) {
	head, err := s.checkedWorktreeHead(ctx, t, m)
	if err != nil {
		return "", err
	}
	for _, parent := range []string{m.BeforeSHA, m.SourceSHA} {
		ok, err := s.ancestor(ctx, t, parent, head)
		if err != nil {
			return "", err
		}
		if !ok {
			return "", errors.New("合并结果未包含确认的源和目标提交")
		}
	}
	return head, nil
}

func (s *Service) conflict(ctx context.Context, b *Batch, m *Merge, message string) error {
	files, err := s.run(ctx, m.Worktree, "diff", "--name-only", "--diff-filter=U")
	if err != nil {
		return err
	}
	m.Conflicts = nil
	if files != "" {
		m.Conflicts = strings.Split(files, "\n")
	}
	b.Phase, b.Message = "CONFLICT", message
	if err := s.save(b); err != nil {
		return err
	}
	return errors.New(message)
}

func (s *Service) cleanupWorktree(ctx context.Context, b *Batch, m *Merge) {
	if _, err := os.Lstat(m.Worktree); os.IsNotExist(err) {
		return
	}
	status, err := s.run(ctx, m.Worktree, "status", "--porcelain", "--untracked-files=all")
	if err == nil && status == "" {
		_, err = s.run(ctx, b.Target.Repository, "worktree", "remove", m.Worktree)
	}
	if err != nil || status != "" {
		b.RetainedWorktrees = append(b.RetainedWorktrees, m.Worktree)
	}
}
