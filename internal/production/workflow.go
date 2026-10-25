package production

import (
	"context"
	"errors"
	"path/filepath"
	"silverwing-go/internal/domain"
	"strings"
)

func (s *Service) checkPreview(ctx context.Context, p Preview, release string) (Preview, domain.AppConfig, error) {
	c, err := s.validate(ctx, p.Target)
	if err != nil {
		return p, c, err
	}
	if p.ProductionTag == "" || p.ProductionSHA == "" {
		return p, c, errors.New("请先确认生产基线 Tag")
	}
	manual := ""
	if p.Manual {
		manual = p.ProductionTag
	}
	fresh, err := s.observe(ctx, p.Target, c, manual, release, false)
	if err != nil {
		return fresh, c, err
	}
	if !sameProductionBaseline(p, fresh) || fresh.MainSHA != p.MainSHA {
		return fresh, c, errors.New("生产基线或主分支已变化，请重新检查并确认")
	}
	if release != "" && fresh.BranchHeads[release] != p.ReleaseSHA {
		return fresh, c, errors.New("release 已变化，请重新检查差异并确认")
	}
	fresh.ReleaseSHA = p.ReleaseSHA
	return fresh, c, nil
}

func (s *Service) Create(ctx context.Context, preview Preview, version, expectedID string) (Batch, error) {
	return s.initialize(ctx, preview, version, false, expectedID)
}

func (s *Service) InspectBatch(ctx context.Context, id, manual string) (Batch, Preview, error) {
	var p Preview
	b, err := s.withBatch(ctx, id, func(b *Batch, c domain.AppConfig) error {
		var e error
		p, e = s.observe(ctx, b.Target, c, manual, b.ReleaseBranch, true)
		if e == nil {
			p.ReleaseSHA = p.BranchHeads[b.ReleaseBranch]
			if p.ReleaseSHA != "" && b.ReleaseSHA != "" && p.ReleaseSHA != b.ReleaseSHA {
				var diff, commits string
				diff, e = s.run(ctx, b.Target.Repository, "diff", "--stat", b.ReleaseSHA, p.ReleaseSHA, "--")
				if e == nil {
					commits, e = s.run(ctx, b.Target.Repository, "log", "--oneline", "--max-count=80", b.ReleaseSHA+".."+p.ReleaseSHA)
				}
				if e == nil {
					p.Diff += "\n\n远端 release 相对本地批次的文件变化：\n" + diff
					p.Commits += "\n\n远端 release 相对本地批次的新增提交（最多 80 条）：\n" + commits
				}
			}
		}
		return e
	})
	return b, p, err
}

func (s *Service) FeaturePreview(ctx context.Context, id, branch string) (Batch, Preview, error) {
	var p Preview
	b, err := s.withBatch(ctx, id, func(b *Batch, _ domain.AppConfig) error {
		refs, err := s.remoteRefs(ctx, b.Target, "refs/heads/"+branch, "refs/heads/"+b.ReleaseBranch)
		if err != nil {
			return err
		}
		source, target := refs["refs/heads/"+branch], refs["refs/heads/"+b.ReleaseBranch]
		if branch == "" || source == "" || target == "" {
			return errors.New("源分支或 release 不存在")
		}
		if err = s.fetchMissing(ctx, b.Target, refs, "refs/heads/"+branch, "refs/heads/"+b.ReleaseBranch); err != nil {
			return err
		}
		p = Preview{Target: b.Target, ReleaseSHA: target, BranchHeads: map[string]string{branch: source}, ObservedAt: timestamp(), ProductionStatus: unknownStatus()}
		p.Commits, err = s.run(ctx, b.Target.Repository, "log", "--oneline", "--max-count=80", target+".."+source)
		if err != nil {
			return err
		}
		p.Diff, err = s.run(ctx, b.Target.Repository, "diff", "--stat", target+"..."+source, "--")
		return err
	})
	return b, p, err
}

func (s *Service) MergeFeature(ctx context.Context, id, branch, sourceSHA, releaseSHA string) (Batch, error) {
	return s.withBatch(ctx, id, func(b *Batch, c domain.AppConfig) error {
		if b.Intent != nil {
			return errors.New("请先继续或结束当前未完成操作")
		}
		if branch == b.Target.MainBranch || branch == b.ReleaseBranch || branch == "" || sourceSHA == "" {
			return errors.New("请选择一个有效的远端功能分支")
		}
		refs, err := s.remoteRefs(ctx, b.Target, "refs/heads/"+branch, "refs/heads/"+b.ReleaseBranch)
		if err != nil {
			return err
		}
		if refs["refs/heads/"+branch] != sourceSHA || refs["refs/heads/"+b.ReleaseBranch] != releaseSHA || releaseSHA == "" {
			return errors.New("源分支或 release 已变化，请重新检查并确认")
		}
		if err := domain.NewGitWritePolicy(c.BlockedGitWriteBranches).RequireAllowed(b.ReleaseBranch, "合并功能分支"); err != nil {
			return err
		}
		if b.ReleaseSHA != releaseSHA {
			return errors.New("release 有批次之外的提交，请先核对并采纳远端状态")
		}
		if err = s.fetchMissing(ctx, b.Target, refs, "refs/heads/"+branch, "refs/heads/"+b.ReleaseBranch); err != nil {
			return err
		}
		contained, err := s.ancestor(ctx, b.Target, sourceSHA, releaseSHA)
		if err != nil {
			return err
		}
		if contained {
			if _, err := s.validate(ctx, b.Target); err != nil {
				return err
			}
			b.Message = "该分支目标提交已包含，无需重复合并"
			if err := s.save(b); err != nil {
				return err
			}
			batchProgress(ctx, b, 4, "skipped", b.Message)
			return nil
		}
		b.TagPlan = nil
		b.Intent = &Intent{Kind: "feature", Step: "feature", SourceBranch: branch, SourceSHA: sourceSHA, Preview: Preview{ReleaseSHA: releaseSHA}}
		if err = s.save(b); err != nil {
			return err
		}
		return s.advance(ctx, b, c, false)
	})
}

// CheckForTag is a read-only Git check. Missing merges require their own buttons.
func (s *Service) CheckForTag(ctx context.Context, id string, preview Preview) (Batch, error) {
	return s.withBatch(ctx, id, func(b *Batch, _ domain.AppConfig) error {
		if preview.Target != b.Target || b.InitialSHA == "" {
			return errors.New("请先创建并核对当前 release")
		}
		if err := s.beginTagCheck(b); err != nil {
			return err
		}
		p, _, err := s.checkPreview(ctx, preview, b.ReleaseBranch)
		if err != nil {
			return err
		}
		return s.prepareCheckedTag(ctx, b, p)
	})
}

func (s *Service) Resume(ctx context.Context, id string) (Batch, error) {
	return s.withBatch(ctx, id, func(b *Batch, c domain.AppConfig) error {
		if err := mergeAbortPending(b); err != nil {
			return err
		}
		if b.Intent == nil {
			return errors.New("没有待恢复的操作，请重新检查后操作")
		}
		if err := s.checkResumedBaseline(ctx, b, c); err != nil {
			return err
		}
		return s.advance(ctx, b, c, true)
	})
}

// AcceptRemote is explicit adoption, never an implicit retry. Planned source and
// target commits must still be ancestors; no remote history is rewritten.
func (s *Service) AcceptRemote(ctx context.Context, id, expectedSHA string) (Batch, error) {
	return s.withBatch(ctx, id, func(b *Batch, c domain.AppConfig) error {
		if b.Intent != nil {
			return errors.New("请先完成当前操作；远端变化时可以结束批次并保留工作区")
		}
		ref := "refs/heads/" + b.ReleaseBranch
		refs, err := s.remoteRefs(ctx, b.Target, ref)
		if err != nil {
			return err
		}
		actual := refs["refs/heads/"+b.ReleaseBranch]
		if actual == "" || actual != expectedSHA {
			return errors.New("远端再次变化，请重新检查")
		}
		if err = s.fetchMissing(ctx, b.Target, refs, ref); err != nil {
			return err
		}
		ok, err := s.ancestor(ctx, b.Target, b.ReleaseSHA, actual)
		if err != nil {
			return err
		}
		if !ok {
			return errors.New("远端 release 已改写原批次历史，不能自动采纳")
		}
		b.ReleaseSHA, b.TagPlan, b.Phase, b.Message = actual, nil, "READY", "已确认并采纳远端 release 的新增提交"
		return s.save(b)
	})
}

func (s *Service) End(ctx context.Context, id string) (Batch, error) {
	b, err := s.Store.Load(id)
	if err != nil {
		return b, err
	}
	err = s.locks.WithRepository(b.Target.CommonDirectory, func() error {
		var e error
		b, e = s.Store.Load(id)
		if e != nil {
			return e
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		b.Ended, b.Phase, b.Message = true, "ENDED", "批次已结束，分支、Tag 和工作区记录保留"
		b.TagPlan = nil
		return s.save(&b)
	})
	return b, err
}

func (s *Service) ConflictWorkspace(ctx context.Context, id string) (string, string, error) {
	var path, tool string
	_, err := s.withRecord(ctx, id, true, func(b *Batch, _ domain.AppConfig) error {
		if b.Intent == nil || b.Intent.Merge == nil {
			return errors.New("没有待处理的合并工作区")
		}
		m := b.Intent.Merge
		if err := s.validateMergeLocation(b, m); err != nil {
			return err
		}
		root := filepath.Join(s.Paths.Home, "production-worktrees", b.ID)
		rel, err := filepath.Rel(root, m.Worktree)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return errors.New("冲突工作区不属于当前生产批次")
		}
		common, err := s.run(ctx, m.Worktree, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil || filepath.Clean(common) != filepath.Clean(b.Target.CommonDirectory) {
			return errors.New("冲突工作区身份已变化")
		}
		path, tool = m.Worktree, string(b.Target.IDE)
		return nil
	})
	return path, tool, err
}

func (s *Service) advance(ctx context.Context, b *Batch, c domain.AppConfig, recovering bool) error {
	if err := mergeAbortPending(b); err != nil {
		return err
	}
	for b.Intent != nil {
		if _, err := s.validate(ctx, b.Target); err != nil {
			return err
		}
		i := b.Intent
		switch i.Step {
		case "main":
			b.Phase, b.Message = "SYNC_MAIN", "正在将线上生产 Tag 同步到主分支"
			step := 5
			if b.InitialSHA == "" {
				step = 2
			}
			batchProgress(ctx, b, step, "running", b.Message)
			if err := s.ensureMerge(b, b.Target.MainBranch, i.Preview.ProductionSHA, i.Preview.MainSHA, false); err != nil {
				return err
			}
			sha, err := s.mergeAndPush(ctx, b, recovering)
			if err != nil {
				return err
			}
			b.MainSHA, b.ProductionTag, b.ProductionSHA = sha, i.Preview.ProductionTag, i.Preview.ProductionSHA
			b.Intent, b.Phase, b.Message = nil, "READY", "生产 Tag 已合入主分支并确认推送"
			if b.InitialSHA == "" {
				b.Phase = "MAIN_READY"
			}
			state := "complete"
			if sha == i.Preview.MainSHA {
				state, b.Message = "skipped", "主分支已包含生产 Tag，无需合并"
			}
			if sha != i.Preview.MainSHA {
				err = s.savePushed(b)
			} else {
				err = s.save(b)
			}
			if err != nil {
				return err
			}
			batchProgress(ctx, b, step, state, b.Message)
			return nil
		case "release-create":
			b.Phase, b.Message = "CREATE_RELEASE", "正在创建并推送 release 分支"
			batchProgress(ctx, b, 3, "running", b.Message)
			refs := references{}
			if recovering {
				var err error
				refs, err = s.remoteRefs(ctx, b.Target, "refs/heads/"+b.Target.MainBranch, "refs/heads/"+b.ReleaseBranch)
				if err != nil {
					return err
				}
				if refs["refs/heads/"+b.Target.MainBranch] != b.MainSHA {
					return errors.New("主分支已变化，暂停创建 release")
				}
			}
			actual := refs["refs/heads/"+b.ReleaseBranch]
			if actual != "" && actual != b.MainSHA {
				return errors.New("release 已被其他操作创建，已暂停")
			}
			if actual == "" {
				if _, err := s.validate(ctx, b.Target); err != nil {
					return err
				}
				if err := s.pushBranch(ctx, b.Target, b.ReleaseBranch, "", b.MainSHA); err != nil {
					return err
				}
			}
			b.InitialSHA, b.ReleaseSHA = b.MainSHA, b.MainSHA
			b.Intent, b.Phase, b.Message = nil, "READY", "release 已就绪，请逐个合并功能分支"
			if err := s.savePushed(b); err != nil {
				return err
			}
			batchProgress(ctx, b, 3, "complete", "release 分支已创建并确认推送")
			return nil
		case "feature", "release-sync":
			source, before, noFF := i.SourceSHA, i.Preview.ReleaseSHA, true
			b.Phase, b.Message = "MERGE_FEATURE", "正在合并功能分支 "+i.SourceBranch
			step := 4
			if i.Step == "release-sync" {
				step = 5
				source, noFF = b.MainSHA, false
				b.Phase, b.Message = "SYNC_RELEASE", "正在将最新主分支同步到 release"
			}
			batchProgress(ctx, b, step, "running", b.Message)
			if err := s.ensureMerge(b, b.ReleaseBranch, source, before, noFF); err != nil {
				return err
			}
			sha, err := s.mergeAndPush(ctx, b, recovering)
			if err != nil {
				return err
			}
			b.ReleaseSHA = sha
			if i.Step == "feature" {
				if sha != before {
					b.Branches = append(b.Branches, MergedBranch{Branch: i.SourceBranch, SourceSHA: source, Commit: sha, At: timestamp()})
					b.Message = "功能分支已合并并推送"
				} else {
					b.Message = "该分支目标提交已包含，无需重复合并"
				}
				b.Intent, b.Phase = nil, "READY"
				if sha != before {
					err = s.savePushed(b)
				} else {
					err = s.save(b)
				}
				if err != nil {
					return err
				}
				state := "complete"
				if sha == before {
					state = "skipped"
				}
				batchProgress(ctx, b, 4, state, b.Message)
				return nil
			}
			b.Intent, b.Phase, b.Message = nil, "READY", "主分支已合入 release 并确认推送，请重新检查后构建"
			state := "complete"
			if sha == before {
				state, b.Message = "skipped", "release 已包含主分支，无需合并"
			}
			if sha != before {
				err = s.savePushed(b)
			} else {
				err = s.save(b)
			}
			if err != nil {
				return err
			}
			batchProgress(ctx, b, 5, state, b.Message)
			return nil
		case "prepare-tag":
			// Legacy records may already have completed their merge. Resuming
			// this stage only prepares a read-only confirmation, never a Tag.
			b.Intent = nil
			manual := ""
			if i.Preview.Manual {
				manual = i.Preview.ProductionTag
			}
			preview, err := s.observe(ctx, b.Target, c, manual, b.ReleaseBranch, false)
			if err != nil {
				return err
			}
			return s.prepareCheckedTag(ctx, b, preview)
		case "tag-push":
			if recovering {
				return s.resumeTag(ctx, b, c)
			}
			return s.pushTag(ctx, b)
		default:
			return errors.New("生产发布阶段无效，已停止")
		}
	}
	return nil
}
