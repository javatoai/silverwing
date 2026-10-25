package production

import (
	"context"
	"errors"
	"fmt"

	"silverwing-go/internal/domain"
)

func (s *Service) checkResumedBaseline(ctx context.Context, b *Batch, c domain.AppConfig) error {
	i := b.Intent
	if i == nil || (i.Kind != "sync" && i.Kind != "create" && i.Kind != "main" && i.Kind != "release") {
		return nil
	}
	manual := ""
	if i.Preview.Manual {
		manual = i.Preview.ProductionTag
	}
	fresh, err := s.observe(ctx, b.Target, c, manual, b.ReleaseBranch, false)
	if err != nil {
		return err
	}
	if !sameProductionBaseline(i.Preview, fresh) {
		if i.Kind == "create" {
			return errors.New("线上生产基线已变化，初始化暂停。请结束本批次后重新创建；原工作区保留")
		}
		return errors.New("线上生产基线已变化，旧同步暂停。请重新检查生产基线并确认；原工作区保留")
	}
	return nil
}

// ReconcileEnded records only results that are already on the remote. It never
// resumes a merge, creates a branch/Tag, removes a worktree or pushes anything.
func (s *Service) ReconcileEnded(ctx context.Context, id string) (Batch, error) {
	return s.withRecord(ctx, id, true, func(b *Batch, _ domain.AppConfig) error {
		if !b.Ended {
			return errors.New("进行中的操作请使用校验并继续")
		}
		i := b.Intent
		if i == nil || i.ResultConfirmed {
			return nil
		}
		ref := "refs/heads/" + b.ReleaseBranch
		if i.Kind == "tag" {
			ref = "refs/tags/" + i.Tag
		} else if i.Merge != nil {
			ref = "refs/heads/" + i.Merge.TargetBranch
		}
		refs, err := s.remoteRefs(ctx, b.Target, ref)
		if err != nil {
			return err
		}
		if refs[ref] != "" {
			if err := s.fetchMissing(ctx, b.Target, refs, ref); err != nil {
				return err
			}
		}
		if i.Kind == "tag" {
			if i.Tag == "" || i.TagObject == "" || i.TagCommit == "" {
				return errors.New("原 Tag 记录缺少身份信息，不能确认")
			}
			if refs["refs/tags/"+i.Tag] != i.TagObject {
				return errors.New("远端 Tag 与原待确认对象不一致；记录保留，未执行任何推送")
			}
			commit, err := s.commit(ctx, b.Target, i.TagObject)
			if err != nil {
				return err
			}
			if commit != i.TagCommit {
				return errors.New("原 Tag 对象与已确认提交不匹配")
			}
			if _, err := s.validate(ctx, b.Target); err != nil {
				return err
			}
			b.Tags = append(b.Tags, Tag{Name: i.Tag, Commit: i.TagCommit, Object: i.TagObject, PushedAt: timestamp()})
			b.Intent, b.Message = nil, "已核对原生产 Tag 推送成功；批次仍已结束，可刷新构建状态"
			return s.save(b)
		}
		if i.Step == "release-create" && refs["refs/heads/"+b.ReleaseBranch] == b.MainSHA {
			if _, err := s.validate(ctx, b.Target); err != nil {
				return err
			}
			b.InitialSHA, b.ReleaseSHA, i.ResultConfirmed = b.MainSHA, b.MainSHA, true
			b.Message = "已核对 release 创建成功；批次仍已结束，未继续后续操作"
			return s.save(b)
		}
		m := i.Merge
		if m == nil {
			return errors.New("没有可核对的远端写入；原阶段记录保留")
		}
		if err := s.validateMergeLocation(b, m); err != nil {
			return err
		}
		after := m.AfterSHA
		if after == "" {
			after, err = s.resolvedWorktree(ctx, b.Target, m)
			if err != nil {
				return fmt.Errorf("原工作区尚无可确认的合并结果：%w", err)
			}
		}
		actual := refs["refs/heads/"+m.TargetBranch]
		if actual == "" {
			return errors.New("远端目标分支不存在，原记录保留")
		}
		for _, pair := range [][2]string{{m.BeforeSHA, after}, {m.SourceSHA, after}, {after, actual}} {
			ok, err := s.ancestor(ctx, b.Target, pair[0], pair[1])
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("远端未包含原合并结果，记录和工作区继续保留")
			}
		}
		if _, err := s.validate(ctx, b.Target); err != nil {
			return err
		}
		m.AfterSHA, i.ResultConfirmed = after, true
		if m.TargetBranch == b.ReleaseBranch {
			b.ReleaseSHA = after
		} else {
			b.MainSHA = after
		}
		if i.Kind == "feature" {
			b.Branches = append(b.Branches, MergedBranch{Branch: i.SourceBranch, SourceSHA: m.SourceSHA, Commit: after, At: timestamp()})
		}
		b.Message = "已核对原合并提交在远端历史中；批次仍已结束，后续阶段未继续，工作区保留"
		return s.save(b)
	})
}

func (s *Service) PendingPreview(ctx context.Context, id string) (Batch, Preview, error) {
	var p Preview
	b, err := s.withBatch(ctx, id, func(b *Batch, _ domain.AppConfig) error {
		if b.Intent == nil || b.Intent.Merge == nil {
			return errors.New("没有待核对的合并现场")
		}
		m := b.Intent.Merge
		ref := "refs/heads/" + m.TargetBranch
		refs, err := s.remoteRefs(ctx, b.Target, ref)
		if err != nil {
			return err
		}
		actual := refs["refs/heads/"+m.TargetBranch]
		if actual == "" {
			return errors.New("远端目标分支已删除")
		}
		if err = s.fetchMissing(ctx, b.Target, refs, ref); err != nil {
			return err
		}
		p = Preview{Target: b.Target, MainSHA: actual, ObservedAt: timestamp(), ProductionStatus: unknownStatus()}
		p.Commits, err = s.run(ctx, b.Target.Repository, "log", "--oneline", "--max-count=80", m.BeforeSHA+".."+actual)
		if err == nil {
			p.Diff, err = s.run(ctx, b.Target.Repository, "diff", "--stat", m.BeforeSHA, actual, "--")
		}
		return err
	})
	return b, p, err
}

// RebasePending requires a fresh explicit confirmation of the changed remote.
// It preserves the former worktree rather than resetting a user's resolution.
func (s *Service) RebasePending(ctx context.Context, id, expected string) (Batch, error) {
	return s.withBatch(ctx, id, func(b *Batch, c domain.AppConfig) error {
		if err := mergeAbortPending(b); err != nil {
			return err
		}
		if b.Intent == nil || b.Intent.Merge == nil {
			return errors.New("没有待恢复的合并")
		}
		if err := s.checkResumedBaseline(ctx, b, c); err != nil {
			return err
		}
		m := b.Intent.Merge
		ref := "refs/heads/" + m.TargetBranch
		refs, err := s.remoteRefs(ctx, b.Target, ref)
		if err != nil {
			return err
		}
		actual := refs["refs/heads/"+m.TargetBranch]
		if expected == "" || actual != expected {
			return errors.New("远端再次变化，请重新核对")
		}
		if err = s.fetchMissing(ctx, b.Target, refs, ref); err != nil {
			return err
		}
		ok, err := s.ancestor(ctx, b.Target, m.BeforeSHA, actual)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("远端已改写原目标历史，不能自动恢复")
		}
		contained, err := s.ancestor(ctx, b.Target, m.SourceSHA, actual)
		if err != nil {
			return err
		}
		if contained {
			m.AfterSHA = actual
		} else if actual != m.BeforeSHA {
			b.RetainedWorktrees = append(b.RetainedWorktrees, m.Worktree)
			if b.Intent.Step == "main" {
				b.Intent.Preview.MainSHA = actual
			} else {
				b.Intent.Preview.ReleaseSHA = actual
			}
			b.Intent.Merge = nil
		}
		if err = s.save(b); err != nil {
			return err
		}
		return s.advance(ctx, b, c, true)
	})
}

func (s *Service) ReprepareUnpushedTag(ctx context.Context, id string) (Batch, error) {
	return s.withBatch(ctx, id, func(b *Batch, _ domain.AppConfig) error {
		if b.Intent == nil || b.Intent.Kind != "tag" {
			return errors.New("没有待重新准备的 Tag 推送")
		}
		refs, err := s.remoteRefs(ctx, b.Target, "refs/tags/"+b.Intent.Tag)
		if err != nil {
			return err
		}
		if refs["refs/tags/"+b.Intent.Tag] == b.Intent.TagObject {
			return errors.New("原 Tag 已成功推送，请先校验并继续以记录真实结果")
		}
		b.Intent, b.TagPlan, b.Phase, b.Message = nil, nil, "READY", "已保留原 Tag 对象，请重新检查生产基线和版本编号"
		return s.save(b)
	})
}
