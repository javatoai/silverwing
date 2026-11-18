package desktop

import (
	"context"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/workspace"
)

type taskAppendRemoteCheckCall struct {
	ctx    context.Context
	action application.Action
	reply  chan []workspace.WorktreeRemoteCheck
}

type taskAppendRemoteCheckFake struct {
	*materialsGitFakeBackend
	calls chan taskAppendRemoteCheckCall
}

func (b *taskAppendRemoteCheckFake) CheckTaskAppendRemotes(ctx context.Context, action application.Action) ([]workspace.WorktreeRemoteCheck, error) {
	call := taskAppendRemoteCheckCall{ctx: ctx, action: action, reply: make(chan []workspace.WorktreeRemoteCheck, 1)}
	b.calls <- call
	return <-call.reply, nil
}

func TestTaskAppendRemoteCheckIsCancellableAndPreservesForm(t *testing.T) {
	s, tester := appendServiceToggleFixture(t, 960, false)
	state := s.workspaceUnit.state.workspaceAppend
	b := &taskAppendRemoteCheckFake{materialsGitFakeBackend: &materialsGitFakeBackend{}, calls: make(chan taskAppendRemoteCheckCall, 2)}
	s.backend = b
	updates := make(chan func(), 4)
	s.taskUnit.state.taskContextUpdate = func(update func()) { updates <- update }
	if err := tester.Click("合成服务甲"); err != nil {
		t.Fatal(err)
	}
	draft := state.moduleData["alpha"]["api"]
	draft.TargetBranch, draft.LastTargetBranch, draft.TargetEdited = "feature/manual", "feature/manual", true
	tester.Frame()
	if err := tester.Click("检查远端连接"); err != nil {
		t.Fatal(err)
	}
	var call taskAppendRemoteCheckCall
	select {
	case call = <-b.calls:
	case <-time.After(3 * time.Second):
		t.Fatal("missing remote diagnosis")
	}
	selections := call.action.Payload["serviceSelections"].([]workspace.TaskServiceSelection)
	if call.action.TaskID != state.taskID || len(selections) != 1 || selections[0].Modules[0].BaseRef != "origin/main" || selections[0].Modules[0].TargetBranch != "feature/manual" {
		t.Fatalf("connection check did not preserve the selected repository and base: %+v", selections)
	}
	if err := tester.Click("取消检查"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-call.ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not cancel the remote diagnosis")
	}
	call.reply <- []workspace.WorktreeRemoteCheck{{Repository: "old remote", BaseRef: "origin/main"}}
	materialsGitNextUpdate(t, updates)
	if !state.open || !state.selectedServices["alpha"] || draft.TargetBranch != "feature/manual" || state.previewSignature != "" || state.pending ||
		state.remoteCheck.message != "远端连接检查已取消。" || len(state.remoteCheck.checks) != 0 {
		t.Fatal("remote diagnosis changed the form, bypassed preflight or retained a canceled result")
	}
}
