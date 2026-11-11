package application

import (
	"context"
	"errors"
	"strings"

	"silverwing-go/internal/integrations"
	"silverwing-go/internal/larkcli"
	"silverwing-go/internal/noticeprep"
	"silverwing-go/internal/releasenotice"
)

func (a *App) prepareUserTagNotice(ctx context.Context, b releaseNoticeBatch, client larkcli.Client) (releasenotice.Session, *integrations.TagNoticePreparation, error) {
	prepared, err := a.Integrations.PrepareTagNotice(ctx, b.config, b.task.RequirementLink, b.task.FolderName, client)
	if err != nil {
		return releasenotice.Session{}, nil, err
	}
	recipients, warning := prepared.Snapshot.Recipients, ""
	if reason := prepared.Snapshot.FallbackReason; reason != "" {
		var fallback string
		recipients, fallback = tagRecipientFallback(prepared.Identity, reason)
		if warning != "" {
			warning += "；"
		}
		warning += fallback
	}
	session := releasenotice.Session{Scope: b.scope, Identity: prepared.Identity, Title: prepared.Snapshot.Title, URL: b.task.RequirementLink, Rows: b.rows,
		Testers: prepared.Snapshot.Testers, Recipients: recipients, Warning: warning, RequirementIdentity: prepared.Snapshot.Scope.MeegleIdentity, PreparationStatus: prepared.Warning()}
	session.BindTagOwner(a.noticeOwnerKey(b.config, b.task), b.config.EffectiveLarkNotice().Profile)
	err = session.Validate()
	if err != nil {
		prepared.Discard()
	}
	return session, prepared, err
}

func tagRecipientFallback(identity releasenotice.Identity, reason string) ([]releasenotice.Recipient, string) {
	return []releasenotice.Recipient{{Name: identity.Name, OpenID: identity.OpenID}}, reason + "；本次仅发送给本人，请自行转发给测试负责人"
}

func tagRecipients(ctx context.Context, identity releasenotice.Identity, metadata integrations.RequirementMetadataReadResult, metadataErr error, resolve func(context.Context, string) (releasenotice.Recipient, error)) ([]releasenotice.Recipient, string, error) {
	people, reason, err := resolveTagTesters(ctx, metadata, metadataErr, resolve)
	if err != nil || reason == "" {
		return people, "", err
	}
	people, warning := tagRecipientFallback(identity, reason)
	return people, warning, nil
}

// A reason means the entire roster must fall back to self. Keep resolution
// independent of the sender identity, and retain source order after parallel IO.
func resolveTagTesters(ctx context.Context, metadata integrations.RequirementMetadataReadResult, metadataErr error, resolve func(context.Context, string) (releasenotice.Recipient, error)) ([]releasenotice.Recipient, string, error) {
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if metadataErr != nil || metadata.Stale || metadata.RefreshError != "" {
		return nil, "无法读取最新测试负责人", nil
	}
	people := metadata.Participants.QCOwners
	if metadata.ReleaseTesters != nil {
		people = metadata.ReleaseTesters
	}
	for _, p := range people {
		if strings.TrimSpace(p.Name) == "" {
			return nil, "部分测试负责人缺少可识别姓名", nil
		}
	}
	names := releaseNoticeTesters(metadata)
	if len(names) == 0 {
		return nil, "需求未配置测试负责人", nil
	}
	return noticeprep.Resolve(ctx, names, resolve)
}

func (a *App) validateUserTagNotice(ctx context.Context, action Action, saved releasenotice.Session) (releasenotice.Identity, *integrations.TagNoticePreparation, error) {
	current, err := a.releaseNoticeBatch(ctx, action, true)
	if err != nil {
		return releasenotice.Identity{}, nil, err
	}
	if current.scope != saved.Scope {
		return releasenotice.Identity{}, nil, errors.New("通知批次、任务或配置已变化，请重新准备通知")
	}
	client := larkcli.Client{Runner: a.Runner, Config: current.config, AsUser: true}
	latest, prepared, err := a.prepareUserTagNotice(ctx, current, client)
	if err != nil {
		return releasenotice.Identity{}, nil, err
	}
	if latest.ID != saved.ID || latest.RetryChoice().Snapshot != saved.RetryChoice().Snapshot {
		prepared.Discard()
		return releasenotice.Identity{}, nil, errors.New("通知收件人、需求或账号已变化，请重新准备通知")
	}
	_, err = a.currentUserTagNoticeBatch(ctx, action, saved)
	if err != nil {
		prepared.Discard()
	}
	return latest.Identity, prepared, err
}

func (a *App) currentUserTagNoticeBatch(ctx context.Context, action Action, saved releasenotice.Session) (releaseNoticeBatch, error) {
	current, err := a.releaseNoticeBatch(ctx, action, false)
	if err != nil {
		return current, err
	}
	if current.scope != saved.Scope {
		return current, errors.New("通知查询期间任务或配置已变化，请重新准备通知")
	}
	for _, op := range current.operations {
		if !releaseNoticeProbeFresh(op.GenbuStatus) {
			return current, errors.New("通知查询期间构建状态已过期，请重新准备通知")
		}
	}
	return current, ctx.Err()
}

// This is deliberately local: online login/permission verification is supplied
// by Refresh for this send attempt. Re-read account and scope after async work,
// and immediately before every RPC; local status alone is never verification.
func (a *App) checkUserTagNotice(ctx context.Context, action Action, saved releasenotice.Session, prepared *integrations.TagNoticePreparation) (err error) {
	defer func() {
		if err != nil && prepared != nil {
			prepared.Discard()
		}
	}()
	current, err := a.currentUserTagNoticeBatch(ctx, action, saved)
	if err != nil {
		return err
	}
	client := larkcli.Client{Runner: a.Runner, Config: current.config, AsUser: true}
	identity, err := client.LocalIdentity(ctx)
	if err != nil {
		return err
	}
	if identity.AppID != saved.Identity.AppID || identity.OpenID != saved.Identity.OpenID {
		return errors.New("当前 Lark 账号已变化，请重新准备通知")
	}
	if prepared == nil {
		return errors.New("通知缺少本次准备核对")
	}
	if err := prepared.Commit(ctx); err != nil {
		return err
	}
	_, err = a.currentUserTagNoticeBatch(ctx, action, saved)
	return err
}

func (a *App) userTagNoticeChecks(action Action) releasenotice.TagSendChecks {
	var prepared *integrations.TagNoticePreparation
	return releasenotice.TagSendChecks{
		Refresh: func(ctx context.Context, saved releasenotice.Session) (releasenotice.Identity, error) {
			identity, current, err := a.validateUserTagNotice(ctx, action, saved)
			prepared = current
			return identity, err
		},
		BeforeSend: func(ctx context.Context, saved releasenotice.Session) error {
			return a.checkUserTagNotice(ctx, action, saved, prepared)
		},
	}
}
