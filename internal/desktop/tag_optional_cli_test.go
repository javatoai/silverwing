package desktop

import (
	"context"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/domain"
)

func TestTagHistoryHidesGenbuControlsButKeepsGitHistory(t *testing.T) {
	s := tagLayoutFixture(t)
	setTestPlatformSwitch(s, "genbu", false)
	s.snapshot.TagHistory[0].RetryKind = "RETRY_BUILD"
	s.snapshot.TagHistory[2].RetryKind = "RETAG"
	*s.input(tagGenbuTaskErrorKey("task-a"), "") = "fixture Genbu failure"
	tester := tagLayoutTester(s, 900, false)
	for _, hidden := range []string{"Genbu", "构建 进行中", "UAT 未开始", "构建 成功", "构建 失败", "构建日志", "重新打Tag", "Genbu 刷新失败"} {
		if tester.HasText(hidden) {
			t.Fatalf("Genbu-only control or state remains visible while disabled: %s", hidden)
		}
	}
	if !tester.HasText("重试构建Tag") || !tester.HasText("3.11.82.beta-1") {
		t.Fatal("ordinary Git retry or Tag history changed")
	}
}

func TestTagHistoryDisabledGenbuReadsLocalHistoryWithoutPolling(t *testing.T) {
	s, _, w := tagDesktopTestApp()
	setTestPlatformSwitch(s, "genbu", false)
	s.tab = taskTagTab
	record := application.TagRecord{ID: "local", TaskID: "task-a", ServiceID: "service", ModuleID: "module", Tag: "fixture-tag", State: "SUCCESS", GenbuStatus: domain.GenbuTagProbeStatus{Build: domain.GenbuUnknown, UAT: domain.GenbuUnknown}}
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
	w.until(t, func() bool {
		backend.mu.Lock()
		defer backend.mu.Unlock()
		return backend.historyReads == 1 && s.releaseUnit.state.tagGenbuAutoProbe.current.request == nil
	})
	session := s.releaseUnit.state.tagGenbuAutoProbe.current
	if session.polling || len(s.taskTagHistoryRecords("task-a")) != 1 {
		t.Fatal("disabled Genbu did not retain local history with polling stopped")
	}
	s.queueTagGenbuAutoProbe(session, w)
	w.drain()
	select {
	case call := <-backend.calls:
		t.Fatalf("disabled Genbu issued remote status request: %+v", call.action)
	default:
	}

	s.requestTagHistoryRefresh()
	s.observeTagGenbuAutoProbeWindow(w)
	w.until(t, func() bool {
		backend.mu.Lock()
		defer backend.mu.Unlock()
		return backend.historyReads == 2 && s.releaseUnit.state.tagGenbuAutoProbe.current.request == nil
	})
	select {
	case call := <-backend.calls:
		t.Fatalf("manual history refresh issued remote status request: %+v", call.action)
	default:
	}
}

func TestTagRetryGateOnlyBlocksRetagWhenGenbuIsDisabled(t *testing.T) {
	s, _, _ := tagDesktopTestApp()
	setTestPlatformSwitch(s, "genbu", false)
	s.snapshot.TagHistory = []application.TagRecord{
		{ID: "retag", TaskID: "task-a", RetryKind: "RETAG"},
		{ID: "retry", TaskID: "task-a", RetryKind: "RETRY_BUILD"},
	}
	if !s.tagRetryRequiresGenbu(application.Action{Kind: "tag.retry", TaskID: "task-a", Payload: map[string]any{"operationId": "retag"}}) {
		t.Fatal("Genbu-dependent retag was not gated")
	}
	if s.tagRetryRequiresGenbu(application.Action{Kind: "tag.retry", TaskID: "task-a", Payload: map[string]any{"operationId": "retry"}}) {
		t.Fatal("ordinary Git Tag retry was gated")
	}
}

func TestQueuedTagRetryRechecksGenbuBeforeStarting(t *testing.T) {
	s, backend, window := tagDesktopTestApp()
	defer tagDesktopClose(t, s, window)
	retag := application.TagRecord{ID: "retag", TaskID: "task-a", ServiceID: "service", ModuleID: "module", RetryKind: "RETAG"}
	s.snapshot.TagHistory = []application.TagRecord{retag}
	scope := s.tagJobScope()
	view := s.tagJobView(scope)
	view.commandRunning = true
	action := application.Action{Kind: "tag.retry", TaskID: "task-a", ServiceID: "service", ModuleID: "module", Payload: map[string]any{"operationId": "retag"}}
	if !s.queueTagActionWindow(action, window) || len(view.commands) != 1 {
		t.Fatal("enabled Genbu did not accept the retry for the active command queue")
	}
	setTestPlatformSwitch(s, "genbu", false)
	view.commandRunning = false
	s.startNextTagJobCommand(scope, view, backend, window)
	if len(view.commands) != 0 || view.commandRunning {
		t.Fatal("queued RETAG remained runnable after Genbu was disabled")
	}
	select {
	case action := <-backend.actions:
		t.Fatalf("queued RETAG reached backend after Genbu was disabled: %+v", action)
	default:
	}
}

func TestTagGenbuDisableCancelsInFlightStatusAndRetainsLocalRead(t *testing.T) {
	s, _, w := tagDesktopTestApp()
	s.tab = taskTagTab
	record := application.TagRecord{ID: "running", TaskID: "task-a", ServiceID: "service", ModuleID: "module", Tag: "fixture-tag", State: "SUCCESS", GenbuStatus: domain.GenbuTagProbeStatus{Build: domain.GenbuUnknown, UAT: domain.GenbuUnknown}}
	backend := &tagPollTestBackend{records: []application.TagRecord{record}, calls: make(chan tagPollTestCall, 4)}
	s.backend = backend
	s.snapshot.TagHistory = []application.TagRecord{record}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.closeTagGenbuAutoProbe(ctx); err != nil {
			t.Error(err)
		}
		w.drain()
	}()

	s.observeTagGenbuAutoProbeWindow(w)
	var probe tagPollTestCall
	w.until(t, func() bool {
		select {
		case probe = <-backend.calls:
			return true
		default:
			return false
		}
	})
	oldSession := s.releaseUnit.state.tagGenbuAutoProbe.current
	setTestPlatformSwitch(s, "genbu", false)
	s.disableTagGenbuOptionalState()
	if oldSession.ctx.Err() == nil || probe.ctx.Err() == nil {
		t.Fatal("disabling Genbu did not cancel the active timer and status request")
	}
	s.observeTagGenbuAutoProbeWindow(w)
	w.until(t, func() bool {
		backend.mu.Lock()
		defer backend.mu.Unlock()
		return backend.historyReads == 2 && s.releaseUnit.state.tagGenbuAutoProbe.current.request == nil
	})
	if s.releaseUnit.state.tagGenbuAutoProbe.current.polling {
		t.Fatal("disabled Genbu restarted its status timer")
	}
	select {
	case call := <-backend.calls:
		t.Fatalf("status request restarted after Genbu was disabled: %+v", call.action)
	default:
	}
}
