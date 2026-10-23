package production

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"silverwing-go/internal/domain"
)

const abortingMerge = "ABORTING"

func mergeAbortPending(b *Batch) error {
	if b.Phase == abortingMerge {
		return errors.New("本次合并正在撤回，请点击撤回合并完成核对，不能继续原合并")
	}
	return nil
}

// AbortMerge only discards the uncommitted merge owned by this confirmation.
// The existing phase is a durable intent: cancellation or a failed final save
// must not make Resume start the original merge again.
func (s *Service) AbortMerge(ctx context.Context, id string, target Target, expected Merge) (Batch, error) {
	return s.withBatch(ctx, id, func(b *Batch, _ domain.AppConfig) error {
		if b.Target != target || b.Intent == nil || b.Intent.Merge == nil {
			return errors.New("待撤回的合并已变化，请重新确认")
		}
		m := b.Intent.Merge
		if m.ID != expected.ID || m.SourceSHA != expected.SourceSHA || m.BeforeSHA != expected.BeforeSHA ||
			m.Worktree != expected.Worktree || m.TargetBranch != expected.TargetBranch || m.Branch != expected.Branch {
			return errors.New("待撤回的合并已变化，请重新确认")
		}
		if b.Intent.ResultConfirmed || m.AfterSHA != "" || expected.AfterSHA != "" {
			return errors.New("合并结果已生成或已确认，请核对后继续；不能撤回已提交的合并")
		}
		if b.Phase != "CONFLICT" && b.Phase != abortingMerge {
			return errors.New("当前没有可撤回的冲突合并")
		}
		if err := s.validateMergeLocation(b, m); err != nil {
			return err
		}
		inMerge, err := s.abortMergeState(ctx, b.Target, m)
		if err != nil {
			return err
		}
		if !inMerge && b.Phase != abortingMerge {
			return errors.New("未检测到本次未提交合并，请核对工作区后再继续")
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		b.Phase, b.Message, b.TagPlan = abortingMerge, "正在撤回本次合并…", nil
		if err := s.save(b); err != nil {
			return err
		}
		if inMerge {
			// Recheck immediately before the local mutation. No reset/clean fallback.
			if _, err := s.validate(ctx, b.Target); err != nil {
				return err
			}
			if stillMerging, err := s.abortMergeState(ctx, b.Target, m); err != nil {
				return err
			} else if stillMerging {
				if _, err := s.run(ctx, m.Worktree, "merge", "--abort"); err != nil {
					return err
				}
			}
		}
		if remaining, err := s.abortMergeState(ctx, b.Target, m); err != nil {
			return err
		} else if remaining {
			return errors.New("合并尚未撤回，已保留现场，请重试撤回合并")
		}
		if _, err := s.validate(ctx, b.Target); err != nil {
			return err
		}
		// Keep the workspace, including any unrelated untracked files. Saving a
		// copy leaves the durable abort intent intact if the completion write fails.
		completed := *b
		completed.RetainedWorktrees = slices.Clone(b.RetainedWorktrees)
		if !slices.Contains(completed.RetainedWorktrees, m.Worktree) {
			completed.RetainedWorktrees = append(completed.RetainedWorktrees, m.Worktree)
		}
		completed.Intent, completed.TagPlan = nil, nil
		completed.Phase, completed.Message = "READY", "本次合并已撤回"
		if err := s.save(&completed); err != nil {
			return errors.Join(errors.New("合并已撤回，保存结果失败；请重试撤回以完成核对"), err)
		}
		*b = completed
		return nil
	})
}

// Missing MERGE_HEAD is success only after a persisted abort intent. A user
// commit, checkout, replaced repository or foreign merge must never be reset.
func (s *Service) abortMergeState(ctx context.Context, target Target, m *Merge) (bool, error) {
	common, err := s.run(ctx, m.Worktree, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return false, err
	}
	if filepath.Clean(common) != filepath.Clean(target.CommonDirectory) {
		return false, errors.New("隔离工作区身份已变化")
	}
	branch, err := s.run(ctx, m.Worktree, "symbolic-ref", "--short", "HEAD")
	if err != nil || branch != m.Branch {
		return false, errors.New("隔离工作区分支已变化，不能撤回")
	}
	head, err := s.run(ctx, m.Worktree, "rev-parse", "HEAD")
	if err != nil {
		return false, err
	}
	if head != m.BeforeSHA {
		return false, errors.New("合并已提交或工作区提交已变化，请核对后继续；不能撤回已提交的合并")
	}
	result, err := s.Git.RunContext(ctx, m.Worktree, time.Minute, false, true, "rev-parse", "--verify", "--quiet", "MERGE_HEAD")
	if err != nil {
		return false, err
	}
	if result.ExitCode == 1 {
		return false, nil
	}
	if result.ExitCode != 0 || strings.TrimSpace(result.Stdout) != m.SourceSHA {
		return false, errors.New("工作区的合并来源已变化，不能撤回")
	}
	original, err := s.run(ctx, m.Worktree, "rev-parse", "ORIG_HEAD")
	if err != nil || original != m.BeforeSHA {
		return false, errors.New("合并前提交已变化，不能撤回")
	}
	autostash, err := s.Git.RunContext(ctx, m.Worktree, time.Minute, false, true, "rev-parse", "--verify", "--quiet", "MERGE_AUTOSTASH")
	if err != nil {
		return false, err
	}
	if autostash.ExitCode != 1 {
		return false, errors.New("工作区包含额外的自动暂存状态，请先人工核对")
	}
	return true, nil
}
