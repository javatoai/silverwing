package integrations

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"silverwing-go/internal/domain"
	"silverwing-go/internal/larkcli"
	"silverwing-go/internal/noticeprep"
	"silverwing-go/internal/storage"
)

type noticeBeginBarrierContext struct {
	context.Context
	entered chan struct{}
	resume  chan struct{}
	once    sync.Once
}

func (c *noticeBeginBarrierContext) Done() <-chan struct{} {
	c.once.Do(func() {
		close(c.entered)
		<-c.resume
	})
	return c.Context.Done()
}

func newNoticeRefreshTestService(t *testing.T) (*Service, domain.AppConfig, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".meegle"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".meegle", "config.json"), []byte(`{"current":"notice-order-fixture"}`), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewService(storage.NewPaths(t.TempDir()), nil, nil)
	cli := "notice-order-fixture"
	cfg := domain.AppConfig{TagEnabled: true, LarkExecutablePath: &cli, MeegleExecutablePath: &cli,
		LarkNotice:     &domain.LarkNoticeConfig{Profile: "notice-order-fixture"},
		MeegleProjects: []domain.MeegleProjectConfig{{SimpleName: "notice", ProjectKey: "notice-project"}}}
	url := "https://project.feishu.cn/notice/userstory/detail/101"
	return s, cfg, url
}

func TestNoticeRefreshReservationAndDiskFenceKeepSameOrder(t *testing.T) {
	s, cfg, url := newNoticeRefreshTestService(t)
	payload := map[string]any{"url": url, "force": true}
	store := s.noticeStore()
	releaseDisk, err := storage.AcquireLock(filepath.Join(store.Directory, "preparation.lock"))
	if err != nil {
		t.Fatal(err)
	}
	var diskOnce sync.Once
	unlockDisk := func() { diskOnce.Do(releaseDisk) }
	defer unlockDisk()
	ctx := &noticeBeginBarrierContext{Context: context.Background(), entered: make(chan struct{}), resume: make(chan struct{})}
	var resumeOnce sync.Once
	resume := func() { resumeOnce.Do(func() { close(ctx.resume) }) }
	defer resume()
	firstDone, secondDone := make(chan *noticeRefresh, 1), make(chan *noticeRefresh, 1)
	go func() { firstDone <- s.beginNoticeMetadataRefresh(ctx, cfg, payload) }()
	select {
	case <-ctx.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("first refresh did not reach the disk fence barrier")
	}
	unlockDisk()
	// An unserialized reservation lets B finish its disk fence while A is
	// paused after reserving v1. Force exactly that order when it is possible;
	// a serialized reservation must instead release A before B can continue.
	c := s.noticeJobs()
	canOvertake := c.mu.TryLock()
	if canOvertake {
		c.mu.Unlock()
	}
	go func() { secondDone <- s.beginNoticeMetadataRefresh(context.Background(), cfg, payload) }()
	await := func(done <-chan *noticeRefresh) *noticeRefresh {
		t.Helper()
		select {
		case refresh := <-done:
			if refresh == nil || refresh.ticket.Revision == "" {
				t.Fatal("refresh did not establish a disk fence", refresh)
			}
			return refresh
		case <-time.After(2 * time.Second):
			t.Fatal("refresh reservation remained blocked")
			return nil
		}
	}
	var second *noticeRefresh
	if canOvertake {
		second = await(secondDone)
	}
	resume()
	first := await(firstDone)
	if second == nil {
		second = await(secondDone)
	}
	if second.version <= first.version || s.noticeVersion(url) != second.version {
		t.Fatal("second refresh did not reserve the newest local version", first, second)
	}
	if err := store.Check(context.Background(), second.ticket); err != nil {
		t.Fatal("newest local version lost its disk fence to the older refresh", err)
	}
	if err := store.Check(context.Background(), first.ticket); err != noticeprep.ErrSuperseded {
		t.Fatal("older refresh retained the disk fence", err)
	}
}

func TestNoticeListRetiredAfterGenerationCheckCannotReplaceNewFence(t *testing.T) {
	s, cfg, url := newNoticeRefreshTestService(t)
	c := s.noticeJobs()
	c.started = true // No network worker is needed to test list reservation.
	refresh := s.beginNoticeListRefresh(context.Background(), cfg, false)
	store := s.noticeStore()
	releaseDisk, err := storage.AcquireLock(filepath.Join(store.Directory, "preparation.lock"))
	if err != nil {
		t.Fatal(err)
	}
	var diskOnce sync.Once
	unlockDisk := func() { diskOnce.Do(releaseDisk) }
	defer unlockDisk()
	ctx := &noticeBeginBarrierContext{Context: context.Background(), entered: make(chan struct{}), resume: make(chan struct{})}
	var resumeOnce sync.Once
	resume := func() { resumeOnce.Do(func() { close(ctx.resume) }) }
	defer resume()
	done := make(chan struct{})
	go func() {
		s.finishNoticeListRefresh(ctx, cfg, refresh, participatedResult{Items: []domain.ParticipatedWorkItem{{URL: url}}}, nil)
		close(done)
	}()
	select {
	case <-ctx.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("list did not pass its outer generation check")
	}
	unlockDisk()
	s.retireNoticePreparations(false)
	latest := s.beginNoticeMetadataRefresh(context.Background(), cfg, map[string]any{"url": url, "force": true})
	if latest == nil || latest.ticket.Revision == "" {
		t.Fatal("new generation did not establish a fence")
	}
	resume()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("retired list remained blocked")
	}
	if err := store.Check(context.Background(), latest.ticket); err != nil {
		t.Fatal("retired list replaced the new generation's disk fence", err)
	}
	if got := s.noticeVersion(url); got != latest.version {
		t.Fatal("retired list reserved another local version", got, latest.version)
	}
}

func TestNoticeLiveMissCannotReserveAfterLifecycleRetirement(t *testing.T) {
	for _, closeJobs := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "close"}[closeJobs], func(t *testing.T) {
			s, cfg, url := newNoticeRefreshTestService(t)
			p := &TagNoticePreparation{service: s, config: cfg}
			s.retireNoticePreparations(closeJobs)
			base, err := noticeScope(cfg, url)
			if err != nil {
				t.Fatal(err)
			}
			if err := p.loadLive(context.Background(), base, "", larkcli.Client{}, nil); err == nil {
				t.Fatal("retired live miss was accepted")
			}
			if p.ticket.Revision != "" || s.noticeVersion(url) != 0 {
				t.Fatal("retired live miss established a new fence", p.ticket)
			}
		})
	}
}

func TestNoticeQueuedPreparationsKeepNetworkBudgetUntilWorkerStarts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &Service{}
		c := s.noticeJobs()
		defer c.cancel()
		c.started = true
		c.versions["requirement"] = 1
		s.enqueueNoticePreparation(domain.AppConfig{}, noticeRefresh{url: "requirement", version: 1}, nil)
		job := c.queue[0]
		time.Sleep(46 * time.Second)
		if err := job.ctx.Err(); err != nil {
			t.Fatal("queued preparation exhausted its network budget before receiving a worker", err)
		}
		job.cancel()
	})
}

func TestNoticeRetiringLargeQueueSharesOneDiskWaitBudget(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := NewService(storage.NewPaths(t.TempDir()), nil, nil)
		c := s.noticeJobs()
		defer c.cancel()
		c.started = true
		for i := range 24 {
			url := fmt.Sprint(i)
			c.versions[url] = 1
			s.enqueueNoticePreparation(domain.AppConfig{}, noticeRefresh{url: url, version: 1,
				ticket: noticeprep.Ticket{Subject: url, Revision: "blocked-refresh"}}, nil)
		}
		release, err := storage.AcquireLock(filepath.Join(s.noticeStore().Directory, "preparation.lock"))
		if err != nil {
			t.Fatal(err)
		}
		defer release()
		started := time.Now()
		s.retireNoticePreparations(false)
		if elapsed := time.Since(started); elapsed > time.Second {
			t.Fatal("retiring each queued ticket consumed a separate disk-lock budget", elapsed)
		}
		if len(c.queue) != 0 || len(c.active) != 0 || c.generation != 1 {
			t.Fatal("queue was not retired while the disk was unavailable")
		}
	})
}

type noticeWaitingContext struct {
	context.Context
	subscribed chan struct{}
	once       sync.Once
}

func (c *noticeWaitingContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.subscribed) })
	return c.Context.Done()
}

func TestNoticeQueuedReplacementKeepsExistingIdleSubscribers(t *testing.T) {
	s := NewService(storage.NewPaths(t.TempDir()), nil, nil)
	c := s.noticeJobs()
	// Pause worker startup to hold the precise queued/replacement state. A wait
	// subscriber must observe completion of the replacement, not get stranded.
	c.started = true
	c.idle = make(chan struct{})
	c.versions["requirement"] = 2
	oldCtx, oldCancel := context.WithCancel(c.ctx)
	defer oldCancel()
	old := &noticePreparationJob{ctx: oldCtx, cancel: oldCancel, refresh: noticeRefresh{url: "requirement", version: 1}}
	c.queue = []*noticePreparationJob{old}
	c.active["requirement"] = old
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &noticeWaitingContext{Context: base, subscribed: make(chan struct{})}
	done := make(chan error, 1)
	go func() { done <- s.WaitTagNoticePreparations(ctx) }()
	<-ctx.subscribed
	s.enqueueNoticePreparation(domain.AppConfig{}, noticeRefresh{url: "requirement", version: 2}, nil)
	c.mu.Lock()
	replacement := c.queue[0]
	c.queue = nil
	c.running = 1
	c.mu.Unlock()
	replacement.cancel()
	c.mu.Lock()
	c.running = 0
	delete(c.active, "requirement")
	close(c.idle)
	c.mu.Unlock()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("coalescing stranded an existing preparation waiter")
	}
	c.cancel()
}
