package desktop

import (
	"context"
	"errors"
	"testing"
	"time"

	"silverwing-go/internal/application"
	"silverwing-go/internal/workspace"
)

type projectMaterialsBackendFunc func(context.Context, application.Action) (application.ActionResult, error)

func (projectMaterialsBackendFunc) Snapshot(context.Context) (application.Snapshot, error) {
	panic("the project browser must not request a task snapshot")
}

func (f projectMaterialsBackendFunc) Dispatch(ctx context.Context, action application.Action) (application.ActionResult, error) {
	return f(ctx, action)
}

func TestProjectMaterialsBackendRoutesOnlyProjectFilesAndLinks(t *testing.T) {
	var forwarded application.Action
	backend := projectMaterialsBackendFunc(func(_ context.Context, action application.Action) (application.ActionResult, error) {
		forwarded = action
		return application.ActionResult{Message: "forwarded"}, nil
	})
	initial := workspace.MaterialsGitScope{Home: "profile", Root: "checkout", URL: "https://example.invalid/project", Branch: "main"}
	adapter := newProjectMaterialsBackend(backend, initial, t.Context(), nil)
	updated := initial
	updated.EnvironmentID = "new-environment"
	adapter.setScope(updated)
	const scopeKey = application.ProjectInstructionsScopePayloadKey
	for _, tc := range []struct {
		kind, path, forwardedKind string
		scoped                    bool
	}{
		{"document.read", "guide/setup.md", "document.read", true},
		{"system.open", "guide/setup.md", "document.open", true},
		{"system.open-with", "guide/setup.md", "document.open-with", true},
		{"system.open", "https://example.invalid", "system.open", false},
	} {
		action := application.Action{Kind: tc.kind, TaskID: "pane-owner", Path: tc.path, Payload: map[string]any{scopeKey: initial}}
		result, err := adapter.Dispatch(t.Context(), action)
		if err != nil || result.Message != "forwarded" || forwarded.Kind != tc.forwardedKind || forwarded.Path != tc.path || forwarded.TaskID != "" {
			t.Fatalf("%s routed incorrectly: %+v %+v %v", tc.kind, forwarded, result, err)
		}
		scope, scoped := forwarded.Payload[scopeKey].(workspace.MaterialsGitScope)
		if scoped != tc.scoped || scoped && scope != updated || action.Payload[scopeKey] != initial {
			t.Fatalf("%s used the wrong scope or changed the original action: %+v", tc.kind, forwarded.Payload)
		}
	}
	if _, err := adapter.Dispatch(t.Context(), application.Action{Kind: "system.terminal"}); err == nil {
		t.Fatal("project browser dispatched an unrelated system action")
	}
	if _, err := adapter.Snapshot(t.Context()); err == nil {
		t.Fatal("project browser accepted a task snapshot request")
	}
}

func TestProjectMaterialsBackendSerializesCheckoutAndRetainsDraftScope(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	lifetime, retire := context.WithCancel(ctx)
	defer retire()
	scope := workspace.MaterialsGitScope{Root: "checkout", Branch: "docs"}
	posted := make(chan application.Action, 4)
	backend := projectMaterialsBackendFunc(func(_ context.Context, action application.Action) (application.ActionResult, error) {
		posted <- action
		return application.ActionResult{}, nil
	})
	adapter := newProjectMaterialsBackend(backend, scope, lifetime, nil)
	nextScope := scope
	nextScope.Branch = "other"
	next := newProjectMaterialsBackend(backend, nextScope, ctx, adapter.checkout)
	started, done := make(chan struct{}), make(chan error, 1)
	go func() {
		done <- adapter.withCheckout(ctx, func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("checkout job did not start")
	}
	short, stop := context.WithTimeout(ctx, 50*time.Millisecond)
	defer stop()
	if _, err := next.Dispatch(short, application.Action{Kind: "document.list"}); !errors.Is(err, context.DeadlineExceeded) || len(posted) != 0 {
		t.Fatalf("a replacement pane reached the checkout during the previous sync: %v", err)
	}
	if _, err := adapter.Dispatch(ctx, application.Action{Kind: "document.draft"}); err != nil {
		t.Fatalf("draft unexpectedly waited for checkout availability: %v", err)
	}
	retire()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("retiring the pane did not cancel checkout activity: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("retired checkout job did not return")
	}
	if _, err := adapter.Dispatch(ctx, application.Action{Kind: "document.read"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("retired pane still accepted a file read: %v", err)
	}
	if _, err := adapter.Dispatch(ctx, application.Action{Kind: "document.draft-discard"}); err != nil {
		t.Fatalf("retired pane could not settle its old scoped draft: %v", err)
	}
	if len(posted) != 2 {
		t.Fatalf("backend received %d requests, want only the two draft requests", len(posted))
	}
	for range 2 {
		if action := <-posted; action.Payload[application.ProjectInstructionsScopePayloadKey] != scope {
			t.Fatalf("persisted draft used a different checkout scope: %+v", action)
		}
	}
	if _, err := next.Dispatch(ctx, application.Action{Kind: "document.list"}); err != nil {
		t.Fatalf("replacement pane could not read after retirement: %v", err)
	}
	if action := <-posted; action.Payload[application.ProjectInstructionsScopePayloadKey] != nextScope {
		t.Fatalf("replacement pane used the retired checkout scope: %+v", action)
	}
}
