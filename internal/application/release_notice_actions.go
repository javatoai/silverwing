package application

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"silverwing-go/internal/delivery"
	"silverwing-go/internal/domain"
	"silverwing-go/internal/integrations"
	"silverwing-go/internal/larkcli"
	"silverwing-go/internal/releasenotice"
)

// Only notification-relevant configuration invalidates an open composer.
// This is an opaque digest: credentials and configuration are never in receipts.
func ReleaseNoticeConfigIdentity(c domain.AppConfig) string {
	return releasenotice.Digest([]any{c.TaskRoot, c.TagEnabled, c.Groups, c.Repositories,
		c.LarkExecutablePath, releasenotice.SelfOnly, c.EffectiveLarkNotice().Profile, c.EffectiveLarkNotice().AppID, c.GenbuExecutablePath, c.GitExecutablePath,
		c.MeegleExecutablePath, c.MeegleProjects, c.CommandProxyURL, c.CommandProxyTargets,
		c.CommandProxyUsername, c.CommandProxyPassword, c.CommandProxyNoProxy})
}

type releaseNoticeBatch struct {
	config     domain.AppConfig
	task       domain.TaskManifest
	operations []domain.TagOperation
	rows       []releasenotice.Row
	scope      string
}

// Load the complete persisted batch, never a client-provided or filtered list.
func (a *App) releaseNoticeBatch(ctx context.Context, action Action, refresh bool) (releaseNoticeBatch, error) {
	return a.readReleaseNoticeBatch(ctx, action, refresh, false)
}

func (a *App) readReleaseNoticeBatch(ctx context.Context, action Action, refresh, forceProbe bool) (releaseNoticeBatch, error) {
	var b releaseNoticeBatch
	c, e := a.Config.Load()
	if e != nil {
		return b, e
	}
	b.config = c
	for _, k := range []string{"tagHistoryHome", "tagHistoryTaskRoot", "tagHistoryTaskPath", "tagHistoryTaskCreatedAt", "groupId", "noticeConfigIdentity"} {
		if strings.TrimSpace(textPayload(action, k)) == "" {
			return b, errors.New("通知缺少任务或配置身份，请重新发起通知")
		}
	}
	if textPayload(action, "noticeConfigIdentity") != ReleaseNoticeConfigIdentity(c) {
		return b, errors.New("通知相关配置已变化，请重新发起通知")
	}
	_, tasks, e := a.tagHistoryActionScope(c, action)
	if e != nil {
		return b, e
	}
	b.task = tasks[0]
	if !c.TagEnabled {
		return b, errors.New("测试 Tag 功能已关闭")
	}
	reports, e := a.delivery(c).History(ctx, action.TaskID)
	if e != nil {
		return b, e
	}
	operations := make([]domain.TagOperation, 0, len(reports))
	for _, r := range reports {
		operations = append(operations, r.Operation)
	}
	for _, g := range delivery.GroupTagHistory(operations) {
		if g.GroupID == textPayload(action, "groupId") {
			b.operations = g.Operations
			break
		}
	}
	if len(b.operations) == 0 {
		return b, errors.New("Tag 批次不存在或已被清理")
	}
	sort.SliceStable(b.operations, func(i, j int) bool {
		if b.operations[i].CreatedAt != b.operations[j].CreatedAt {
			return b.operations[i].CreatedAt < b.operations[j].CreatedAt
		}
		return b.operations[i].OperationID < b.operations[j].OperationID
	})
	for _, op := range b.operations {
		service, e := c.GroupService(b.task.GroupID, op.GroupServiceID)
		if e != nil || !service.GenbuProbeEnabled {
			return b, errors.New("批次服务未开启 Genbu 探测，不能确认编译完成")
		}
		if op.State != domain.TagOperationSuccess || strings.TrimSpace(value(op.Tag)) == "" {
			return b, errors.New("本批次的 Tag 尚未全部推送成功")
		}
		if forceProbe {
			if e = a.delivery(c).CheckLiveTagBuild(ctx, action.TaskID, op); e != nil {
				return b, e
			}
		} else if refresh && !releaseNoticeProbeFresh(op.GenbuStatus) {
			if _, e = a.delivery(c).Status(ctx, action.TaskID, op.OperationID); e != nil {
				return b, fmt.Errorf("核对构建状态：%w", e)
			}
		}
	}
	if refresh {
		// Reload after probes/CAS. A concurrently changed or extended batch must
		// be checked in its entirety before any notification can be sent.
		b, e = a.releaseNoticeBatch(ctx, action, false)
		if e != nil {
			return b, e
		}
	}
	b.rows = nil
	for _, op := range b.operations {
		if refresh && !releaseNoticeProbeFresh(op.GenbuStatus) {
			return b, errors.New("构建状态已过期，请刷新后重试")
		}
		if op.GenbuStatus.Build != domain.GenbuSuccess || op.GenbuStatus.NotFound || strings.TrimSpace(value(op.GenbuStatus.FailureReason)) != "" {
			return b, errors.New("本批次的 Genbu 编译尚未全部成功，请刷新构建状态")
		}
		b.rows = append(b.rows, releasenotice.Row{OperationID: op.OperationID, Service: op.ServiceName, Version: value(op.Tag)})
	}
	b.scope = releasenotice.Digest([]any{a.Paths.Home, value(c.TaskRoot), textPayload(action, "tagHistoryTaskPath"),
		b.task.CreatedAt, b.task.RequirementLink, b.task.GroupID, ReleaseNoticeConfigIdentity(c), b.rows})
	return b, ctx.Err()
}

func releaseNoticeProbeFresh(status domain.GenbuTagProbeStatus) bool {
	if status.CheckedAt == nil {
		return false
	}
	t, e := domain.ParseSilverWingTime(*status.CheckedAt)
	age := time.Since(t)
	return e == nil && age >= 0 && age < time.Minute
}

func (a *App) releaseNoticeAction(ctx context.Context, action Action) (ActionResult, error) {
	if action.Kind != "tag.notice.prepare" && action.Kind != "tag.notice.send" {
		return ActionResult{}, errors.New("不支持的通知操作")
	}
	b, e := a.releaseNoticeBatch(ctx, action, true)
	if e != nil {
		return ActionResult{}, e
	}
	store := releasenotice.Store{Directory: filepath.Join(a.Paths.Home, "release-notices")}
	client := larkcli.Client{Runner: a.Runner, Config: b.config}
	if action.Kind == "tag.notice.prepare" {
		identity, e := client.Identity(ctx)
		if e != nil {
			return ActionResult{}, e
		}
		if e = identity.ValidateSelf(); e != nil {
			return ActionResult{}, e
		}
		m, e := a.noticeMetadata(ctx, b.task.RequirementLink)
		if e != nil {
			return ActionResult{}, e
		}
		title := strings.TrimSpace(value(m.Title))
		session := releasenotice.Session{Scope: b.scope, Identity: identity, Title: title, URL: b.task.RequirementLink, Rows: b.rows, Testers: releaseNoticeTesters(m)}
		session.BindOwner(a.noticeOwnerKey(b.config, b.task))
		// Revalidate after asynchronous metadata and identity reads.
		current, e := a.releaseNoticeBatch(ctx, action, false)
		if e != nil {
			return ActionResult{}, e
		}
		if current.scope != b.scope {
			return ActionResult{}, errors.New("批次已变化，请重新发起通知")
		}
		saved, e := store.Prepare(session)
		message := ""
		if m.Stale || m.RefreshError != "" {
			message = "需求标题来自本地缓存"
		}
		return ActionResult{Data: saved, Message: message}, e
	}
	if !boolPayload(action, "confirmed") {
		return ActionResult{}, errors.New("通知缺少显式发送操作")
	}
	choice, e := noticeSelection(action)
	if e != nil {
		return ActionResult{}, e
	}
	session, e := store.Send(ctx, textPayload(action, "sessionId"), choice, client, func(ctx context.Context, session releasenotice.Session) error {
		current, e := a.releaseNoticeBatch(ctx, action, true)
		if e != nil {
			return e
		}
		if current.scope != session.Scope {
			return errors.New("通知批次或任务已变化，请重新发起通知")
		}
		return nil
	})
	return ActionResult{Data: session}, e
}

func noticeSelection(action Action) (releasenotice.Selection, error) {
	var choice releasenotice.Selection
	if action.Payload["selection"] == nil {
		return choice, errors.New("通知缺少发送参数")
	}
	data, e := json.Marshal(action.Payload["selection"])
	if e != nil {
		return choice, e
	}
	if e = domain.DecodeStrict(data, &choice); e != nil {
		return choice, fmt.Errorf("通知发送参数无效：%w", e)
	}
	return choice, nil
}

func (a *App) noticeMetadata(ctx context.Context, link string) (integrations.RequirementMetadataReadResult, error) {
	m, err := a.readNoticeMetadata(ctx, link, false)
	if err == nil && strings.TrimSpace(value(m.Title)) == "" {
		err = errors.New("未取得需求标题，请刷新需求后重试")
	}
	return m, err
}

func (a *App) readNoticeMetadata(ctx context.Context, link string, force bool) (integrations.RequirementMetadataReadResult, error) {
	var m integrations.RequirementMetadataReadResult
	if a.Integrations == nil {
		return m, errors.New("需求元数据服务不可用")
	}
	metadata, e := a.Integrations.Execute(ctx, "requirements.metadata", map[string]any{"url": link, "force": force})
	if e != nil {
		return m, fmt.Errorf("读取需求标题失败：%w", e)
	}
	m, ok := metadata.(integrations.RequirementMetadataReadResult)
	if !ok {
		return m, errors.New("需求元数据响应类型无效")
	}
	return m, nil
}

func (a *App) noticeOwnerKey(c domain.AppConfig, m domain.TaskManifest) string {
	home, root := filepath.Clean(a.Paths.Home), filepath.Clean(value(c.TaskRoot))
	path := filepath.Join(root, m.TaskDirectoryName)
	if runtime.GOOS == "windows" {
		home, root, path = strings.ToLower(home), strings.ToLower(root), strings.ToLower(path)
	}
	return releasenotice.Digest([]string{home, root, path, m.CreatedAt})
}
