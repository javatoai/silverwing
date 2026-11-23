package desktop

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
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

func TestTaskMenuDoesNotProbeWorkspaceTools(t *testing.T) {
	s, _ := taskMenuFixture(t)
	configureTaskOpenTools(t, s, []string{"codex", "cursor"})
	backend := &taskOpenToolsProbeBackend{calls: make(chan *taskOpenToolsProbeCall, 1)}
	s.backend = backend
	tester := taskNavigationTester(s, 340, 620)
	for range 3 {
		if err := tester.RightClick(s.taskUnit.state.tasks[1].title); err != nil {
			t.Fatal(err)
		}
		tester.Frame()
		tester.Key(0, ui.KeyEscape)
	}
	select {
	case call := <-backend.calls:
		call.reply <- taskOpenToolsProbeReply{}
		t.Fatalf("rendering task menus dispatched %+v", call.action)
	case <-time.After(20 * time.Millisecond):
	}
}
