package desktop

import (
	"context"
	"errors"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/workspace"
)

type workspaceDiffSessionReleaseBackend struct {
	actions chan application.Action
}

type cancelAfterDiffFilesBackend struct {
	actions chan application.Action
	cancel  context.CancelFunc
}

func (b *workspaceDiffSessionReleaseBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, nil
}

func (b *workspaceDiffSessionReleaseBackend) Dispatch(_ context.Context, action application.Action) (application.ActionResult, error) {
	b.actions <- action
	return application.ActionResult{}, nil
}

func (b *cancelAfterDiffFilesBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, nil
}

func (b *cancelAfterDiffFilesBackend) Dispatch(_ context.Context, action application.Action) (application.ActionResult, error) {
	if action.Kind == "workspace.diff-files" {
		b.cancel()
		return application.ActionResult{Data: workspace.CompareFilesResult{SessionID: "late-session"}}, nil
	}
	b.actions <- action
	return application.ActionResult{}, nil
}

func TestWorkspaceReviewPreviewRequiresCurrentSessionID(t *testing.T) {
	state := &workspaceHistoryState{
		TaskID: "task", ServiceID: "service", ModuleID: "module", Path: `C:\worktree`,
		Mode: "review", Open: true, CompareMode: workspaceReviewUncommitted, PreviewLoading: true,
		DiffSessionID: "current-session", SelectedPath: "src/a.go", SelectedKind: "MODIFIED",
		TargetCommit: "worktree:head:tree",
	}
	action := application.Action{
		Kind: "workspace.diff-preview", TaskID: state.TaskID, ServiceID: state.ServiceID,
		ModuleID: state.ModuleID, Path: state.Path,
		Payload: map[string]any{
			"sessionId": "stale-session",
			"compare":   map[string]any{"mode": workspaceReviewUncommitted, "expectedTargetCommit": state.TargetCommit},
			"change":    map[string]any{"path": state.SelectedPath, "kind": string(state.SelectedKind)},
		},
	}
	if state.matches(action) {
		t.Fatal("preview from a retired session was accepted")
	}
	action.Payload["sessionId"] = state.DiffSessionID
	if !state.matches(action) {
		t.Fatal("preview from the current session was rejected")
	}
}

func TestStaleDiffFilesResultReleasesReturnedSession(t *testing.T) {
	backend := &workspaceDiffSessionReleaseBackend{actions: make(chan application.Action, 1)}
	s := &app{backend: backend}
	ctx, cancel := context.WithCancel(context.Background())
	request := &workspaceHistoryJob{
		action: application.Action{Kind: "workspace.diff-files"},
		ctx:    ctx, cancel: cancel,
	}
	s.finishWorkspaceHistoryJob(request, application.ActionResult{Data: workspace.CompareFilesResult{SessionID: "late-session"}}, nil)
	select {
	case action := <-backend.actions:
		if action.Kind != workspace.CompareSessionCloseAction || action.Payload["sessionId"] != "late-session" {
			t.Fatalf("unexpected late-session cleanup action: %+v", action)
		}
	case <-time.After(time.Second):
		t.Fatal("stale diff-files response did not release its session")
	}
}

func TestCanceledDiffFilesJobPreservesAndReleasesLateSession(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	backend := &cancelAfterDiffFilesBackend{actions: make(chan application.Action, 1), cancel: cancel}
	s := &app{backend: backend}
	request := &workspaceHistoryJob{
		action: application.Action{Kind: "workspace.diff-files"},
		ctx:    ctx, cancel: cancel,
	}
	result, err := executeWorkspaceHistoryJob(backend, request)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled dispatch error = %v, want context.Canceled", err)
	}
	var files workspace.CompareFilesResult
	if decodeWorkspaceValue(result.Data, &files) != nil || files.SessionID != "late-session" {
		t.Fatalf("canceled dispatch discarded its session ID: %+v", result)
	}
	s.finishWorkspaceHistoryJob(request, result, err)
	select {
	case action := <-backend.actions:
		if action.Kind != workspace.CompareSessionCloseAction || action.Payload["sessionId"] != "late-session" {
			t.Fatalf("unexpected cancellation cleanup action: %+v", action)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled diff-files result did not release its late session")
	}
}
