package application

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/larkcli"
	"silverwing-go/internal/releasenotice"
)

// Branch messages read task manifests and requirement metadata only. They do
// not need Tag configuration, Git commands, Genbu probes, builds or deployments.
func BranchNoticeConfigIdentity(c domain.AppConfig) string {
	n := c.EffectiveLarkNotice()
	return releasenotice.Digest([]any{c.TaskRoot, c.LarkExecutablePath, n.Profile, n.AppID, releasenotice.SelfOnly,
		c.MeegleExecutablePath, c.MeegleProjects, c.CommandProxyURL, c.CommandProxyTargets,
		c.CommandProxyUsername, c.CommandProxyPassword, c.CommandProxyNoProxy})
}

type branchNoticeScope struct {
	config domain.AppConfig
	task   domain.TaskManifest
	rows   []releasenotice.Row
	scope  string
}

func (a *App) branchNoticeScope(ctx context.Context, action Action) (branchNoticeScope, error) {
	var b branchNoticeScope
	expected, e := taskFilesContext(action)
	if e != nil {
		return b, errors.New("通知缺少任务身份，请重新发起通知")
	}
	c, e := a.Config.LoadForPreview(ctx)
	if e != nil {
		return b, e
	}
	if textPayload(action, "noticeConfigIdentity") != BranchNoticeConfigIdentity(c) {
		return b, errors.New("通知相关配置已变化，请重新发起通知")
	}
	m, path, e := a.taskFilesIdentity(ctx, action, expected)
	if e != nil {
		return b, e
	}
	rows, e := releasenotice.BranchRows(m.Services)
	if e != nil {
		return b, e
	}
	b.config, b.task, b.rows = c, m, rows
	b.scope = releasenotice.Digest([]any{releasenotice.Branches, a.Paths.Home, action.TaskID, path, m.CreatedAt,
		m.LifecycleStatus, m.RequirementLink, BranchNoticeConfigIdentity(c), rows})
	return b, ctx.Err()
}

func (a *App) branchNoticeAction(ctx context.Context, action Action) (ActionResult, error) {
	if action.Kind != "task.branch-notice.prepare" && action.Kind != "task.branch-notice.send" {
		return ActionResult{}, errors.New("不支持的分支通知操作")
	}
	b, e := a.branchNoticeScope(ctx, action)
	if e != nil {
		return ActionResult{}, e
	}
	client := larkcli.Client{Runner: a.Runner, Config: b.config}
	store := releasenotice.Store{Directory: filepath.Join(a.Paths.Home, "release-notices")}
	if action.Kind == "task.branch-notice.prepare" {
		identity, e := client.Identity(ctx)
		if e != nil {
			return ActionResult{}, e
		}
		if e = identity.ValidateSelf(); e != nil {
			return ActionResult{}, e
		}
		metadata, e := a.noticeMetadata(ctx, b.task.RequirementLink)
		if e != nil {
			return ActionResult{}, e
		}
		session := releasenotice.Session{Kind: releasenotice.Branches, Scope: b.scope, Identity: identity, Title: strings.TrimSpace(value(metadata.Title)), URL: b.task.RequirementLink, Rows: b.rows}
		session.BindOwner(a.noticeOwnerKey(b.config, b.task))
		current, e := a.branchNoticeScope(ctx, action)
		if e != nil {
			return ActionResult{}, e
		}
		if current.scope != b.scope {
			return ActionResult{}, errors.New("任务服务或分支已变化，请重新发起通知")
		}
		saved, e := store.Prepare(session)
		message := ""
		if metadata.Stale || metadata.RefreshError != "" {
			message = "需求标题来自本地缓存，请核对"
		}
		return ActionResult{Data: saved, Message: message}, e
	}
	if !boolPayload(action, "confirmed") {
		return ActionResult{}, errors.New("通知缺少显式发送操作")
	}
	selection, e := noticeSelection(action)
	if e != nil {
		return ActionResult{}, e
	}
	session, e := store.Send(ctx, textPayload(action, "sessionId"), selection, client, func(ctx context.Context, s releasenotice.Session) error {
		current, e := a.branchNoticeScope(ctx, action)
		if e != nil {
			return e
		}
		if releasenotice.Digest(s.Rows) != releasenotice.Digest(current.rows) || s.URL != current.task.RequirementLink {
			return errors.New("已确认的分支内容已变化，请重新发起通知")
		}
		if s.Kind != releasenotice.Branches || s.Scope != current.scope {
			return errors.New("任务分支已变化，请重新发起通知")
		}
		return nil
	})
	return ActionResult{Data: session}, e
}
