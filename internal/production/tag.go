package production

import (
	"context"
	"errors"
	"fmt"
	"silverwing-go/internal/commandruntime"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/executable"
	"strings"
	"time"
)

// PrepareTag observes the remote and runtime once for this build click. It
// prepares a token, but never creates or pushes a Tag.
func (s *Service) PrepareTag(ctx context.Context, id string, expected Preview) (Batch, Preview, error) {
	var p Preview
	b, err := s.withBatch(ctx, id, func(b *Batch, c domain.AppConfig) error {
		if expected.Target != b.Target {
			return errors.New("确认的生产发布目标不匹配")
		}
		if err := s.beginTagCheck(b); err != nil {
			return err
		}
		manual := ""
		if expected.Manual {
			manual = expected.ProductionTag
		}
		var err error
		fresh, err := s.observe(ctx, b.Target, c, manual, b.ReleaseBranch, false)
		if err != nil {
			return err
		}
		if expected.Manual && !sameProductionBaseline(expected, fresh) {
			return errors.New("人工确认的生产基线已变化，请重新检查并确认")
		}
		p = fresh
		return s.prepareCheckedTag(ctx, b, p)
	})
	return b, p, err
}

func (s *Service) beginTagCheck(b *Batch) error {
	if b.Intent != nil {
		return errors.New("请先继续当前未完成操作")
	}
	if b.InitialSHA == "" {
		return errors.New("请先创建并核对当前 release")
	}
	// A failed new observation must not leave an older confirmation usable.
	b.TagPlan = nil
	b.Phase, b.Message = "READY", "上次构建确认已失效，请完成本轮检查"
	return s.save(b)
}

func (s *Service) prepareCheckedTag(ctx context.Context, b *Batch, p Preview) error {
	if p.ProductionSHA == "" {
		return errors.New("请先确认生产基线 Tag")
	}
	if p.ReleaseSHA == "" || p.ReleaseSHA != b.ReleaseSHA {
		return errors.New("release 已变化，请先核对并采纳远端状态")
	}
	if !p.ProductionInMain || !p.MainInRelease {
		return errors.New("构建前检查未通过，请分别点击合并生产 Tag 到主分支、合并主分支到 release")
	}
	b.MainSHA, b.ProductionTag, b.ProductionSHA = p.MainSHA, p.ProductionTag, p.ProductionSHA
	if err := s.prepareTag(ctx, b, p); err != nil {
		return err
	}
	batchProgress(ctx, b, 5, "complete", b.Message)
	return nil
}

func (s *Service) prepareTag(ctx context.Context, b *Batch, preview Preview) error {
	// A new numbered Tag may intentionally reuse the release commit. Only a
	// fresh baseline check and explicit confirmation authorize its publication.
	if preview.Target != b.Target || preview.ReleaseSHA != b.ReleaseSHA || preview.MainSHA != b.MainSHA {
		return errors.New("分支在基线同步后发生变化，请重新检查")
	}
	tag, err := NextTag(b.Version, preview.Tags, len(b.Tags) == 0)
	if err != nil {
		return err
	}
	p := &TagPlan{Token: newID(), Tag: tag, Commit: b.ReleaseSHA, MainSHA: b.MainSHA, Preview: preview}
	p.Diff, err = s.run(ctx, b.Target.Repository, "diff", "--stat", b.LastTagCommit(), b.ReleaseSHA, "--")
	if err != nil {
		return err
	}
	p.Commits, err = s.run(ctx, b.Target.Repository, "log", "--oneline", "--max-count=80", b.LastTagCommit()+".."+b.ReleaseSHA)
	if err != nil {
		return err
	}
	if _, err = s.validate(ctx, b.Target); err != nil {
		return err
	}
	b.TagPlan, b.Intent, b.Phase, b.Message = p, nil, "TAG_READY", "构建前检查通过，请确认最终 release 提交和生产 Tag"
	return s.save(b)
}

func (s *Service) Publish(ctx context.Context, id, token string) (Batch, error) {
	return s.withBatch(ctx, id, func(b *Batch, c domain.AppConfig) error {
		p := b.TagPlan
		if b.Intent != nil || p == nil || p.Token != token || token == "" {
			return errors.New("构建确认已失效，请重新检查生产基线")
		}
		if p.Preview.Target != b.Target || p.Commit == "" || p.Commit != b.ReleaseSHA || p.MainSHA != b.MainSHA ||
			p.Preview.ReleaseSHA != p.Commit || p.Preview.MainSHA != p.MainSHA {
			return errors.New("构建目标或提交已变化，请重新检查生产基线")
		}
		object, err := s.makeTag(ctx, c, b, p)
		if err != nil {
			return err
		}
		if _, err = s.validate(ctx, b.Target); err != nil {
			return err
		}
		b.Intent = &Intent{Kind: "tag", Step: "tag-push", Preview: p.Preview, Tag: p.Tag, TagCommit: p.Commit, TagObject: object}
		b.Phase, b.Message = "PUSH_TAG", "正在推送生产 Tag"
		if err = s.save(b); err != nil {
			return err
		}
		return s.pushTag(ctx, b)
	})
}

func (s *Service) makeTag(ctx context.Context, c domain.AppConfig, b *Batch, p *TagPlan) (string, error) {
	identity, err := s.run(ctx, b.Target.Repository, "var", "GIT_COMMITTER_IDENT")
	if err != nil {
		return "", err
	}
	command := ""
	if c.GitExecutablePath != nil {
		command = *c.GitExecutablePath
	}
	spec, err := commandruntime.Prepare(ctx, s.Runner, &c, domain.ProxyTargetGit, executable.ToolGit, command)
	if err != nil {
		return "", err
	}
	spec.Arguments = []string{"-C", b.Target.Repository, "mktag"}
	spec.RemoveEnv = append(spec.RemoveEnv, gitScopeEnvironment...)
	spec.Stdin = []byte(fmt.Sprintf("object %s\ntype commit\ntag %s\ntagger %s\n\nSilverwing production %s\n", p.Commit, p.Tag, identity, b.Version))
	spec.Timeout = 30 * time.Second
	r, err := s.Runner.Run(ctx, spec)
	if err != nil {
		return "", err
	}
	if err = r.RequireSuccess(); err != nil {
		return "", err
	}
	object := strings.TrimSpace(r.Stdout)
	_, err = s.run(ctx, b.Target.Repository, "update-ref", namespace(b.Target)+"/batches/"+b.ID+"/tags/"+p.Tag, object)
	return object, err
}

func (s *Service) pushTag(ctx context.Context, b *Batch) error {
	i := b.Intent
	batchProgress(ctx, b, 6, "running", "正在推送生产 Tag "+i.Tag)
	ref := "refs/tags/" + i.Tag
	if _, err := s.run(ctx, b.Target.Repository, "push", "--no-follow-tags", "--recurse-submodules=no", b.Target.Remote, i.TagObject+":"+ref); err != nil {
		return err
	}
	return s.finishTagPush(ctx, b)
}

// Recovery reads the receipt only when the previous push was not acknowledged.
func (s *Service) resumeTag(ctx context.Context, b *Batch, c domain.AppConfig) error {
	i := b.Intent
	ref := "refs/tags/" + i.Tag
	refs, err := s.remoteRefs(ctx, b.Target, ref)
	if err != nil {
		return err
	}
	if existing := refs[ref]; existing != "" && existing != i.TagObject {
		return errors.New("同名远端 Tag 已存在且不属于本次操作，不能覆盖")
	}
	if refs[ref] == "" {
		manual := ""
		if i.Preview.Manual {
			manual = i.Preview.ProductionTag
		}
		fresh, err := s.observe(ctx, b.Target, c, manual, b.ReleaseBranch, false)
		if err != nil {
			return err
		}
		if !sameProductionBaseline(i.Preview, fresh) || fresh.MainSHA != b.MainSHA || fresh.BranchHeads[b.ReleaseBranch] != i.TagCommit {
			return errors.New("生产基线或分支已变化，保留本次 Tag 操作，请核对后恢复")
		}
		if _, err = s.validate(ctx, b.Target); err != nil {
			return err
		}
		return s.pushTag(ctx, b)
	}
	return s.finishTagPush(ctx, b)
}

func (s *Service) finishTagPush(ctx context.Context, b *Batch) error {
	i := b.Intent
	b.Tags = append(b.Tags, Tag{Name: i.Tag, Commit: i.TagCommit, Object: i.TagObject, PushedAt: timestamp()})
	b.TagPlan, b.Intent, b.Phase, b.Message = nil, nil, "READY", "生产 Tag 已推送，正在等待构建结果"
	if err := s.savePushed(b); err != nil {
		return err
	}
	batchProgress(ctx, b, 6, "complete", b.Message)
	return nil
}

func (s *Service) RefreshBuild(ctx context.Context, id string) (Batch, error) {
	return s.withRecord(ctx, id, true, func(b *Batch, c domain.AppConfig) error {
		if len(b.Tags) == 0 {
			return nil
		}
		// Keep the result detached until its configuration is revalidated. A
		// pending merge may be checkpointed on error, but must not save a late
		// build result together with that checkpoint.
		tags := append([]Tag(nil), b.Tags...)
		for n := range tags {
			// Continue following older pending builds after another Tag is
			// pushed. Failed builds are also refreshable after platform rebuilds.
			if n != len(tags)-1 && tags[n].Status.Build == domain.GenbuSuccess {
				continue
			}
			status, probeErr := s.Genbu.Status(ctx, c, b.Target.GenbuService, tags[n].Name)
			if _, err := s.validate(ctx, b.Target); err != nil {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if probeErr != nil {
				message := commandruntime.Redact(c, probeErr.Error())
				tags[n].Status.FailureReason = &message
			} else {
				tags[n].Status = normalizeStatus(status)
			}
		}
		b.Tags = tags
		return s.save(b)
	})
}
