package desktop

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"silverwing-go/internal/application"
	"silverwing-go/internal/appupdate"
)

type installTestBackend struct {
	calls    chan application.Action
	dispatch func(context.Context, application.Action) (application.ActionResult, error)
}

func (b *installTestBackend) Snapshot(context.Context) (application.Snapshot, error) {
	return application.Snapshot{}, errors.New("unexpected snapshot")
}
func (b *installTestBackend) Dispatch(ctx context.Context, a application.Action) (application.ActionResult, error) {
	b.calls <- a
	if b.dispatch != nil {
		return b.dispatch(ctx, a)
	}
	return application.ActionResult{}, nil
}

func TestAppUpdateInstallUsesNormalCloseAndAppliesOnlyAfterSuccessfulShutdown(t *testing.T) {
	s, _, _ := newUpdateTestApp(t, time.Now())
	backend := &installTestBackend{calls: make(chan application.Action, 8)}
	s.backend = backend
	owner := s.ensureAppUpdateState()
	owner.result = newerUpdateResult()
	owner.result.CanAutoUpdate = true
	closed := 0
	state := &appUpdateInstallState{home: s.snapshot.Home, backend: backend, status: appupdate.Snapshot{ID: "prepared", Phase: "ready"}, requestClose: func() { closed++ }}
	owner.install = state
	s.beginAppUpdateInstall()
	if closed != 1 || !state.exiting || len(backend.calls) != 0 {
		t.Fatal("ready update must request a normal close without installing first")
	}
	event := &mygo.CloseEvent{}
	s.busy = true
	if !s.guardAppUpdateClose(event) || !event.DefaultPrevented() || state.exiting {
		t.Fatal("busy operation did not prevent update exit")
	}
	s.busy = false
	state.exiting = true
	if err := s.applyAppUpdateAfterExit(errors.New("save failed")); err == nil || len(backend.calls) != 0 {
		t.Fatal("failed shutdown handed off an installation")
	}
	if err := s.applyAppUpdateAfterExit(nil); err != nil {
		t.Fatal(err)
	}
	call := <-backend.calls
	if call.Kind != "update.apply" || call.Payload["confirmed"] != true || call.Payload["jobId"] != "prepared" {
		t.Fatalf("unexpected handoff: %+v", call)
	}
}

func TestAppUpdateInstallCancellationRejectsLateReadyReceipt(t *testing.T) {
	s, _, posted := newUpdateTestApp(t, time.Now())
	started := make(chan struct{})
	backend := &installTestBackend{calls: make(chan application.Action, 8), dispatch: func(ctx context.Context, a application.Action) (application.ActionResult, error) {
		if a.Kind != "update.prepare" {
			return application.ActionResult{}, nil
		}
		close(started)
		<-ctx.Done()
		return application.ActionResult{Data: appupdate.Snapshot{ID: "late", Phase: "ready"}}, nil
	}}
	s.backend = backend
	owner := s.ensureAppUpdateState()
	owner.result = newerUpdateResult()
	owner.result.CanAutoUpdate = true
	s.beginAppUpdateInstall()
	<-started
	state := owner.install
	closed := false
	state.requestClose = func() { closed = true }
	s.cancelAppUpdateInstall()
	applyUpdatePost(t, posted)
	if closed || state.exiting || state.preparing || state.status.Phase != "cancelled" {
		t.Fatal("late ready receipt closed the application")
	}
	if len(backend.calls) != 1 {
		t.Fatal("cancellation must not dispatch an unscoped cancellation of a newer job")
	}
}

func TestAppUpdateInstallSaveConflictKeepsReadyPackage(t *testing.T) {
	s, _, _ := newUpdateTestApp(t, time.Now())
	owner := s.ensureAppUpdateState()
	state := &appUpdateInstallState{home: s.snapshot.Home, status: appupdate.Snapshot{ID: "ready", Phase: "ready"}, exiting: true}
	owner.install = state
	s.showDocumentCloseError(errors.New("hash conflict"))
	if state.exiting || state.status.Phase != "ready" || state.errorText != "hash conflict" {
		t.Fatal("save failure did not retain a retryable update")
	}
}
