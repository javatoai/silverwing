package desktop

import (
	"context"
	"strings"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func TestTagHistoryRetentionConfigChangeRereadsLocalHistory(t *testing.T) {
	s, _, w := tagDesktopTestApp()
	s.settingsUnit.state.configJSON = strings.TrimSuffix(s.settingsUnit.state.configJSON, "}") + `,"tagHistoryMaxGroups":4}`
	s.snapshot.ConfigJSON = s.settingsUnit.state.configJSON
	s.tab = taskTagTab
	b := &tagPollTestBackend{calls: make(chan tagPollTestCall, 2), records: []application.TagRecord{}}
	s.backend = b
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.closeTagGenbuAutoProbe(ctx); err != nil {
			t.Error(err)
		}
		w.drain()
	}()
	s.observeTagGenbuAutoProbeWindow(w)
	w.until(t, func() bool { return s.releaseUnit.state.tagGenbuAutoProbe.current.request == nil })
	old := s.releaseUnit.state.tagGenbuAutoProbe.current
	s.settingsUnit.state.configJSON = strings.Replace(s.settingsUnit.state.configJSON, `"tagHistoryMaxGroups":4`, `"tagHistoryMaxGroups":2`, 1)
	s.snapshot.ConfigJSON = s.settingsUnit.state.configJSON
	s.observeTagGenbuAutoProbeWindow(w)
	w.until(t, func() bool { return s.releaseUnit.state.tagGenbuAutoProbe.current.request == nil })
	b.mu.Lock()
	reads := b.historyReads
	b.mu.Unlock()
	if reads != 2 || s.releaseUnit.state.tagGenbuAutoProbe.current == old || old.ctx.Err() == nil {
		t.Fatalf("new limit did not reread history under a fresh context: reads=%d", reads)
	}
	// Ordinary frames must not keep reading disk once the new limit is applied.
	for i := 0; i < 10; i++ {
		s.observeTagGenbuAutoProbeWindow(w)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.historyReads != 2 {
		t.Fatal("unchanged config triggered repeated local history reads")
	}
}

func TestTagHistoryDeferredRetentionRetriesOnTimer(t *testing.T) {
	s, _, w := tagDesktopTestApp()
	s.settingsUnit.state.configJSON = strings.TrimSuffix(s.settingsUnit.state.configJSON, "}") + `,"tagHistoryMaxGroups":1}`
	s.snapshot.ConfigJSON = s.settingsUnit.state.configJSON
	s.tab = taskTagTab
	status := domain.GenbuTagProbeStatus{Build: domain.GenbuUnknown, UAT: domain.GenbuUnknown, Production: domain.GenbuUnknown}
	old := application.TagRecord{ID: "old", TaskID: "task-a", State: "FAILED", Updated: "2026-10-20 12:00:00", GenbuStatus: status}
	newer := application.TagRecord{ID: "new", TaskID: "task-a", State: "FAILED", Updated: "2026-10-20 13:00:00", GenbuStatus: status}
	b := &tagPollTestBackend{calls: make(chan tagPollTestCall, 2), records: []application.TagRecord{old, newer}}
	s.backend = b
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.closeTagGenbuAutoProbe(ctx); err != nil {
			t.Error(err)
		}
		w.drain()
	}()
	s.observeTagGenbuAutoProbeWindow(w)
	w.until(t, func() bool { return s.releaseUnit.state.tagGenbuAutoProbe.current.request == nil })
	session := s.releaseUnit.state.tagGenbuAutoProbe.current
	if !session.retentionPending || len(s.taskTagHistoryRecords("task-a")) != 2 {
		t.Fatal("a deferred prune discarded running progress or lost its retry")
	}
	view := s.tagJobView(s.tagJobScope())
	view.snapshot.Busy = true
	s.queueTagGenbuAutoProbe(session, w)
	w.drain()
	if session.request != nil {
		t.Fatal("retention retried during an active build")
	}
	view.snapshot.Busy = false
	b.mu.Lock()
	b.records = []application.TagRecord{newer}
	b.mu.Unlock()
	for i := 0; i < 10; i++ {
		s.observeTagGenbuAutoProbeWindow(w)
	}
	if session.request != nil {
		t.Fatal("retention retried on each UI frame")
	}
	s.queueTagGenbuAutoProbe(session, w)
	w.until(t, func() bool {
		b.mu.Lock()
		defer b.mu.Unlock()
		return b.historyReads > 1 && session.request == nil
	})
	if session.retentionPending {
		t.Fatalf("pruned read still pending: records=%+v config=%d error=%q", s.snapshot.TagHistory, s.configInt("tagHistoryMaxGroups", 3), s.releaseUnit.state.tagHistory.Error)
	}
	if records := s.taskTagHistoryRecords("task-a"); len(records) != 1 || records[0].ID != newer.ID {
		t.Fatalf("timer did not replace old history after retention: %+v", records)
	}
}
