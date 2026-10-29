package desktop

import (
	"context"
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

// taskOpenToolsProbeBackend is shared by asynchronous requirement-comment tests.
type taskOpenToolsProbeBackend struct {
	calls chan *taskOpenToolsProbeCall
	count atomic.Int32
}

func (*taskOpenToolsProbeBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, nil
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

func nextTaskOpenToolsUpdate(t *testing.T, updates chan func()) func() {
	t.Helper()
	select {
	case update := <-updates:
		return update
	case <-time.After(time.Second):
		t.Fatal("异步任务更新未完成")
		return nil
	}
}

func TestTaskOpenToolAvailabilityIsCodexOnly(t *testing.T) {
	s := &app{
		settingsUnit: settingsController{state: settingsState{configJSON: `{"defaultWorkspaceToolIds":["codex"]}`}},
	}
	if !s.taskOpenToolEnabled("codex") || !s.taskOpenToolAvailable("codex") {
		t.Fatal("enabled Codex should remain visible and available without executable discovery")
	}
	if s.taskOpenToolEnabled("cursor") || s.taskOpenToolAvailable("cursor") {
		t.Fatal("retired Cursor integration remains enabled or available")
	}
}
