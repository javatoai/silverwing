package desktop

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

type tagPollTestCall struct {
	action  application.Action
	ctx     context.Context
	release chan struct{}
}
type tagPollTestBackend struct {
	mu                 sync.Mutex
	records            []application.TagRecord
	calls              chan tagPollTestCall
	historyReads       int
	historyRelease     chan struct{}
	historyEmitted     chan struct{}
	historyEmittedOnce sync.Once
}

type tagDesktopAndProbeTestBackend struct {
	*tagDesktopTestBackend
	probe *tagPollTestBackend
}

func (b *tagDesktopAndProbeTestBackend) DispatchTagProbe(ctx context.Context, a application.Action, emit func(application.TagRecord)) (application.ActionResult, error) {
	return b.probe.DispatchTagProbe(ctx, a, emit)
}

func (*tagPollTestBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, errors.New("unexpected full snapshot")
}
func (*tagPollTestBackend) Dispatch(context.Context, application.Action) (application.ActionResult, error) {
	return application.ActionResult{}, errors.New("typed probe was bypassed")
}
func (b *tagPollTestBackend) DispatchTagProbe(ctx context.Context, action application.Action, emit func(application.TagRecord)) (application.ActionResult, error) {
	b.mu.Lock()
	records := append([]application.TagRecord(nil), b.records...)
	if action.Kind == "tag.history" {
		b.historyReads++
	}
	b.mu.Unlock()
	if action.Kind == "tag.history" {
		for _, record := range records {
			emit(record)
		}
		if b.historyEmitted != nil {
			b.historyEmittedOnce.Do(func() { close(b.historyEmitted) })
		}
		if b.historyRelease != nil {
			select {
			case <-b.historyRelease:
			case <-ctx.Done():
				return application.ActionResult{}, ctx.Err()
			}
		}
		return application.ActionResult{Data: records}, nil
	}
	if action.Kind != "tag.status" {
		return application.ActionResult{}, errors.New("unexpected probe kind")
	}
	call := tagPollTestCall{action: action, ctx: ctx, release: make(chan struct{})}
	b.calls <- call
	select {
	case <-call.release:
	case <-ctx.Done():
		return application.ActionResult{}, ctx.Err()
	}
	for _, record := range records {
		if record.ID != payloadString(action.Payload, "operationId") {
			continue
		}
		checked := "2026-10-15T10:01:00Z"
		record.GenbuStatus = domain.GenbuTagProbeStatus{Build: domain.GenbuBuilding, UAT: domain.GenbuInitial, Production: domain.GenbuUnknown, CheckedAt: &checked}
		emit(record)
		return application.ActionResult{Data: record}, nil
	}
	return application.ActionResult{}, errors.New("unknown fixture operation")
}

func TestTagGenbuPollingCadence(t *testing.T) {
	now := time.Date(2026, 10, 15, 10, 1, 0, 0, time.UTC)
	base := application.TagRecord{Tag: "test-tag", State: "SUCCESS", CreatedAt: now.Add(-time.Minute).Format(time.RFC3339Nano)}
	cases := []struct {
		name   string
		change func(*application.TagRecord)
		want   time.Duration
	}{
		{"new-unknown", func(*application.TagRecord) {}, 5 * time.Second},
		{"active-build", func(r *application.TagRecord) { r.GenbuStatus.Build = domain.GenbuBuilding }, 5 * time.Second},
		{"active-uat", func(r *application.TagRecord) {
			r.GenbuStatus.Build = domain.GenbuSuccess
			r.GenbuStatus.UAT = domain.GenbuBuilding
		}, 5 * time.Second},
		{"new-not-found", func(r *application.TagRecord) { r.GenbuStatus.NotFound = true }, 5 * time.Second},
		{"old-not-found", func(r *application.TagRecord) {
			r.CreatedAt = now.Add(-2 * time.Minute).Format(time.RFC3339Nano)
			r.GenbuStatus.NotFound = true
		}, 30 * time.Second},
		{"recent-push-after-long-merge", func(r *application.TagRecord) {
			r.CreatedAt = now.Add(-time.Hour).Format(time.RFC3339)
			r.Updated = domain.FormatSilverWingTime(now.Add(-time.Minute))
			r.GenbuStatus.NotFound = true
		}, 5 * time.Second},
		{"idle", func(r *application.TagRecord) {
			r.GenbuStatus.Build = domain.GenbuSuccess
			r.GenbuStatus.UAT = domain.GenbuUnknown
		}, 30 * time.Second},
		{"uat-done", func(r *application.TagRecord) { r.GenbuStatus.UAT = domain.GenbuSuccess }, 0},
		{"build-failed", func(r *application.TagRecord) { r.GenbuStatus.Build = domain.GenbuFailed }, 0},
		{"superseded", func(r *application.TagRecord) { r.GenbuStatus.StoppedByNewerRelease = true }, 0},
		{"local-not-pushed", func(r *application.TagRecord) { r.State = "CREATED" }, 0},
		{"no-tag", func(r *application.TagRecord) { r.Tag = "" }, 0},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			record := base
			test.change(&record)
			if got := tagGenbuProbeInterval(record, now); got != test.want {
				t.Fatalf("cadence=%v want=%v", got, test.want)
			}
		})
	}
}

func TestTagGenbuPollingPersistedTimeZone(t *testing.T) {
	originalLocal := time.Local
	defer func() { time.Local = originalLocal }()
	// No t.Parallel: changing Local is scoped to this sequential test, and all
	// persisted fixtures are produced by the actual Shanghai formatter.
	now := time.Date(2026, 10, 15, 10, 0, 0, 0, time.UTC)
	for _, location := range []*time.Location{time.UTC, time.FixedZone("UTC-08", -8*60*60), time.FixedZone("UTC+12", 12*60*60)} {
		t.Run(location.String(), func(t *testing.T) {
			time.Local = location
			for _, age := range []time.Duration{time.Minute, 3 * time.Minute} {
				for _, field := range []string{"updated", "created-fallback"} {
					record := application.TagRecord{Tag: "fixture-tag", State: "SUCCESS", CreatedAt: domain.FormatSilverWingTime(now.Add(-time.Hour)), GenbuStatus: domain.GenbuTagProbeStatus{NotFound: true}}
					if field == "updated" {
						record.Updated = domain.FormatSilverWingTime(now.Add(-age))
					} else {
						record.Updated = "invalid"
						record.CreatedAt = domain.FormatSilverWingTime(now.Add(-age))
					}
					want := 5 * time.Second
					if age > 2*time.Minute {
						want = 30 * time.Second
					}
					if got := tagGenbuProbeInterval(record, now); got != want {
						t.Fatalf("%s age %s cadence=%s want=%s", field, age, got, want)
					}
				}
			}
		})
	}
}

func TestTagGenbuPollingSlowRepositoryAndCAS(t *testing.T) {
	s, _, w := tagDesktopTestApp()
	now := time.Date(2026, 10, 15, 10, 1, 0, 0, time.UTC)
	records := []application.TagRecord{}
	for _, id := range []string{"slow", "fast"} {
		records = append(records, application.TagRecord{ID: id, TaskID: "task-a", ServiceID: id, ModuleID: "module", Tag: "tag-" + id, State: "SUCCESS", Updated: "version-1", CreatedAt: now.Add(-time.Minute).Format(time.RFC3339Nano), GenbuStatus: domain.GenbuTagProbeStatus{Build: domain.GenbuUnknown, UAT: domain.GenbuUnknown, Production: domain.GenbuUnknown}})
	}
	b := &tagPollTestBackend{records: records, calls: make(chan tagPollTestCall, 16)}
	s.backend = b
	s.tab = taskTagTab
	s.snapshot.TagHistory = append([]application.TagRecord(nil), records...)
	s.ensureTagGenbuAutoProbeState().now = func() time.Time { return now }
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.closeTagGenbuAutoProbe(ctx); err != nil {
			t.Error(err)
		}
		w.drain()
	}()
	s.observeTagGenbuAutoProbeWindow(w)
	calls := []tagPollTestCall{}
	w.until(t, func() bool {
		for {
			select {
			case call := <-b.calls:
				calls = append(calls, call)
			default:
				return len(calls) == 2
			}
		}
	})
	var slow, fast tagPollTestCall
	for _, call := range calls {
		if call.action.ServiceID == "slow" {
			slow = call
		} else {
			fast = call
		}
	}
	if slow.action.Kind != "tag.status" || fast.action.Kind != "tag.status" {
		t.Fatal("polling is not per record")
	}
	close(fast.release)
	session := s.tagGenbuAutoProbe.current
	w.until(t, func() bool { return session.probes["fast:module"] == nil })
	if s.snapshot.TagHistory[1].GenbuStatus.Build != domain.GenbuBuilding || session.probes["slow:module"] == nil {
		t.Fatalf("fast result not applied while slow in flight: error=%q action=%+v records=%+v lanes=%v", s.tagGenbuError("fast"), fast.action, s.snapshot.TagHistory, session.probes)
	}
	now = now.Add(4 * time.Second)
	s.queueTagGenbuAutoProbe(session, w)
	w.drain()
	select {
	case <-b.calls:
		t.Fatal("probe ran before 5 seconds")
	default:
	}
	now = now.Add(time.Second)
	s.queueTagGenbuAutoProbe(session, w)
	var again tagPollTestCall
	w.until(t, func() bool {
		select {
		case again = <-b.calls:
			return true
		default:
			return false
		}
	})
	if again.action.ServiceID != "fast" {
		t.Fatal("slow repository overlapped or blocked fast poll")
	}
	// The slow reply belongs to the old Tag version and may update no fields.
	s.snapshot.TagHistory[0].Tag = "replacement-tag"
	oldSlow := session.probes["slow:module"]
	close(slow.release)
	w.until(t, func() bool { return session.probes["slow:module"] != oldSlow })
	if s.snapshot.TagHistory[0].GenbuStatus.Build != domain.GenbuUnknown {
		t.Fatal("stale Tag query overwrote replacement")
	}
	s.selected = 1
	s.observeTagGenbuAutoProbeWindow(w)
	if again.ctx.Err() == nil {
		t.Fatal("task switch retained old probe subscription")
	}
	if len(s.taskTagHistoryRecords("task-b")) != 0 {
		t.Fatal("old probe contaminated task-b")
	}
}

func TestTagGenbuPollingIdleVirtualClock(t *testing.T) {
	s, _, w := tagDesktopTestApp()
	now := time.Date(2026, 10, 15, 10, 5, 0, 0, time.UTC)
	record := application.TagRecord{ID: "idle", TaskID: "task-a", ServiceID: "idle", ModuleID: "module", Tag: "tag-idle", State: "SUCCESS", CreatedAt: now.Add(-3 * time.Minute).Format(time.RFC3339), GenbuStatus: domain.GenbuTagProbeStatus{NotFound: true}}
	b := &tagPollTestBackend{records: []application.TagRecord{record}, calls: make(chan tagPollTestCall, 8)}
	s.backend = b
	s.tab = taskTagTab
	s.snapshot.TagHistory = []application.TagRecord{record}
	state := s.ensureTagGenbuAutoProbeState()
	state.now = func() time.Time { return now }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := &tagGenbuAutoProbeSession{identity: s.taskTagGenbuIdentity(), ctx: ctx, cancel: cancel, probes: map[string]*tagGenbuAutoProbeRequest{}, lastProbe: map[string]time.Time{tagGenbuProbeRecordKey(record): now}, forceRecords: map[string]bool{}}
	state.current = session
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), time.Second)
		defer stop()
		if err := s.closeTagGenbuAutoProbe(cleanup); err != nil {
			t.Error(err)
		}
		w.drain()
	}()
	now = now.Add(29 * time.Second)
	s.startDueTagGenbuProbes(session, w)
	if len(session.probes) != 0 {
		t.Fatal("idle poll ran before 30 seconds")
	}
	now = now.Add(time.Second)
	s.startDueTagGenbuProbes(session, w)
	if len(session.probes) != 1 {
		t.Fatal("unrecognised old Tag stopped instead of slowing")
	}
	// Repeated frame/timer/manual scheduling never duplicates an in-flight lane.
	for i := 0; i < 20; i++ {
		s.startDueTagGenbuProbes(session, w)
	}
	if len(session.probes) != 1 {
		t.Fatal("overlapping idle requests")
	}
}

func TestTagGenbuPollingRestoreBeforeLocalHistory(t *testing.T) {
	s, jobs, w := tagDesktopTestApp()
	s.tab = taskTagTab
	scope := s.tagJobScope()
	snapshot := tagDesktopTestSnapshot(scope)
	jobs.snapshots[scope] = snapshot
	local := *snapshot.Items[0].Record
	local.GenbuStatus = domain.GenbuTagProbeStatus{Build: domain.GenbuUnknown, UAT: domain.GenbuUnknown, Production: domain.GenbuUnknown}
	probe := &tagPollTestBackend{records: []application.TagRecord{local}, calls: make(chan tagPollTestCall, 4)}
	s.backend = &tagDesktopAndProbeTestBackend{jobs, probe}
	defer tagDesktopClose(t, s, w)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.closeTagGenbuAutoProbe(ctx); err != nil {
			t.Error(err)
		}
		w.drain()
	}()
	s.observeTagJobsWindow(w)
	s.observeTagGenbuAutoProbeWindow(w)
	probe.mu.Lock()
	reads := probe.historyReads
	probe.mu.Unlock()
	if reads != 0 || s.tagGenbuAutoProbe != nil && s.tagGenbuAutoProbe.current != nil {
		t.Fatal("local history started before job restoration")
	}
	w.until(t, func() bool { return s.tagJobs.current.loaded })
	s.observeTagGenbuAutoProbeWindow(w)
	w.until(t, func() bool { return s.tagGenbuAutoProbe.current.request == nil })
	if len(s.taskTagHistoryRecords("task-a")) != 2 || s.tagBatchProgress("batch-a").Running != 2 {
		t.Fatal("partial persisted history erased a queued item")
	}
}

func TestTagGenbuPollingManualRefreshCoalesces(t *testing.T) {
	s, _, w := tagDesktopTestApp()
	s.tab = taskTagTab
	s.requirementIdentityJobs = &requirementIdentityJobState{confirmed: true, identity: "synthetic-account"}
	record := application.TagRecord{ID: "finished", TaskID: "task-a", ServiceID: "service", ModuleID: "module", Tag: "tag-finished", State: "SUCCESS", GenbuStatus: domain.GenbuTagProbeStatus{Build: domain.GenbuFailed, UAT: domain.GenbuUnknown, Production: domain.GenbuUnknown}}
	backend := &tagPollTestBackend{records: []application.TagRecord{record}, calls: make(chan tagPollTestCall, 4)}
	s.backend = backend
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.closeTagGenbuAutoProbe(ctx); err != nil {
			t.Error(err)
		}
		w.drain()
	}()
	s.observeTagGenbuAutoProbeWindow(w)
	w.until(t, func() bool { return s.tagGenbuAutoProbe.current.request == nil })
	select {
	case <-backend.calls:
		t.Fatal("terminal state auto-probed")
	default:
	}
	for i := 0; i < 20; i++ {
		s.requestTagHistoryRefresh()
	}
	s.observeTagGenbuAutoProbeWindow(w)
	var call tagPollTestCall
	w.until(t, func() bool {
		select {
		case call = <-backend.calls:
			return true
		default:
			return false
		}
	})
	if call.action.Payload["force"] != true || call.action.TaskID != "task-a" || call.action.Payload["tagJobAccountIdentity"] != "synthetic-account" {
		t.Fatal("manual refresh lost its forced scoped query")
	}
	backend.mu.Lock()
	reads := backend.historyReads
	backend.mu.Unlock()
	if reads != 2 {
		t.Fatalf("20 clicks created %d local refreshes", reads-1)
	}
	for i := 0; i < 20; i++ {
		s.queueTagGenbuAutoProbe(s.tagGenbuAutoProbe.current, w)
	}
	w.drain()
	select {
	case <-backend.calls:
		t.Fatal("manual request overlapped an existing repository query")
	default:
	}
	s.requestTagHistoryRefresh()
	s.selected = 1
	s.observeTagGenbuAutoProbeWindow(w)
	if s.tagGenbuAutoProbe.forcePending {
		t.Fatal("old task's pending forced refresh leaked to the next task")
	}
}

func TestTagGenbuPollingLocalReadCannotEraseFinishedJob(t *testing.T) {
	s, jobs, w := tagDesktopTestApp()
	s.tab = taskTagTab
	scope := s.tagJobScope()
	snapshot := tagDesktopTestSnapshot(scope)
	jobs.snapshots[scope] = snapshot
	local := *snapshot.Items[0].Record
	local.GenbuStatus = domain.GenbuTagProbeStatus{Build: domain.GenbuUnknown, UAT: domain.GenbuUnknown, Production: domain.GenbuUnknown}
	probe := &tagPollTestBackend{records: []application.TagRecord{local}, calls: make(chan tagPollTestCall, 8), historyRelease: make(chan struct{}), historyEmitted: make(chan struct{})}
	s.backend = &tagDesktopAndProbeTestBackend{jobs, probe}
	defer tagDesktopClose(t, s, w)
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.closeTagGenbuAutoProbe(ctx); err != nil {
			t.Error(err)
		}
		w.drain()
	}()
	s.observeTagJobsWindow(w)
	w.until(t, func() bool { return s.tagJobs.current.loaded })
	s.observeTagGenbuAutoProbeWindow(w)
	w.until(t, func() bool { probe.mu.Lock(); defer probe.mu.Unlock(); return probe.historyReads == 1 })
	final := tagDesktopTestSnapshot(scope)
	final.Sequence = 2
	final.Busy = false
	for i := range final.Items {
		final.Items[i].Phase = application.TagJobPhaseCompleted
		final.Items[i].Queued = false
		final.Items[i].Done = true
		final.Items[i].Record.State = "SUCCESS"
		final.Items[i].Record.Tag = "finished-" + final.Items[i].ID
	}
	s.acceptTagJobSnapshot(s.currentTagJobView(), scope, final, false)
	w.until(t, func() bool {
		select {
		case <-probe.historyEmitted:
			return true
		default:
			return false
		}
	})
	if s.snapshot.TagHistory[0].State != "SUCCESS" {
		t.Fatal("late local history callback regressed a completed phase")
	}
	// The first read is still buffered with old data. A completion-triggered
	// follow-up read must see the final records that the real backend persisted.
	probe.mu.Lock()
	probe.records = nil
	for _, item := range final.Items {
		probe.records = append(probe.records, *item.Record)
	}
	probe.mu.Unlock()
	close(probe.historyRelease)
	w.until(t, func() bool { return s.tagGenbuAutoProbe.current.request == nil })
	if len(s.snapshot.TagHistory) != 2 || s.snapshot.TagHistory[0].State != "SUCCESS" || s.snapshot.TagHistory[1].Tag != "finished-two" {
		t.Fatalf("late local history erased a finished job: records=%+v readSequence=%d view=%+v", s.snapshot.TagHistory, s.tagGenbuAutoProbe.current.generation, s.currentTagJobView().snapshot)
	}
}
