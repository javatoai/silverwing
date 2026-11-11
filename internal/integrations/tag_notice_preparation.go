package integrations

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/larkcli"
	"silverwing-go/internal/noticeprep"
	"silverwing-go/internal/releasenotice"
)

// TagNoticePreparation contains permanent recipient data and a request-local
// change fence. Online login authority lives only in Identity, never Snapshot.
type TagNoticePreparation struct {
	Identity releasenotice.Identity
	Snapshot noticeprep.Snapshot
	CacheHit bool

	service    *Service
	config     domain.AppConfig
	local      noticeAccountContext
	ticket     noticeprep.Ticket
	pending    bool
	cacheable  bool
	generation uint64
	version    uint64
	verifiedAt time.Time
}

func (s *Service) noticeStore() noticeprep.Store {
	return noticeprep.Store{Directory: filepath.Join(s.paths.Cache, "tag-notice-preparation")}
}

// Only recipient-relevant values enter this opaque digest. Credentials and
// proxy passwords are never written to a preparation record.
func noticeConfigIdentity(c domain.AppConfig) string {
	return releasenotice.Digest([]any{c.LarkExecutablePath, c.EffectiveLarkNotice(), c.MeegleExecutablePath, c.MeegleProjects,
		c.CommandProxyURL, c.CommandProxyTargets, c.CommandProxyUsername, c.CommandProxyPassword, c.CommandProxyNoProxy})
}

func noticeScope(cfg domain.AppConfig, link string) (noticeprep.Scope, error) {
	space, kind, id, builtin, err := parseRemoteRequirementURL(link)
	if err != nil {
		return noticeprep.Scope{}, err
	}
	project := builtin
	for _, p := range cfg.MeegleProjects {
		if strings.EqualFold(p.SimpleName, space) {
			project = p.ProjectKey
			break
		}
	}
	if project == "" {
		return noticeprep.Scope{}, errors.New("需求项目未配置，不能准备通知")
	}
	return noticeprep.Scope{Config: noticeConfigIdentity(cfg), Requirement: strings.TrimSpace(link), Project: project, Kind: kind, ID: id,
		Profile: cfg.EffectiveLarkNotice().Profile, AppID: cfg.EffectiveLarkNotice().AppID}, nil
}

func noticeSameContext(saved, current noticeprep.Scope) bool {
	return saved.Config == current.Config && saved.Requirement == current.Requirement && saved.Project == current.Project &&
		saved.Kind == current.Kind && saved.ID == current.ID && saved.Profile == current.Profile &&
		(current.AppID == "" || saved.AppID == current.AppID) && (current.Sender == "" || saved.Sender == current.Sender)
}

// PrepareTagNotice is shared by desktop and sw. A matching local credential
// fence permits a disk hit without any Meegle/contacts request. Every call
// still verifies the Lark user online, independently of the stored data.
func (s *Service) PrepareTagNotice(ctx context.Context, cfg domain.AppConfig, link, fallbackTitle string, client larkcli.Client) (*TagNoticePreparation, error) {
	base, err := noticeScope(cfg, link)
	if err != nil {
		return nil, err
	}
	p := &TagNoticePreparation{service: s, config: cfg, local: s.noticeLocalAccount(ctx, cfg)}
	p.generation, err = s.noticeGeneration()
	if err != nil {
		return nil, err
	}
	p.version = s.noticeVersion(base.Requirement)
	queryCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	type verified struct {
		identity releasenotice.Identity
		err      error
		at       time.Time
	}
	online := make(chan verified, 1)
	go func() {
		identity, err := client.Identity(queryCtx)
		if err == nil {
			err = identity.ValidateUser()
		}
		if err != nil {
			cancel()
		}
		online <- verified{identity, err, time.Now()}
	}()
	sourceErr := p.load(queryCtx, base, fallbackTitle, client)
	identity := <-online
	if ctx.Err() != nil {
		p.abandon()
		return nil, ctx.Err()
	}
	if identity.err != nil {
		p.abandon()
		return nil, identity.err
	}
	if sourceErr != nil {
		p.abandon()
		return nil, sourceErr
	}
	p.Identity = identity.identity
	p.verifiedAt = identity.at
	base.AppID, base.Sender = p.Identity.AppID, p.Identity.OpenID
	if p.CacheHit && !noticeSameContext(p.Snapshot.Scope, base) {
		// A different sender cannot inherit directory results prepared under the
		// previous account, even if the profile name happens to be unchanged.
		p.CacheHit = false
		if err := p.loadLive(ctx, base, fallbackTitle, client, nil); err != nil {
			p.abandon()
			return nil, err
		}
	}
	p.Snapshot.Scope.AppID, p.Snapshot.Scope.Sender = p.Identity.AppID, p.Identity.OpenID
	if time.Since(p.verifiedAt) >= 5*time.Second {
		latest, err := client.Identity(ctx)
		if err != nil {
			p.abandon()
			return nil, err
		}
		if err := latest.ValidateUser(); err != nil {
			p.abandon()
			return nil, err
		}
		if latest.AppID != p.Identity.AppID || latest.OpenID != p.Identity.OpenID {
			p.abandon()
			return nil, errors.New("通知查询期间 Lark 账号已变化")
		}
		p.Identity, p.verifiedAt = latest, time.Now()
	}
	if err := p.Check(ctx); err != nil {
		p.abandon()
		return nil, err
	}
	return p, nil
}

func (p *TagNoticePreparation) load(ctx context.Context, base noticeprep.Scope, fallback string, client larkcli.Client) error {
	store := p.service.noticeStore()
	candidate, ticket, hit, _ := store.Load(ctx, base.Requirement)
	if hit && !noticeSameContext(candidate.Scope, base) {
		hit = false
	}
	if hit && candidate.RefreshState == "loading" {
		return noticeprep.ErrRefreshing
	}
	if hit && p.local.bound && candidate.Scope.MeegleContext == p.local.fingerprint {
		p.Snapshot, p.ticket, p.CacheHit = candidate, ticket, true
		p.cacheable = true
		return nil
	}
	if hit && !p.local.bound {
		account, err := p.service.noticeCurrentAccount(ctx, p.config, p.local)
		if err != nil {
			return err
		}
		if account == candidate.Scope.MeegleIdentity {
			p.Snapshot, p.ticket, p.CacheHit = candidate, ticket, true
			p.cacheable = true
			return nil
		}
	}
	if hit {
		return p.loadLive(ctx, base, fallback, client, &candidate)
	}
	return p.loadLive(ctx, base, fallback, client, nil)
}

func (p *TagNoticePreparation) loadLive(ctx context.Context, base noticeprep.Scope, fallback string, client larkcli.Client, candidate *noticeprep.Snapshot) error {
	refresh, err := p.service.beginNoticeRefresh(ctx, base.Requirement, &p.generation)
	if err != nil {
		return err
	}
	p.generation, p.version, p.ticket = refresh.generation, refresh.version, refresh.ticket
	p.pending = p.ticket.Revision != ""
	session, loggedOut, err := p.service.requirementIdentityFor(ctx, p.config, "", true, p.service.requirementCacheIdentity)
	if err != nil {
		return err
	}
	if loggedOut || session.raw == "" {
		return errors.New("无法确认当前 Meegle 账号，不能准备通知")
	}
	base.MeegleIdentity, base.MeegleContext = noticeAccountID(session.raw), p.local.fingerprint
	if candidate != nil && noticeSameContext(candidate.Scope, base) && candidate.Scope.MeegleIdentity == base.MeegleIdentity {
		// Token rotation may change the encrypted local representation while the
		// confirmed account is the same. Rebind the fence without re-querying roles.
		p.Snapshot = *candidate
		p.Snapshot.Scope.MeegleContext = base.MeegleContext
		p.cacheable = true
		return nil
	}
	data, readErr := p.service.readRequirementMetadata(ctx, p.config, map[string]any{"url": base.Requirement, "force": true}, &session)
	metadata, _ := data.(RequirementMetadataReadResult)
	p.cacheable = metadata.Title != nil && strings.TrimSpace(*metadata.Title) != ""
	if ctx.Err() != nil {
		return ctx.Err()
	}
	p.Snapshot, err = prepareNoticeData(ctx, base, metadata, readErr, fallback, client)
	return err
}

func prepareNoticeData(ctx context.Context, scope noticeprep.Scope, metadata RequirementMetadataReadResult, metadataErr error, fallback string, client larkcli.Client) (noticeprep.Snapshot, error) {
	title := ""
	if metadata.Title != nil {
		title = strings.TrimSpace(*metadata.Title)
	}
	snapshot := noticeprep.Snapshot{Scope: scope, FetchedAt: metadata.FetchedAt, Title: title, Testers: []string{}, Recipients: []releasenotice.Recipient{}}
	if snapshot.Title == "" {
		snapshot.Title = fallback
	}
	if metadataErr != nil || metadata.Stale || metadata.RefreshError != "" {
		snapshot.FallbackReason = "无法读取最新测试负责人"
		return snapshot, ctx.Err()
	}
	if !metadata.ReleaseTestersConfirmed || metadata.ReleaseTesters == nil {
		snapshot.FallbackReason = "无法确认完整测试负责人名单"
		return snapshot, ctx.Err()
	}
	snapshot.RosterComplete = true
	names := []string{}
	for _, person := range metadata.ReleaseTesters {
		if strings.TrimSpace(person.Name) == "" {
			snapshot.RosterComplete = false
			snapshot.FallbackReason = "部分测试负责人缺少可识别姓名"
			return snapshot, ctx.Err()
		}
		names = append(names, person.Name)
	}
	snapshot.Testers = noticeprep.Names(names)
	var err error
	snapshot.Recipients, snapshot.FallbackReason, err = noticeprep.Resolve(ctx, snapshot.Testers, client.ResolveTester)
	return snapshot, err
}

// Check is local when the credential store is observable. On other platforms
// a current user read supplies the account fence. Neither path grants send
// authority; Store.SendTag still requires its short online Lark authorization.
func (p *TagNoticePreparation) Check(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	generation, err := p.service.noticeGeneration()
	if err != nil {
		return err
	}
	if generation != p.generation {
		return noticeprep.ErrSuperseded
	}
	if p.service.noticeVersion(p.Snapshot.Scope.Requirement) != p.version {
		return noticeprep.ErrSuperseded
	}
	current, err := p.service.config.Load()
	if err != nil {
		return err
	}
	if noticeConfigIdentity(current) != noticeConfigIdentity(p.config) {
		return noticeprep.ErrSuperseded
	}
	local := p.service.noticeLocalAccount(ctx, current)
	if local.fingerprint != p.local.fingerprint {
		return errors.New("Meegle 本地账号或配置已变化，请重新准备通知")
	}
	if !local.bound {
		account, err := p.service.noticeCurrentAccount(ctx, current, local)
		if err != nil {
			return err
		}
		if account != p.Snapshot.Scope.MeegleIdentity {
			return errors.New("Meegle 账号已变化，请重新准备通知")
		}
	}
	return p.service.noticeStore().Check(ctx, p.ticket)
}

// Commit follows the caller's final Lark/task check. A disk failure does not
// prevent a verified live send; a superseded request always does.
func (p *TagNoticePreparation) Commit(ctx context.Context) error {
	// Data does not expire. This timestamp is solely the online authorization
	// acquired by this request and is never serialized or reused by prefetch.
	if p.Identity.UserVerified && (time.Since(p.verifiedAt) < 0 || time.Since(p.verifiedAt) >= 5*time.Second) {
		p.abandon()
		return errors.New("发送前在线验证已过期，请重新发起通知")
	}
	if err := p.Check(ctx); err != nil {
		p.abandon()
		return err
	}
	if !p.pending {
		return nil
	}
	store := p.service.noticeStore()
	var err error
	if p.cacheable && p.Snapshot.Valid() {
		err = store.Commit(ctx, p.ticket, p.Snapshot)
	} else {
		err = store.Fail(ctx, p.ticket)
	}
	if errors.Is(err, noticeprep.ErrSuperseded) || ctx.Err() != nil {
		return errors.Join(err, ctx.Err())
	}
	p.pending = false
	if err != nil {
		p.ticket = noticeprep.Ticket{}
	}
	return nil
}

func (p *TagNoticePreparation) abandon() {
	if p.pending {
		p.service.failNoticeTicket(p.ticket, "cancelled")
		p.pending = false
	}
}

func (p *TagNoticePreparation) Discard() { p.abandon() }

func (p *TagNoticePreparation) Warning() string {
	switch p.Snapshot.RefreshState {
	case "failed":
		return "需求通知资料刷新失败，沿用上次成功的本地缓存；可点击刷新需求重试"
	case "cancelled":
		return "需求通知资料刷新已取消，沿用上次成功的本地缓存"
	default:
		return ""
	}
}
