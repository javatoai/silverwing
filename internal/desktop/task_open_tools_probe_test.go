package desktop

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"silverwing-go/internal/application"
)

type taskOpenToolsProbeReply struct {
	result application.ActionResult
	err    error
}

type taskOpenToolsProbeCall struct {
	action application.Action
	ctx    context.Context
	reply  chan taskOpenToolsProbeReply
}

type taskOpenToolsProbeBackend struct {
	calls chan *taskOpenToolsProbeCall
	count atomic.Int32
}

func (*taskOpenToolsProbeBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, errors.New("availability probe must not read a snapshot")
}

func (b *taskOpenToolsProbeBackend) Dispatch(ctx context.Context, action application.Action) (application.ActionResult, error) {
	b.count.Add(1)
	call := &taskOpenToolsProbeCall{action: action, ctx: ctx, reply: make(chan taskOpenToolsProbeReply, 1)}
	b.calls <- call
	select {
	case reply := <-call.reply:
		return reply.result, reply.err
	case <-ctx.Done():
		return application.ActionResult{}, ctx.Err()
	}
}

func taskOpenToolsProbeFixture(t *testing.T) (*app, *taskOpenToolsProbeBackend, chan func()) {
	t.Helper()
	b := &taskOpenToolsProbeBackend{calls: make(chan *taskOpenToolsProbeCall, 8)}
	updates := make(chan func(), 8)
	s := &app{
		taskUnit: taskController{state: taskState{taskContextUpdate: func(update func()) { updates <- update },
			selected: 1}},
		materialsUnit: materialsController{state: materialsState{documentContent: "保留任务草稿",
			documentDirty: true}},
		settingsUnit: settingsController{state: settingsState{configJSON: `{"defaultWorkspaceToolIds":["codex","cursor"]}`}},
		backend:      b,
		snapshot:     application.Snapshot{Home: t.TempDir()},
	}
	t.Cleanup(s.taskUnit.stopTaskOpenTools)
	return s, b, updates
}

func nextTaskOpenToolsProbe(t *testing.T, b *taskOpenToolsProbeBackend) *taskOpenToolsProbeCall {
	t.Helper()
	select {
	case call := <-b.calls:
		a := call.action
		if a.Kind != "workspace.tools-options" || a.TaskID != "" || a.Path != "" ||
			a.ServiceID != "" || a.ModuleID != "" || a.Text != "" || len(a.Payload) != 1 ||
			payloadString(a.Payload, "taskCreateToken") != "task-menu-tools" {
			t.Fatalf("availability observation dispatched a launch or another action: %+v", a)
		}
		return call
	case <-time.After(time.Second):
		t.Fatal("availability probe was not dispatched")
		return nil
	}
}

func nextTaskOpenToolsUpdate(t *testing.T, updates chan func()) func() {
	t.Helper()
	select {
	case update := <-updates:
		return update
	case <-time.After(time.Second):
		t.Fatal("availability probe did not settle")
		return nil
	}
}

func taskOpenToolsProbeOptions(available bool) application.ActionResult {
	return application.ActionResult{Data: []taskCreationWorkspaceToolOption{
		{ID: "codex", Available: true}, {ID: "cursor", Available: available},
	}}
}

func TestTaskOpenToolsProbeRecoversWithoutRetryStorm(t *testing.T) {
	for _, mode := range []string{"error", "decode", "null-options", "timeout", "canceled", "unavailable"} {
		t.Run(mode, func(t *testing.T) {
			s, b, updates := taskOpenToolsProbeFixture(t)
			now := time.Now()
			s.observeTaskOpenToolsAt(now)
			state := s.taskUnit.state.taskOpenTools
			call := nextTaskOpenToolsProbe(t, b)
			for range 100 {
				s.observeTaskOpenToolsAt(now.Add(time.Hour))
			}
			if !state.running || b.count.Load() != 1 {
				t.Fatal("a running availability probe was duplicated")
			}
			reply := taskOpenToolsProbeReply{}
			switch mode {
			case "error":
				reply.err = errors.New("temporary tool discovery failure")
			case "decode":
				reply.result.Data = "malformed options"
			case "null-options":
				reply.result.Data = nil
			case "timeout":
				reply.err = context.DeadlineExceeded
			case "canceled":
				state.cancel()
			case "unavailable":
				reply.result = taskOpenToolsProbeOptions(false)
			}
			if mode != "canceled" {
				call.reply <- reply
			}
			nextTaskOpenToolsUpdate(t, updates)()
			if state.running || b.count.Load() != 1 {
				t.Fatal("probe completion left a running request or dispatched another probe")
			}
			if mode == "unavailable" {
				for range 100 {
					s.observeTaskOpenToolsAt(now.Add(time.Hour))
				}
				if !state.loaded || s.taskOpenToolAvailable("cursor") || b.count.Load() != 1 {
					t.Fatal("a successful unavailable result was retried or enabled")
				}
			} else {
				if state.loaded || s.taskOpenToolAvailable("cursor") || state.retryAfter.IsZero() {
					t.Fatal("failed discovery did not remain disabled and retryable")
				}
				for range 100 {
					s.observeTaskOpenToolsAt(state.retryAfter.Add(-time.Nanosecond))
				}
				if b.count.Load() != 1 {
					t.Fatal("failed discovery caused a retry storm before the cooldown")
				}
				s.observeTaskOpenToolsAt(state.retryAfter)
				retry := nextTaskOpenToolsProbe(t, b)
				for range 100 {
					s.observeTaskOpenToolsAt(state.retryAfter.Add(time.Hour))
				}
				if b.count.Load() != 2 || !state.running {
					t.Fatal("the recovery probe ran in parallel with another probe")
				}
				retry.reply <- taskOpenToolsProbeReply{result: taskOpenToolsProbeOptions(true)}
				nextTaskOpenToolsUpdate(t, updates)()
				for range 100 {
					s.observeTaskOpenToolsAt(now.Add(24 * time.Hour))
				}
				if !state.loaded || state.running || !state.retryAfter.IsZero() ||
					!s.taskOpenToolAvailable("cursor") || b.count.Load() != 2 {
					t.Fatal("successful recovery did not enable Cursor and cache its result")
				}
			}
			if s.taskUnit.state.selected != 1 || !s.materialsUnit.state.documentDirty || s.materialsUnit.state.documentContent != "保留任务草稿" || len(b.calls) != 0 {
				t.Fatal("probing changed task/draft state or dispatched an extra action")
			}
		})
	}
}

func TestTaskOpenToolsProbeRejectsQueuedResultsFromOldScope(t *testing.T) {
	for _, change := range []string{"home", "config"} {
		for _, callbackOrder := range []string{"before-observe", "after-observe"} {
			t.Run(change+"/"+callbackOrder, func(t *testing.T) {
				s, b, updates := taskOpenToolsProbeFixture(t)
				s.observeTaskOpenTools()
				old := s.taskUnit.state.taskOpenTools
				call := nextTaskOpenToolsProbe(t, b)
				call.reply <- taskOpenToolsProbeReply{result: taskOpenToolsProbeOptions(true)}
				oldUpdate := nextTaskOpenToolsUpdate(t, updates)
				if change == "home" {
					s.snapshot.Home += "-other"
				} else {
					s.settingsUnit.state.configJSON = `{"defaultWorkspaceToolIds":["cursor"]}`
				}
				if callbackOrder == "before-observe" {
					oldUpdate()
					if old.loaded || s.taskOpenToolAvailable("cursor") {
						t.Fatal("a queued result accepted changed home/config before observation")
					}
				}
				s.observeTaskOpenTools()
				current := s.taskUnit.state.taskOpenTools
				next := nextTaskOpenToolsProbe(t, b)
				if current == old || current.loaded || !current.running {
					t.Fatal("scope change did not replace the old discovery request")
				}
				if callbackOrder == "after-observe" {
					oldUpdate()
					if old.loaded || current.loaded || !current.running || s.taskOpenToolAvailable("cursor") {
						t.Fatal("old discovery overwrote its replacement")
					}
				}
				next.reply <- taskOpenToolsProbeReply{result: taskOpenToolsProbeOptions(false)}
				nextTaskOpenToolsUpdate(t, updates)()
				if !current.loaded || s.taskOpenToolAvailable("cursor") || b.count.Load() != 2 ||
					s.taskUnit.state.selected != 1 || !s.materialsUnit.state.documentDirty || s.materialsUnit.state.documentContent != "保留任务草稿" {
					t.Fatal("replacement probe did not preserve its own result/task state")
				}
			})
		}
	}
}

func TestTaskOpenToolsProbeStopCancelsAndRejectsLateCompletion(t *testing.T) {
	s, b, updates := taskOpenToolsProbeFixture(t)
	s.observeTaskOpenTools()
	call := nextTaskOpenToolsProbe(t, b)
	s.taskUnit.stopTaskOpenTools()
	select {
	case <-call.ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("stopping discovery did not cancel its worker")
	}
	nextTaskOpenToolsUpdate(t, updates)()
	if s.taskUnit.state.taskOpenTools != nil || s.taskOpenToolAvailable("cursor") || b.count.Load() != 1 {
		t.Fatal("stopped discovery accepted a late result")
	}
}

func TestTaskOpenToolsProbeSkipsDiscoveryWhenCursorDisabled(t *testing.T) {
	s, b, _ := taskOpenToolsProbeFixture(t)
	for _, config := range []string{`{"defaultWorkspaceToolIds":[]}`, `{"defaultWorkspaceToolIds":["codex"]}`} {
		s.settingsUnit.state.configJSON = config
		for range 10 {
			s.observeTaskOpenToolsAt(time.Now().Add(time.Hour))
		}
		if s.taskUnit.state.taskOpenTools.running || b.count.Load() != 0 || s.taskOpenToolAvailable("cursor") {
			t.Fatal("disabled Cursor caused discovery work")
		}
	}
}
