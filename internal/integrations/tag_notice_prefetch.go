package integrations

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/larkcli"
	"silverwing-go/internal/noticeprep"
	"silverwing-go/internal/releasenotice"
)

type noticeRefresh struct {
	url                 string
	ticket              noticeprep.Ticket
	local               noticeAccountContext
	generation, version uint64
	force               bool
}

type noticePreparationJob struct {
	refresh  noticeRefresh
	config   domain.AppConfig
	metadata *RequirementMetadataReadResult
	ctx      context.Context
	cancel   context.CancelFunc
}

// Two workers drain the requirements returned by source reads. New work for a
// URL replaces queued work and cancels its older running request. Network work
// is detached only after a successful source read, and owned by this lifecycle.
type noticePreparationJobs struct {
	mu              sync.Mutex
	ctx             context.Context
	cancel          context.CancelFunc
	wake            chan struct{}
	idle            chan struct{}
	queue           []*noticePreparationJob
	active          map[string]*noticePreparationJob
	versions        map[string]uint64
	generation      uint64
	running         int
	started, closed bool
	workers         sync.WaitGroup
}

func (s *Service) noticeJobs() *noticePreparationJobs {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.noticePreparations == nil {
		ctx, cancel := context.WithCancel(context.Background())
		idle := make(chan struct{})
		close(idle)
		s.noticePreparations = &noticePreparationJobs{ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), idle: idle,
			active: map[string]*noticePreparationJob{}, versions: map[string]uint64{}}
	}
	return s.noticePreparations
}

func (s *Service) noticeGeneration() (uint64, error) {
	c := s.noticeJobs()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return c.generation, errors.New("需求通知准备服务正在关闭")
	}
	return c.generation, nil
}

func (s *Service) noticeVersion(url string) uint64 {
	c := s.noticeJobs()
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.versions[url]
}

func (s *Service) beginNoticeRefresh(ctx context.Context, url string, expectedGeneration *uint64) (*noticeRefresh, error) {
	c := s.noticeJobs()
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.closed {
		return nil, errors.New("需求通知准备服务正在关闭")
	}
	if expectedGeneration != nil && c.generation != *expectedGeneration {
		return nil, noticeprep.ErrSuperseded
	}
	c.versions[url]++
	if old := c.active[url]; old != nil {
		old.cancel()
	}
	// Reserve the local version and disk revision in the same order as
	// retirement. Store operations never acquire coordinator/service locks or
	// perform network work; cap OS-lock contention while holding c.mu.
	diskCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	ticket, _ := s.noticeStore().Begin(diskCtx, url, true)
	return &noticeRefresh{url: url, ticket: ticket, generation: c.generation, version: c.versions[url]}, nil
}

func noticePrefetchEnabled(cfg domain.AppConfig) bool {
	// An explicitly configured notification CLI keeps ordinary requirement-only
	// installations (and integrations) from unexpectedly launching another tool.
	return cfg.TagEnabled && cfg.LarkExecutablePath != nil && strings.TrimSpace(*cfg.LarkExecutablePath) != "" && cfg.EffectiveLarkNotice().Profile != ""
}

func (s *Service) beginNoticeMetadataRefresh(ctx context.Context, cfg domain.AppConfig, payload map[string]any) *noticeRefresh {
	if !noticePrefetchEnabled(cfg) || !(boolValue(payload, "force") || boolValue(payload, "fullRefresh")) || ctx.Err() != nil {
		return nil
	}
	base, err := noticeScope(cfg, stringValue(payload, "url", "requirementLink", "link"))
	if err != nil {
		return nil
	}
	if !boolValue(payload, "force") {
		// fullRefresh also accompanies cached page reentry. It verifies a live
		// read when one is needed; it does not itself request replacement data.
		cached, _, hit, _ := s.noticeStore().Load(ctx, base.Requirement)
		if hit && noticeSameContext(cached.Scope, base) {
			return nil
		}
	}
	refresh, err := s.beginNoticeRefresh(ctx, base.Requirement, nil)
	if err != nil {
		return nil
	}
	refresh.local = s.noticeLocalAccount(ctx, cfg)
	return refresh
}

func (s *Service) finishNoticeMetadataRefresh(ctx context.Context, cfg domain.AppConfig, refresh *noticeRefresh, data any, err error) {
	if refresh == nil {
		return
	}
	metadata, ok := data.(RequirementMetadataReadResult)
	if err != nil || ctx.Err() != nil || !ok || metadata.Stale || metadata.RefreshError != "" || !metadata.ReleaseTestersConfirmed || metadata.noticeIdentity.raw == "" {
		state := "failed"
		if ctx.Err() != nil {
			state = "cancelled"
		}
		s.failNoticeTicket(refresh.ticket, state)
		return
	}
	s.enqueueNoticePreparation(cfg, *refresh, &metadata)
}

func (s *Service) beginNoticeListRefresh(ctx context.Context, cfg domain.AppConfig, force bool) *noticeRefresh {
	if !noticePrefetchEnabled(cfg) || ctx.Err() != nil {
		return nil
	}
	if force {
		s.retireNoticePreparations(false)
	}
	generation, err := s.noticeGeneration()
	if err != nil {
		return nil
	}
	return &noticeRefresh{generation: generation, force: force}
}

func (s *Service) finishNoticeListRefresh(ctx context.Context, cfg domain.AppConfig, refresh *noticeRefresh, data any, err error) {
	if refresh == nil || err != nil || ctx.Err() != nil {
		return
	}
	result, ok := data.(participatedResult)
	if !ok {
		return
	}
	generation, genErr := s.noticeGeneration()
	if genErr != nil || generation != refresh.generation {
		return
	}
	seen := map[string]bool{}
	for _, item := range result.Items {
		base, err := noticeScope(cfg, item.URL)
		if err != nil || seen[base.Requirement] {
			continue
		}
		seen[base.Requirement] = true
		if !refresh.force {
			cached, _, hit, _ := s.noticeStore().Load(ctx, base.Requirement)
			if hit && noticeSameContext(cached.Scope, base) {
				continue
			}
		}
		// List projections never establish a complete roster. A bounded worker
		// reads full role metadata; it never treats missing QCOwners as empty.
		r, err := s.beginNoticeRefresh(ctx, base.Requirement, &refresh.generation)
		if err != nil {
			return
		}
		r.local = s.noticeLocalAccount(ctx, cfg)
		s.enqueueNoticePreparation(cfg, *r, nil)
	}
}

func (s *Service) enqueueNoticePreparation(cfg domain.AppConfig, refresh noticeRefresh, metadata *RequirementMetadataReadResult) {
	c := s.noticeJobs()
	c.mu.Lock()
	if c.closed || c.generation != refresh.generation || c.versions[refresh.url] != refresh.version {
		c.mu.Unlock()
		s.failNoticeTicket(refresh.ticket, "cancelled")
		return
	}
	wasIdle := len(c.queue) == 0 && c.running == 0
	for i := 0; i < len(c.queue); i++ {
		if c.queue[i].refresh.url == refresh.url {
			c.queue[i].cancel()
			c.queue = append(c.queue[:i], c.queue[i+1:]...)
			i--
		}
	}
	if wasIdle {
		c.idle = make(chan struct{})
	}
	ctx, cancel := context.WithCancel(c.ctx)
	job := &noticePreparationJob{refresh: refresh, config: cfg, metadata: metadata, ctx: ctx, cancel: cancel}
	c.active[refresh.url] = job
	c.queue = append(c.queue, job)
	if !c.started {
		c.started = true
		for range 2 {
			c.workers.Go(func() { s.runNoticeWorker(c) })
		}
	}
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (s *Service) runNoticeWorker(c *noticePreparationJobs) {
	for {
		c.mu.Lock()
		if len(c.queue) == 0 {
			closed := c.closed
			c.mu.Unlock()
			if closed {
				return
			}
			select {
			case <-c.ctx.Done():
				return
			case <-c.wake:
				continue
			}
		}
		job := c.queue[0]
		c.queue = c.queue[1:]
		c.running++
		more := len(c.queue) > 0
		c.mu.Unlock()
		if more {
			select {
			case c.wake <- struct{}{}:
			default:
			}
		}
		s.runNoticePreparation(job)
		job.cancel()
		c.mu.Lock()
		c.running--
		if c.active[job.refresh.url] == job {
			delete(c.active, job.refresh.url)
		}
		if len(c.queue) == 0 && c.running == 0 {
			close(c.idle)
		}
		c.mu.Unlock()
	}
}

func (s *Service) runNoticePreparation(job *noticePreparationJob) {
	ctx, cancel := context.WithTimeout(job.ctx, 45*time.Second)
	defer cancel()
	cfg, refresh := job.config, job.refresh
	success := false
	defer func() {
		if !success {
			state := "failed"
			if ctx.Err() != nil {
				state = "cancelled"
			}
			s.failNoticeTicket(refresh.ticket, state)
		}
	}()
	if ctx.Err() != nil || s.noticeVersion(refresh.url) != refresh.version {
		return
	}
	base, err := noticeScope(cfg, refresh.url)
	if err != nil {
		return
	}
	metadata := job.metadata
	if metadata == nil {
		value, err := s.readRequirementMetadata(ctx, cfg, map[string]any{"url": refresh.url, "force": true}, nil)
		if err != nil {
			return
		}
		result, ok := value.(RequirementMetadataReadResult)
		if !ok {
			return
		}
		metadata = &result
	}
	if metadata.Stale || metadata.RefreshError != "" || !metadata.ReleaseTestersConfirmed {
		return
	}
	base.MeegleIdentity, base.MeegleContext = noticeAccountID(metadata.noticeIdentity.raw), refresh.local.fingerprint
	if base.MeegleIdentity == "" {
		return
	}
	client := larkcli.Client{Runner: s.runner, Config: cfg, AsUser: true}
	identity, err := client.LocalIdentity(ctx)
	if err != nil || identity.AppID == "" || !releasenotice.ValidOpenID(identity.OpenID) {
		return
	}
	base.AppID, base.Sender = identity.AppID, identity.OpenID
	snapshot, err := prepareNoticeData(ctx, base, *metadata, nil, "", client)
	if err != nil || !snapshot.Valid() {
		return
	}
	proof := &TagNoticePreparation{service: s, config: cfg, local: refresh.local, ticket: refresh.ticket, pending: refresh.ticket.Revision != "",
		generation: refresh.generation, version: refresh.version, Snapshot: snapshot, cacheable: true}
	if err := proof.Check(ctx); err != nil {
		return
	}
	latest, err := client.LocalIdentity(ctx)
	if err != nil || latest.AppID != identity.AppID || latest.OpenID != identity.OpenID {
		return
	}
	if err := proof.Commit(ctx); err != nil {
		return
	}
	success = true
}

func (s *Service) failNoticeTicket(ticket noticeprep.Ticket, state string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = s.noticeStore().Fail(ctx, ticket, state)
}

func (s *Service) retireNoticePreparations(closeJobs bool) {
	c := s.noticeJobs()
	c.mu.Lock()
	c.generation++
	if closeJobs {
		c.closed = true
		c.cancel()
	}
	retired := append([]*noticePreparationJob(nil), c.queue...)
	for _, job := range c.active {
		job.cancel()
	}
	c.queue = nil
	c.active = map[string]*noticePreparationJob{}
	if len(retired) > 0 && c.running == 0 {
		close(c.idle)
	}
	// New reservations must not enter the generation whose epoch is still
	// being retired. Use the same bounded disk-lock wait as Begin.
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	invalidated := s.noticeStore().InvalidateAll(ctx) == nil
	c.mu.Unlock()
	// The epoch already marks every old loading record cancelled. If changing
	// it failed, best-effort failure markers share the same total disk budget.
	if !invalidated {
		for _, job := range retired {
			_ = s.noticeStore().Fail(ctx, job.refresh.ticket, "cancelled")
		}
	}
}

// WaitTagNoticePreparations is optional observability for shutdown, tests and
// read-only probes. It adds no method to the desktop Backend contract.
func (s *Service) WaitTagNoticePreparations(ctx context.Context) error {
	c := s.noticeJobs()
	c.mu.Lock()
	idle := c.idle
	c.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-idle:
		return nil
	}
}

func (s *Service) closeNoticePreparations(ctx context.Context) error {
	s.retireNoticePreparations(true)
	if err := s.WaitTagNoticePreparations(ctx); err != nil {
		return err
	}
	c := s.noticeJobs()
	done := make(chan struct{})
	go func() { c.workers.Wait(); close(done) }()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
		return nil
	}
}
