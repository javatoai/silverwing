package production

import (
	"context"
	"errors"
	"strings"

	"silverwing-go/internal/commandruntime"
	"silverwing-go/internal/domain"
)

// initialize persists an unversioned batch before its first main-branch write.
// A release version is fixed only by the separate create-release confirmation.
func (s *Service) initialize(ctx context.Context, preview Preview, version string, merge bool, expectedID string) (Batch, error) {
	var b Batch
	err := s.locks.WithRepository(preview.Target.CommonDirectory, func() error {
		p, c, err := s.checkPreview(ctx, preview, "")
		if err != nil {
			return err
		}
		if !merge {
			if err = ValidateVersion(version, p.Tags); err != nil {
				return err
			}
			if p.BranchHeads["release/"+version] != "" {
				return errors.New("远端 release 分支已存在，请选择其他基础版本")
			}
			if err = domain.NewGitWritePolicy(c.BlockedGitWriteBranches).RequireAllowed("release/"+version, "创建生产 release"); err != nil {
				return err
			}
			ok, e := s.ancestor(ctx, p.Target, p.ProductionSHA, p.MainSHA)
			if e != nil {
				return e
			}
			if !ok {
				return errors.New("生产 Tag 尚未合入主分支，请先点击合并生产 Tag")
			}
		}
		batches, err := s.Store.List()
		if err != nil {
			return err
		}
		for _, existing := range batches {
			if existing.Ended || existing.Target.CommonDirectory != p.Target.CommonDirectory {
				continue
			}
			if existing.ID != expectedID || existing.Target != p.Target || existing.InitialSHA != "" || existing.Intent != nil || (existing.Version != "" && existing.Version != version) {
				return errors.New("该仓库已有进行中的生产批次，请先继续或结束它")
			}
			b = existing
		}
		if expectedID != "" && b.ID != expectedID {
			return errors.New("确认的初始化批次已结束或变化，请重新选择批次")
		}
		if b.ID == "" {
			b = Batch{ID: newID(), Target: p.Target, CreatedAt: timestamp(), Branches: []MergedBranch{}, Tags: []Tag{}}
		}
		if merge {
			b.Phase, b.Intent = "SYNC_MAIN", &Intent{Kind: "main", Step: "main", Preview: p}
		} else {
			b.Version, b.ReleaseBranch, b.MainSHA = version, "release/"+version, p.MainSHA
			b.ProductionTag, b.ProductionSHA = p.ProductionTag, p.ProductionSHA
			b.Phase, b.Intent = "CREATE_RELEASE", &Intent{Kind: "create", Step: "release-create", Preview: p}
		}
		if err = s.save(&b); err != nil {
			b = Batch{}
			return err
		}
		if err = s.advance(ctx, &b, c); err != nil {
			b.Message = commandruntime.Redact(c, err.Error())
			if b.Phase != "CONFLICT" {
				b.Phase = "PAUSED"
			}
			return errors.Join(err, s.save(&b))
		}
		return nil
	})
	return b, err
}

// MergeProduction performs exactly one confirmed production-Tag -> main merge.
func (s *Service) MergeProduction(ctx context.Context, id string, preview Preview) (Batch, error) {
	if id == "" {
		return s.initialize(ctx, preview, "", true, "")
	}
	return s.withBatch(ctx, id, func(b *Batch, c domain.AppConfig) error {
		reconfirm := b.Intent != nil && b.Intent.Step == "main" && (b.Intent.Kind == "main" || b.Intent.Kind == "sync" || b.Intent.Kind == "create")
		if b.Intent != nil && !reconfirm {
			return errors.New("请先继续当前未完成操作")
		}
		if preview.Target != b.Target {
			return errors.New("确认的生产发布目标不匹配")
		}
		p, _, err := s.checkPreview(ctx, preview, b.ReleaseBranch)
		if err != nil {
			return err
		}
		if b.InitialSHA != "" && b.ReleaseSHA != p.ReleaseSHA {
			return errors.New("release 有批次之外的提交，请先核对并采纳远端状态")
		}
		if reconfirm && b.Intent.Merge != nil {
			m := b.Intent.Merge
			if err := s.validateMergeLocation(b, m); err != nil {
				return err
			}
			ok, err := s.ancestor(ctx, b.Target, m.BeforeSHA, p.MainSHA)
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("远端主分支已改写原目标历史，不能重新确认合并")
			}
			b.RetainedWorktrees = append(b.RetainedWorktrees, m.Worktree)
		}
		b.TagPlan = nil
		b.Intent = &Intent{Kind: "main", Step: "main", Preview: p}
		if err = s.save(b); err != nil {
			return err
		}
		return s.advance(ctx, b, c)
	})
}

// MergeMainToRelease never merges a production Tag or prepares/pushes a Tag.
func (s *Service) MergeMainToRelease(ctx context.Context, id string, preview Preview) (Batch, error) {
	return s.withBatch(ctx, id, func(b *Batch, c domain.AppConfig) error {
		if b.Intent != nil {
			return errors.New("请先继续当前未完成操作")
		}
		if b.InitialSHA == "" || preview.Target != b.Target {
			return errors.New("请先创建当前批次的 release 分支")
		}
		p, _, err := s.checkPreview(ctx, preview, b.ReleaseBranch)
		if err != nil {
			return err
		}
		if p.ReleaseSHA == "" || p.ReleaseSHA != b.ReleaseSHA {
			return errors.New("release 已变化，请先核对并采纳远端状态")
		}
		ok, err := s.ancestor(ctx, b.Target, p.ProductionSHA, p.MainSHA)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("生产 Tag 尚未合入主分支，请先点击合并生产 Tag")
		}
		if err = domain.NewGitWritePolicy(c.BlockedGitWriteBranches).RequireAllowed(b.ReleaseBranch, "合并主分支到 release"); err != nil {
			return err
		}
		b.MainSHA, b.ProductionTag, b.ProductionSHA = p.MainSHA, p.ProductionTag, p.ProductionSHA
		b.TagPlan = nil
		b.Intent = &Intent{Kind: "release", Step: "release-sync", Preview: p}
		if err = s.save(b); err != nil {
			return err
		}
		return s.advance(ctx, b, c)
	})
}

// ReleaseVersion accepts the full branch name shown in the UI. The fixed
// prefix is parsed once, and version policy remains shared with creation.
func ReleaseVersion(branch string, tags []string) (string, error) {
	branch = strings.TrimSpace(branch)
	if !strings.HasPrefix(branch, "release/") {
		return "", errors.New("分支名必须以 release/ 开头")
	}
	version := strings.TrimPrefix(branch, "release/")
	return version, ValidateVersion(version, tags)
}
