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

func (s *Service) prepareTag(ctx context.Context, b *Batch, preview Preview) error {
	if len(b.Branches) == 0 || b.ReleaseSHA == b.LastTagCommit() {
		b.TagPlan, b.Intent, b.Phase, b.Message = nil, nil, "READY", "release 没有新增提交，不生成新 Tag；构建失败请到平台重新编译"
		return s.save(b)
	}
	refs, err := s.remoteRefs(ctx, b.Target)
	if err != nil {
		return err
	}
	if refs["refs/heads/"+b.ReleaseBranch] != b.ReleaseSHA || refs["refs/heads/"+b.Target.MainBranch] != b.MainSHA {
		return errors.New("分支在基线同步后发生变化，请重新检查")
	}
	tag, err := NextTag(b.Version, tagNames(refs), len(b.Tags) == 0)
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
		// This is mandatory even when synchronization ran seconds ago. A new
		// production deployment must not slip between preparation and Tag push.
		manual := ""
		if p.Preview.Manual {
			manual = p.Preview.ProductionTag
		}
		fresh, err := s.inspect(ctx, b.Target, c, manual)
		if err != nil {
			return err
		}
		if !sameProductionBaseline(p.Preview, fresh) || fresh.MainSHA != p.MainSHA || fresh.BranchHeads[b.ReleaseBranch] != p.Commit {
			return errors.New("线上生产 Tag 或分支已变化，请重新执行生产基线同步")
		}
		for _, pair := range [][2]string{{fresh.ProductionSHA, p.MainSHA}, {p.MainSHA, p.Commit}} {
			ok, e := s.ancestor(ctx, b.Target, pair[0], pair[1])
			if e != nil {
				return e
			}
			if !ok {
				return errors.New("生产基线尚未完整同步，不能构建 Tag")
			}
		}
		next, err := NextTag(b.Version, fresh.Tags, len(b.Tags) == 0)
		if err != nil {
			return err
		}
		if next != p.Tag {
			return errors.New("Tag 编号已被占用或发生变化，请重新检查并确认")
		}
		if p.Commit == b.LastTagCommit() || len(b.Branches) == 0 {
			return errors.New("release 没有新增提交，不能生成新 Tag")
		}
		object, err := s.makeTag(ctx, c, b, p)
		if err != nil {
			return err
		}
		b.Intent = &Intent{Kind: "tag", Step: "tag-push", Preview: fresh, Tag: p.Tag, TagCommit: p.Commit, TagObject: object}
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
	batchProgress(ctx, b, 6, "running", "校验最终基线并推送生产 Tag "+i.Tag)
	refs, err := s.remoteRefs(ctx, b.Target)
	if err != nil {
		return err
	}
	ref := "refs/tags/" + i.Tag
	if existing := refs[ref]; existing != "" && existing != i.TagObject {
		return errors.New("同名远端 Tag 已存在且不属于本次操作，不能覆盖")
	}
	if refs[ref] == "" {
		// A resumed unconfirmed push must re-run the same freshness gate.
		c, err := s.validate(ctx, b.Target)
		if err != nil {
			return err
		}
		manual := ""
		if i.Preview.Manual {
			manual = i.Preview.ProductionTag
		}
		fresh, err := s.inspect(ctx, b.Target, c, manual)
		if err != nil {
			return err
		}
		if !sameProductionBaseline(i.Preview, fresh) || fresh.MainSHA != b.MainSHA || fresh.BranchHeads[b.ReleaseBranch] != i.TagCommit {
			return errors.New("生产基线或分支已变化，保留本次 Tag 操作，请核对后恢复")
		}
		if _, err = s.run(ctx, b.Target.Repository, "push", "--no-follow-tags", "--recurse-submodules=no", b.Target.Remote, i.TagObject+":"+ref); err != nil {
			return err
		}
	}
	refs, err = s.remoteRefs(ctx, b.Target)
	if err != nil {
		return err
	}
	if refs[ref] != i.TagObject {
		return errors.New("Tag 推送结果尚不能确认，请校验并继续")
	}
	b.Tags = append(b.Tags, Tag{Name: i.Tag, Commit: i.TagCommit, Object: i.TagObject, PushedAt: timestamp()})
	b.TagPlan, b.Intent, b.Phase, b.Message = nil, nil, "READY", "生产 Tag 已推送，正在等待构建结果"
	if err = s.save(b); err != nil {
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
